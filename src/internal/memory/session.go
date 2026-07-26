package memory

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"onecode/internal/llm"
)

const sessionRetention = 30 * 24 * time.Hour

var sessionIDPattern = regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{4}$`)

// RecordType identifies how a JSONL record changes the effective history.
type RecordType string

const (
	RecordMessage  RecordType = "message"
	RecordSnapshot RecordType = "snapshot"
)

// SessionRecord is one independently decodable JSONL line.
type SessionRecord struct {
	Type      RecordType    `json:"type"`
	Timestamp time.Time     `json:"timestamp"`
	Message   *llm.Message  `json:"message,omitempty"`
	Messages  []llm.Message `json:"messages,omitempty"`
}

// SessionWarning describes a recoverable problem in one session file.
type SessionWarning struct {
	Path    string
	Line    int
	Message string
}

// SessionInfo is derived from the JSONL stream; it is never stored separately.
type SessionInfo struct {
	ID           string
	Title        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
	WarningCount int
}

// RestoreResult is the deterministic effective history recovered from JSONL.
type RestoreResult struct {
	Info         SessionInfo
	Messages     []llm.Message
	LastActiveAt time.Time
	SkippedLines int
	Truncated    bool
	Warnings     []SessionWarning
}

// SessionStore owns project-local JSONL sessions.
type SessionStore struct {
	Root   string
	now    func() time.Time
	random io.Reader
}

// NewSessionStore creates a store rooted at <project>/.onecode/sessions.
func NewSessionStore(projectRoot string) *SessionStore {
	return &SessionStore{Root: filepath.Join(projectRoot, ".onecode", "sessions")}
}

// Create starts a new append-only session journal.
func (s *SessionStore) Create() (*SessionJournal, error) {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return nil, fmt.Errorf("创建会话目录失败: %w", err)
	}
	for attempt := 0; attempt < 8; attempt++ {
		id, err := s.newID()
		if err != nil {
			return nil, err
		}
		journal, err := s.createJournal(id)
		if err == nil {
			return journal, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("创建会话失败: 随机会话 ID 连续冲突")
}

// Open reopens an existing session for append without rewriting old records.
func (s *SessionStore) Open(id string) (*SessionJournal, error) {
	path, err := s.sessionPath(id)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return nil, fmt.Errorf("打开会话 %s 失败: %w", id, err)
	}
	return newSessionJournal(id, path, file, s.clock), nil
}

// Load restores one session without modifying its file.
func (s *SessionStore) Load(id string) (RestoreResult, error) {
	path, err := s.sessionPath(id)
	if err != nil {
		return RestoreResult{}, err
	}
	return s.loadPath(id, path)
}

// List returns valid non-empty sessions ordered by last activity descending.
func (s *SessionStore) List() ([]SessionInfo, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("扫描会话目录失败: %w", err)
	}

	infos := make([]SessionInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id, ok := sessionIDFromName(entry.Name())
		if !ok {
			continue
		}
		result, err := s.loadPath(id, filepath.Join(s.Root, entry.Name()))
		if err != nil || len(result.Messages) == 0 {
			continue
		}
		infos = append(infos, result.Info)
	}
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].UpdatedAt.Equal(infos[j].UpdatedAt) {
			return infos[i].ID > infos[j].ID
		}
		return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
	})
	return infos, nil
}

// Latest restores the most recent valid session within the retention window.
func (s *SessionStore) Latest() (RestoreResult, error) {
	infos, err := s.List()
	if err != nil {
		return RestoreResult{}, err
	}
	cutoff := s.clock().Add(-sessionRetention)
	for _, info := range infos {
		if info.UpdatedAt.Before(cutoff) {
			continue
		}
		return s.Load(info.ID)
	}
	return RestoreResult{}, os.ErrNotExist
}

// Cleanup removes expired valid sessions while preserving activeIDs.
func (s *SessionStore) Cleanup(before time.Time, activeIDs ...string) error {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("扫描会话目录失败: %w", err)
	}
	active := make(map[string]struct{}, len(activeIDs))
	for _, id := range activeIDs {
		active[id] = struct{}{}
	}

	var cleanupErrors []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id, ok := sessionIDFromName(entry.Name())
		if !ok {
			continue
		}
		if _, keep := active[id]; keep {
			continue
		}
		path := filepath.Join(s.Root, entry.Name())
		result, err := s.loadPath(id, path)
		if err != nil || result.LastActiveAt.IsZero() || !result.LastActiveAt.Before(before) {
			continue
		}
		if err := os.Remove(path); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("删除过期会话 %s 失败: %w", id, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func (s *SessionStore) createJournal(id string) (*SessionJournal, error) {
	path := filepath.Join(s.Root, id+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("创建会话 %s 失败: %w", id, err)
	}
	return newSessionJournal(id, path, file, s.clock), nil
}

func (s *SessionStore) newID() (string, error) {
	random := s.random
	if random == nil {
		random = rand.Reader
	}
	suffix := make([]byte, 2)
	if _, err := io.ReadFull(random, suffix); err != nil {
		return "", fmt.Errorf("生成会话 ID 失败: %w", err)
	}
	return s.clock().Format("20060102-150405") + "-" + hex.EncodeToString(suffix), nil
}

func (s *SessionStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *SessionStore) sessionPath(id string) (string, error) {
	if !sessionIDPattern.MatchString(id) {
		return "", fmt.Errorf("无效会话 ID: %s", id)
	}
	return filepath.Join(s.Root, id+".jsonl"), nil
}

func (s *SessionStore) loadPath(id, path string) (RestoreResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("读取会话 %s 失败: %w", id, err)
	}
	defer file.Close()

	timedMessages := make([]timedMessage, 0)
	warnings := make([]SessionWarning, 0)
	reader := bufio.NewReader(file)
	lineNumber := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNumber++
			line = []byte(strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"))
			if len(strings.TrimSpace(string(line))) > 0 {
				var record SessionRecord
				if err := json.Unmarshal(line, &record); err != nil {
					warnings = append(warnings, SessionWarning{Path: path, Line: lineNumber, Message: fmt.Sprintf("JSON 解析失败: %v", err)})
				} else if err := validateSessionRecord(record); err != nil {
					warnings = append(warnings, SessionWarning{Path: path, Line: lineNumber, Message: err.Error()})
				} else if record.Type == RecordMessage {
					timedMessages = append(timedMessages, timedMessage{message: *record.Message, timestamp: record.Timestamp})
				} else {
					timedMessages = timedMessages[:0]
					for _, message := range record.Messages {
						timedMessages = append(timedMessages, timedMessage{message: message, timestamp: record.Timestamp})
					}
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return RestoreResult{}, fmt.Errorf("读取会话 %s 失败: %w", id, readErr)
			}
			break
		}
	}

	prefix, protocolWarning := validMessagePrefix(timedMessages)
	truncated := prefix < len(timedMessages)
	if protocolWarning != "" {
		warnings = append(warnings, SessionWarning{Path: path, Message: protocolWarning})
	}
	timedMessages = timedMessages[:prefix]
	messages := make([]llm.Message, len(timedMessages))
	for i, message := range timedMessages {
		messages[i] = message.message
	}
	createdAt := sessionCreatedAt(id, s.clock().Location())
	lastActiveAt := createdAt
	if len(timedMessages) > 0 {
		lastActiveAt = timedMessages[len(timedMessages)-1].timestamp
	}
	info := SessionInfo{
		ID:           id,
		Title:        sessionTitle(messages, id),
		CreatedAt:    createdAt,
		UpdatedAt:    lastActiveAt,
		MessageCount: len(messages),
		WarningCount: len(warnings),
	}
	return RestoreResult{
		Info:         info,
		Messages:     messages,
		LastActiveAt: lastActiveAt,
		SkippedLines: countLineWarnings(warnings),
		Truncated:    truncated,
		Warnings:     warnings,
	}, nil
}

// SessionJournal serializes complete JSONL records for one active session.
type SessionJournal struct {
	mu     sync.Mutex
	id     string
	path   string
	file   *os.File
	writer *bufio.Writer
	now    func() time.Time
	closed bool
}

func newSessionJournal(id, path string, file *os.File, now func() time.Time) *SessionJournal {
	return &SessionJournal{id: id, path: path, file: file, writer: bufio.NewWriter(file), now: now}
}

// ID returns the active session identifier.
func (j *SessionJournal) ID() string { return j.id }

// Path returns the JSONL path for diagnostics and tests.
func (j *SessionJournal) Path() string { return j.path }

// AppendMessage persists one complete conversation message.
func (j *SessionJournal) AppendMessage(message llm.Message) error {
	return j.appendRecord(SessionRecord{Type: RecordMessage, Timestamp: j.clock(), Message: &message})
}

// AppendSnapshot persists the complete effective model-visible history.
func (j *SessionJournal) AppendSnapshot(messages []llm.Message) error {
	return j.appendRecord(SessionRecord{Type: RecordSnapshot, Timestamp: j.clock(), Messages: append([]llm.Message(nil), messages...)})
}

// Close flushes and closes the journal. It is safe to call more than once.
func (j *SessionJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	flushErr := j.writer.Flush()
	closeErr := j.file.Close()
	return errors.Join(flushErr, closeErr)
}

func (j *SessionJournal) appendRecord(record SessionRecord) error {
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("编码会话记录失败: %w", err)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return fmt.Errorf("会话 %s 已关闭", j.id)
	}
	if _, err := j.writer.Write(line); err != nil {
		return fmt.Errorf("写入会话 %s 失败: %w", j.id, err)
	}
	if err := j.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("写入会话 %s 失败: %w", j.id, err)
	}
	if err := j.writer.Flush(); err != nil {
		return fmt.Errorf("刷新会话 %s 失败: %w", j.id, err)
	}
	return nil
}

func (j *SessionJournal) clock() time.Time {
	if j.now != nil {
		return j.now()
	}
	return time.Now()
}

type timedMessage struct {
	message   llm.Message
	timestamp time.Time
}

func validateSessionRecord(record SessionRecord) error {
	if record.Timestamp.IsZero() {
		return fmt.Errorf("会话记录缺少 timestamp")
	}
	switch record.Type {
	case RecordMessage:
		if record.Message == nil {
			return fmt.Errorf("message 记录缺少 message")
		}
	case RecordSnapshot:
		if record.Messages == nil {
			return fmt.Errorf("snapshot 记录缺少 messages")
		}
	default:
		return fmt.Errorf("未知会话记录类型: %s", record.Type)
	}
	return nil
}

func validMessagePrefix(messages []timedMessage) (int, string) {
	pending := make([]string, 0)
	groupStart := -1
	completed := 0
	for i, item := range messages {
		message := item.message
		switch message.Role {
		case "user":
			if len(pending) > completed {
				return groupStart, "工具调用缺少结果，已截断到最长合法消息前缀"
			}
		case "assistant":
			if len(pending) > completed {
				return groupStart, "工具调用结果不完整，已截断到最长合法消息前缀"
			}
			pending = pending[:0]
			completed = 0
			groupStart = -1
			if len(message.ToolCalls) > 0 {
				groupStart = i
				seen := make(map[string]struct{}, len(message.ToolCalls))
				for _, call := range message.ToolCalls {
					if strings.TrimSpace(call.ID) == "" {
						return i, "工具调用缺少 ID，已截断到最长合法消息前缀"
					}
					if _, exists := seen[call.ID]; exists {
						return i, "工具调用 ID 重复，已截断到最长合法消息前缀"
					}
					seen[call.ID] = struct{}{}
					pending = append(pending, call.ID)
				}
			}
		case "tool":
			if message.ToolResult == nil || groupStart < 0 || completed >= len(pending) {
				return i, "存在孤立工具结果，已截断到最长合法消息前缀"
			}
			if message.ToolResult.ToolUseID != pending[completed] {
				return groupStart, "工具结果 ID 或顺序不匹配，已截断到最长合法消息前缀"
			}
			completed++
		default:
			return i, fmt.Sprintf("未知消息角色 %q，已截断到最长合法消息前缀", message.Role)
		}
	}
	if len(pending) > completed {
		return groupStart, "会话末尾工具调用缺少结果，已截断到最长合法消息前缀"
	}
	return len(messages), ""
}

func sessionTitle(messages []llm.Message, fallback string) string {
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		content := strings.TrimSpace(strings.ReplaceAll(message.Content, "\r\n", "\n"))
		firstLine := strings.SplitN(content, "\n", 2)[0]
		title := strings.Join(strings.Fields(firstLine), " ")
		if title == "" {
			continue
		}
		runes := []rune(title)
		if len(runes) > 60 {
			title = string(runes[:60])
		}
		return title
	}
	return fallback
}

func sessionCreatedAt(id string, location *time.Location) time.Time {
	createdAt, err := time.ParseInLocation("20060102-150405", id[:15], location)
	if err != nil {
		return time.Time{}
	}
	return createdAt
}

func sessionIDFromName(name string) (string, bool) {
	if filepath.Ext(name) != ".jsonl" {
		return "", false
	}
	id := strings.TrimSuffix(name, ".jsonl")
	return id, sessionIDPattern.MatchString(id)
}

func countLineWarnings(warnings []SessionWarning) int {
	count := 0
	for _, warning := range warnings {
		if warning.Line > 0 {
			count++
		}
	}
	return count
}

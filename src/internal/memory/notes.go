package memory

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	indexMaxLines       = 200
	indexMaxBytes       = 25 * 1024
	userIndexShareLines = indexMaxLines / 4
	userIndexShareBytes = indexMaxBytes / 4
)

// Scope identifies whether a note belongs to the current project or the user.
type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// NoteCategory classifies durable memory content.
type NoteCategory string

const (
	CategoryPreference       NoteCategory = "preference"
	CategoryCorrection       NoteCategory = "correction"
	CategoryProjectKnowledge NoteCategory = "project_knowledge"
	CategoryReference        NoteCategory = "reference"
)

// Note is one user-readable Markdown memory item.
type Note struct {
	ID              string       `yaml:"id" json:"id"`
	Scope           Scope        `yaml:"scope" json:"scope"`
	Category        NoteCategory `yaml:"category" json:"category"`
	Title           string       `yaml:"title" json:"title"`
	Summary         string       `yaml:"summary" json:"summary"`
	SourceSessionID string       `yaml:"source_session_id,omitempty" json:"source_session_id,omitempty"`
	CreatedAt       time.Time    `yaml:"created_at" json:"created_at,omitempty"`
	UpdatedAt       time.Time    `yaml:"updated_at" json:"updated_at,omitempty"`
	Body            string       `yaml:"-" json:"body"`
}

// MutationOperation is the only set of changes accepted from the memory LLM.
type MutationOperation string

const (
	MutationSkip   MutationOperation = "skip"
	MutationCreate MutationOperation = "create"
	MutationUpdate MutationOperation = "update"
)

// NoteMutation requests one validated local note change.
type NoteMutation struct {
	Operation MutationOperation `json:"operation"`
	TargetID  string            `json:"target_id,omitempty"`
	Note      Note              `json:"note,omitempty"`
}

// NoteStore owns user and project Markdown memory roots.
type NoteStore struct {
	UserRoot    string
	ProjectRoot string
	Enabled     bool
	now         func() time.Time
	random      io.Reader
	write       func(string, []byte, os.FileMode) error
}

// NewNoteStore creates user- and project-scoped memory stores.
func NewNoteStore(projectRoot, userHome string, enabled bool) *NoteStore {
	userRoot := ""
	if strings.TrimSpace(userHome) != "" {
		userRoot = filepath.Join(userHome, ".onecode", "memory")
	}
	return &NoteStore{
		ProjectRoot: filepath.Join(projectRoot, ".onecode", "memory"),
		UserRoot:    userRoot,
		Enabled:     enabled,
	}
}

// Apply validates local storage invariants, writes notes, then rebuilds indexes.
func (s *NoteStore) Apply(mutations []NoteMutation) error {
	if !s.Enabled {
		return nil
	}
	for _, mutation := range mutations {
		switch mutation.Operation {
		case MutationSkip:
			continue
		case MutationCreate:
			changed, scope, err := s.create(mutation.Note)
			if err != nil {
				return err
			}
			if changed {
				if err := s.RebuildIndex(scope); err != nil {
					return err
				}
			}
		case MutationUpdate:
			changed, scope, err := s.update(mutation.TargetID, mutation.Note)
			if err != nil {
				return err
			}
			if changed {
				if err := s.RebuildIndex(scope); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("未知记忆操作: %s", mutation.Operation)
		}
	}
	return nil
}

// RebuildIndex regenerates one scope's bounded INDEX.md from actual notes.
func (s *NoteStore) RebuildIndex(scope Scope) error {
	root, err := s.rootFor(scope)
	if err != nil {
		return err
	}
	notes, err := s.readAllNotes(scope)
	if err != nil {
		return err
	}
	sort.Slice(notes, func(i, j int) bool {
		if notes[i].UpdatedAt.Equal(notes[j].UpdatedAt) {
			return notes[i].ID < notes[j].ID
		}
		return notes[i].UpdatedAt.After(notes[j].UpdatedAt)
	})

	lines := []string{"# OneCode Memory Index", ""}
	for _, note := range notes {
		line := fmt.Sprintf("- [%s](notes/%s.md) [%s] - %s",
			indexText(note.Title), note.ID, note.Category, indexText(note.Summary))
		lines = append(lines, line)
	}
	content := boundText(strings.Join(lines, "\n")+"\n", indexMaxLines, indexMaxBytes)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("创建记忆目录失败: %w", err)
	}
	if err := s.writeFile(filepath.Join(root, "INDEX.md"), []byte(content), 0o600); err != nil {
		return fmt.Errorf("更新 %s 记忆索引失败: %w", scope, err)
	}
	return nil
}

// LoadIndexes returns bounded user and project indexes for one model request.
func (s *NoteStore) LoadIndexes() (string, error) {
	if !s.Enabled {
		return "", nil
	}
	userContent, userErr := readOptionalFile(filepath.Join(s.UserRoot, "INDEX.md"))
	projectContent, projectErr := readOptionalFile(filepath.Join(s.ProjectRoot, "INDEX.md"))
	if userErr != nil || projectErr != nil {
		return "", errors.Join(userErr, projectErr)
	}
	userContent = boundText(userContent, userIndexShareLines, userIndexShareBytes)

	var out strings.Builder
	if strings.TrimSpace(userContent) != "" {
		out.WriteString("<user-memory-index>\n")
		out.WriteString(strings.TrimSpace(userContent))
		out.WriteString("\n</user-memory-index>")
	}
	usedLines := lineCount(out.String())
	usedBytes := out.Len()
	remainingLines := indexMaxLines - usedLines
	remainingBytes := indexMaxBytes - usedBytes
	if strings.TrimSpace(projectContent) != "" && remainingLines > 2 && remainingBytes > len("\n\n<project-memory-index>\n\n</project-memory-index>") {
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		open := "<project-memory-index>\n"
		close := "\n</project-memory-index>"
		out.WriteString(open)
		remainingLines = indexMaxLines - lineCount(out.String()) - 1
		remainingBytes = indexMaxBytes - out.Len() - len(close)
		out.WriteString(strings.TrimSpace(boundText(projectContent, remainingLines, remainingBytes)))
		out.WriteString(close)
	}
	return boundText(out.String(), indexMaxLines, indexMaxBytes), nil
}

func (s *NoteStore) create(note Note) (bool, Scope, error) {
	if err := validateNote(note, false); err != nil {
		return false, note.Scope, err
	}
	notes, err := s.readAllNotes(note.Scope)
	if err != nil {
		return false, note.Scope, err
	}
	for _, existing := range notes {
		if equivalentNote(existing, note) {
			return false, note.Scope, nil
		}
	}
	var id string
	for attempt := 0; attempt < 8; attempt++ {
		candidate, err := s.newID()
		if err != nil {
			return false, note.Scope, err
		}
		root, err := s.rootFor(note.Scope)
		if err != nil {
			return false, note.Scope, err
		}
		if _, err := os.Stat(filepath.Join(root, "notes", candidate+".md")); errors.Is(err, os.ErrNotExist) {
			id = candidate
			break
		} else if err != nil {
			return false, note.Scope, err
		}
	}
	if id == "" {
		return false, note.Scope, fmt.Errorf("创建记忆失败: 随机记忆 ID 连续冲突")
	}
	now := s.clock()
	note.ID = id
	note.CreatedAt = now
	note.UpdatedAt = now
	if err := s.persistNote(note); err != nil {
		return false, note.Scope, err
	}
	return true, note.Scope, nil
}

func (s *NoteStore) update(targetID string, replacement Note) (bool, Scope, error) {
	if !sessionIDPattern.MatchString(targetID) {
		return false, replacement.Scope, fmt.Errorf("无效记忆 ID: %s", targetID)
	}
	if err := validateScope(replacement.Scope); err != nil {
		return false, replacement.Scope, err
	}
	existing, err := s.readNote(replacement.Scope, targetID)
	if err != nil {
		return false, replacement.Scope, fmt.Errorf("更新目标 %s 不存在或不可读: %w", targetID, err)
	}
	replacement.ID = existing.ID
	replacement.CreatedAt = existing.CreatedAt
	replacement.UpdatedAt = s.clock()
	if replacement.SourceSessionID == "" {
		replacement.SourceSessionID = existing.SourceSessionID
	}
	if err := validateNote(replacement, true); err != nil {
		return false, replacement.Scope, err
	}
	if equivalentNote(existing, replacement) {
		return false, replacement.Scope, nil
	}
	if err := s.persistNote(replacement); err != nil {
		return false, replacement.Scope, err
	}
	return true, replacement.Scope, nil
}

func (s *NoteStore) persistNote(note Note) error {
	root, err := s.rootFor(note.Scope)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建记忆笔记目录失败: %w", err)
	}
	data, err := marshalNote(note)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, note.ID+".md")
	if err := s.writeFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入记忆笔记 %s 失败: %w", note.ID, err)
	}
	return nil
}

func (s *NoteStore) readAllNotes(scope Scope) ([]Note, error) {
	root, err := s.rootFor(scope)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "notes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取记忆笔记目录失败: %w", err)
	}
	notes := make([]Note, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".md")
		if !sessionIDPattern.MatchString(id) {
			continue
		}
		note, err := s.readNote(scope, id)
		if err != nil {
			continue
		}
		notes = append(notes, note)
	}
	return notes, nil
}

func (s *NoteStore) readNote(scope Scope, id string) (Note, error) {
	root, err := s.rootFor(scope)
	if err != nil {
		return Note{}, err
	}
	if !sessionIDPattern.MatchString(id) {
		return Note{}, fmt.Errorf("无效记忆 ID: %s", id)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes", id+".md"))
	if err != nil {
		return Note{}, err
	}
	note, err := unmarshalNote(data)
	if err != nil {
		return Note{}, err
	}
	if note.ID != id || note.Scope != scope {
		return Note{}, fmt.Errorf("记忆 frontmatter 与文件路径不一致")
	}
	return note, nil
}

func (s *NoteStore) rootFor(scope Scope) (string, error) {
	switch scope {
	case ScopeProject:
		if strings.TrimSpace(s.ProjectRoot) == "" {
			return "", fmt.Errorf("项目记忆目录未配置")
		}
		return s.ProjectRoot, nil
	case ScopeUser:
		if strings.TrimSpace(s.UserRoot) == "" {
			return "", fmt.Errorf("用户记忆目录未配置")
		}
		return s.UserRoot, nil
	default:
		return "", fmt.Errorf("未知记忆作用域: %s", scope)
	}
}

func (s *NoteStore) newID() (string, error) {
	random := s.random
	if random == nil {
		random = rand.Reader
	}
	suffix := make([]byte, 2)
	if _, err := io.ReadFull(random, suffix); err != nil {
		return "", fmt.Errorf("生成记忆 ID 失败: %w", err)
	}
	return s.clock().Format("20060102-150405") + "-" + hex.EncodeToString(suffix), nil
}

func (s *NoteStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *NoteStore) writeFile(path string, data []byte, mode os.FileMode) error {
	if s.write != nil {
		return s.write(path, data, mode)
	}
	return atomicWriteFile(path, data, mode)
}

func marshalNote(note Note) ([]byte, error) {
	metadata := note
	metadata.Body = ""
	frontmatter, err := yaml.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("编码记忆 frontmatter 失败: %w", err)
	}
	return []byte("---\n" + string(frontmatter) + "---\n\n" + strings.TrimSpace(note.Body) + "\n"), nil
}

func unmarshalNote(data []byte) (Note, error) {
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return Note{}, fmt.Errorf("记忆文件缺少 frontmatter")
	}
	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		return Note{}, fmt.Errorf("记忆文件 frontmatter 未闭合")
	}
	end += 4
	var note Note
	if err := yaml.Unmarshal([]byte(content[4:end]), &note); err != nil {
		return Note{}, fmt.Errorf("解析记忆 frontmatter 失败: %w", err)
	}
	note.Body = strings.TrimSpace(content[end+5:])
	if err := validateNote(note, true); err != nil {
		return Note{}, err
	}
	return note, nil
}

func validateNote(note Note, requireID bool) error {
	if err := validateScope(note.Scope); err != nil {
		return err
	}
	if !validCategory(note.Category) {
		return fmt.Errorf("未知记忆分类: %s", note.Category)
	}
	if requireID && !sessionIDPattern.MatchString(note.ID) {
		return fmt.Errorf("无效记忆 ID: %s", note.ID)
	}
	if strings.TrimSpace(note.Title) == "" || strings.TrimSpace(note.Summary) == "" || strings.TrimSpace(note.Body) == "" {
		return fmt.Errorf("记忆标题、摘要和正文不能为空")
	}
	return nil
}

func validateScope(scope Scope) error {
	if scope != ScopeProject && scope != ScopeUser {
		return fmt.Errorf("未知记忆作用域: %s", scope)
	}
	return nil
}

func validCategory(category NoteCategory) bool {
	switch category {
	case CategoryPreference, CategoryCorrection, CategoryProjectKnowledge, CategoryReference:
		return true
	default:
		return false
	}
}

func equivalentNote(left, right Note) bool {
	return left.Scope == right.Scope &&
		left.Category == right.Category &&
		normalizeMemoryText(left.Title) == normalizeMemoryText(right.Title) &&
		normalizeMemoryText(left.Summary) == normalizeMemoryText(right.Summary) &&
		normalizeMemoryText(left.Body) == normalizeMemoryText(right.Body)
}

func normalizeMemoryText(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(value, "\r\n", "\n")), " ")
}

func indexText(value string) string {
	value = normalizeMemoryText(value)
	value = strings.ReplaceAll(value, "[", "\\[")
	return strings.ReplaceAll(value, "]", "\\]")
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".onecode-memory-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func readOptionalFile(path string) (string, error) {
	if strings.TrimSpace(filepath.Dir(path)) == "." {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("读取记忆索引 %s 失败: %w", path, err)
	}
	return string(data), nil
}

func boundText(value string, maxLines, maxBytes int) string {
	if maxLines <= 0 || maxBytes <= 0 {
		return ""
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	lines := strings.Split(value, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	value = strings.Join(lines, "\n")
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func lineCount(value string) int {
	if value == "" {
		return 0
	}
	return strings.Count(value, "\n") + 1
}

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)-----BEGIN(?: [A-Z0-9]+)? PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`\b(?:sk-ant-|sk-|ghp_|github_pat_|AIza)[A-Za-z0-9_-]{12,}\b`),
	regexp.MustCompile(`\bAKIA[A-Z0-9]{16}\b`),
	regexp.MustCompile(`(?i)\b(?:password|passwd|secret|token|api[_-]?key)\s*[:=]\s*["']?[^\s"']{8,}`),
	regexp.MustCompile(`(?i)\b(?:secret|token|api[_-]?key)\b[^\n]{0,20}\b[A-Za-z0-9+/=_-]{24,}\b`),
}

func containsSensitiveContent(value string) bool {
	for _, pattern := range sensitivePatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

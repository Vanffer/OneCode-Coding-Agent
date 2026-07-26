package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"onecode/internal/llm"
)

const (
	defaultMemoryQueueSize = 16
	defaultMemoryTimeout   = 60 * time.Second
	memoryExtractorSystem  = `You maintain durable coding-agent memory.
Extract only information that remains useful beyond the current turn.
Return valid JSON only. Never preserve credentials or secrets.`
)

// TurnCandidate contains one complete naturally-finished Agent turn.
type TurnCandidate struct {
	SessionID string
	Messages  []llm.Message
	StoppedAt time.Time
}

type memoryJob struct {
	provider  llm.Provider
	candidate TurnCandidate
}

// Worker serializes asynchronous memory extraction and note updates.
type Worker struct {
	store   *NoteStore
	queue   chan memoryJob
	errors  chan error
	ctx     context.Context
	cancel  context.CancelFunc
	timeout time.Duration

	mu     sync.Mutex
	seen   map[string]struct{}
	closed bool
	wg     sync.WaitGroup
}

// NewWorker starts one background memory consumer.
func NewWorker(store *NoteStore) *Worker {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Worker{
		store:   store,
		queue:   make(chan memoryJob, defaultMemoryQueueSize),
		errors:  make(chan error, defaultMemoryQueueSize),
		ctx:     ctx,
		cancel:  cancel,
		timeout: defaultMemoryTimeout,
		seen:    make(map[string]struct{}),
	}
	w.wg.Add(1)
	go w.run()
	return w
}

// Enqueue submits a useful candidate without blocking the caller.
func (w *Worker) Enqueue(provider llm.Provider, candidate TurnCandidate) bool {
	if w == nil || w.store == nil || !w.store.Enabled || provider == nil || !usefulCandidate(candidate) {
		return false
	}
	hash, err := candidateHash(candidate)
	if err != nil {
		w.report(fmt.Errorf("计算记忆候选哈希失败: %w", err))
		return false
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return false
	}
	if _, duplicate := w.seen[hash]; duplicate {
		w.mu.Unlock()
		return false
	}
	select {
	case w.queue <- memoryJob{provider: provider, candidate: cloneCandidate(candidate)}:
		w.seen[hash] = struct{}{}
		w.mu.Unlock()
		return true
	default:
		w.mu.Unlock()
		w.report(fmt.Errorf("自动记忆队列已满，已跳过本轮候选"))
		return false
	}
}

// Errors exposes best-effort background failures without blocking the worker.
func (w *Worker) Errors() <-chan error {
	if w == nil {
		return nil
	}
	return w.errors
}

// Close cancels in-flight extraction and waits for the consumer to exit.
func (w *Worker) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.cancel()
	w.mu.Unlock()
	w.wg.Wait()
	w.mu.Lock()
	close(w.errors)
	w.mu.Unlock()
	return nil
}

func (w *Worker) run() {
	defer w.wg.Done()
	for {
		select {
		case <-w.ctx.Done():
			return
		case job := <-w.queue:
			if err := w.process(job); err != nil && !errors.Is(err, context.Canceled) {
				w.report(err)
			}
		}
	}
}

func (w *Worker) process(job memoryJob) error {
	if w.store == nil || !w.store.Enabled {
		return nil
	}
	indexes, err := w.store.LoadIndexes()
	if err != nil {
		return fmt.Errorf("读取自动记忆索引失败: %w", err)
	}
	timeout := w.timeout
	if timeout <= 0 {
		timeout = defaultMemoryTimeout
	}
	ctx, cancel := context.WithTimeout(w.ctx, timeout)
	defer cancel()

	text, err := extractMemory(ctx, job.provider, buildMemoryPrompt(job.candidate, indexes))
	if err != nil {
		return err
	}
	mutations, err := parseMemoryMutations(text)
	if err != nil {
		return err
	}
	mutations, err = validateMemoryMutations(mutations, job.candidate)
	if err != nil {
		return err
	}
	if len(mutations) == 0 {
		return nil
	}
	if err := w.store.Apply(mutations); err != nil {
		return fmt.Errorf("应用自动记忆失败: %w", err)
	}
	return nil
}

func (w *Worker) report(err error) {
	if w == nil || err == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	select {
	case w.errors <- err:
	default:
	}
}

func extractMemory(ctx context.Context, provider llm.Provider, extractionPrompt string) (string, error) {
	events, errs := provider.Stream(ctx, []llm.Message{{Role: "user", Content: extractionPrompt}}, nil, llm.StreamOptions{})
	var text strings.Builder
	for events != nil || errs != nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if event.ToolCall != nil {
				return "", fmt.Errorf("自动记忆提取不允许工具调用: %s", event.ToolCall.Name)
			}
			text.WriteString(event.Text)
			if event.Done {
				if strings.TrimSpace(text.String()) == "" {
					return "", fmt.Errorf("自动记忆提取返回空响应")
				}
				return text.String(), nil
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return "", fmt.Errorf("自动记忆提取流失败: %w", err)
			}
		}
	}
	return "", fmt.Errorf("自动记忆提取流未正常结束")
}

func buildMemoryPrompt(candidate TurnCandidate, indexes string) string {
	var builder strings.Builder
	builder.WriteString(memoryExtractorSystem)
	builder.WriteString(`

Analyze the completed coding-agent turn and decide whether it contains durable memory.

Rules:
- Prefer project scope for repository facts, commands, conventions, corrections, and references.
- Use user scope only for an explicit preference that should apply across projects.
- A one-time request is not a user-level preference.
- Use update when an existing indexed note should be corrected or extended.
- Never include passwords, API keys, access tokens, private keys, or other credentials.
- Return exactly one JSON object: {"mutations":[...]}
- Each mutation uses operation skip, create, or update.
- create/update note fields are scope, category, title, summary, and body.
- update also requires target_id from the supplied index.

Existing memory indexes:
`)
	if strings.TrimSpace(indexes) == "" {
		builder.WriteString("(none)")
	} else {
		builder.WriteString(indexes)
	}
	builder.WriteString("\n\nCompleted turn:\n")
	builder.WriteString(candidateTranscript(candidate.Messages))
	return builder.String()
}

func candidateTranscript(messages []llm.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString("[" + message.Role + "]\n")
		if message.Content != "" {
			builder.WriteString(boundText(message.Content, 80, 8*1024))
			builder.WriteByte('\n')
		}
		for _, call := range message.ToolCalls {
			builder.WriteString("tool_call: " + call.Name + "\n")
		}
		if message.ToolResult != nil {
			builder.WriteString("tool_result: ")
			builder.WriteString(boundText(message.ToolResult.Content, 30, 3*1024))
			builder.WriteByte('\n')
		}
	}
	return boundText(builder.String(), 400, 48*1024)
}

type mutationEnvelope struct {
	Mutations []NoteMutation `json:"mutations"`
}

func parseMemoryMutations(value string) ([]NoteMutation, error) {
	value = stripJSONFence(value)
	var envelope mutationEnvelope
	if err := strictJSON([]byte(value), &envelope); err == nil && envelope.Mutations != nil {
		return envelope.Mutations, nil
	}
	var mutations []NoteMutation
	if err := strictJSON([]byte(value), &mutations); err != nil {
		return nil, fmt.Errorf("解析自动记忆 JSON 失败: %w", err)
	}
	return mutations, nil
}

func strictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("JSON 包含多余内容")
		}
		return err
	}
	return nil
}

func stripJSONFence(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") {
		return value
	}
	firstNewline := strings.IndexByte(value, '\n')
	lastFence := strings.LastIndex(value, "```")
	if firstNewline < 0 || lastFence <= firstNewline {
		return value
	}
	return strings.TrimSpace(value[firstNewline+1 : lastFence])
}

func validateMemoryMutations(mutations []NoteMutation, candidate TurnCandidate) ([]NoteMutation, error) {
	validated := make([]NoteMutation, 0, len(mutations))
	for _, mutation := range mutations {
		switch mutation.Operation {
		case MutationSkip:
			continue
		case MutationCreate, MutationUpdate:
		default:
			return nil, fmt.Errorf("自动记忆返回未知操作: %s", mutation.Operation)
		}
		if mutation.Note.Scope == "" {
			mutation.Note.Scope = ScopeProject
		}
		if err := validateScope(mutation.Note.Scope); err != nil {
			return nil, err
		}
		if !validCategory(mutation.Note.Category) {
			return nil, fmt.Errorf("自动记忆返回未知分类: %s", mutation.Note.Category)
		}
		if mutation.Operation == MutationUpdate && !sessionIDPattern.MatchString(mutation.TargetID) {
			return nil, fmt.Errorf("自动记忆 update 缺少合法 target_id")
		}
		if mutation.Operation == MutationCreate && mutation.Note.Scope == ScopeUser {
			if mutation.Note.Category != CategoryPreference || !explicitGlobalPreference(candidate.Messages) {
				mutation.Note.Scope = ScopeProject
			}
		}
		if strings.TrimSpace(mutation.Note.Title) == "" || strings.TrimSpace(mutation.Note.Summary) == "" || strings.TrimSpace(mutation.Note.Body) == "" {
			return nil, fmt.Errorf("自动记忆标题、摘要和正文不能为空")
		}
		if containsSensitiveContent(strings.Join([]string{mutation.Note.Title, mutation.Note.Summary, mutation.Note.Body}, "\n")) {
			continue
		}
		mutation.Note.ID = ""
		mutation.Note.CreatedAt = time.Time{}
		mutation.Note.UpdatedAt = time.Time{}
		mutation.Note.SourceSessionID = candidate.SessionID
		validated = append(validated, mutation)
	}
	return validated, nil
}

func explicitGlobalPreference(messages []llm.Message) bool {
	var userText strings.Builder
	for _, message := range messages {
		if message.Role == "user" {
			userText.WriteString(" ")
			userText.WriteString(strings.ToLower(message.Content))
		}
	}
	value := userText.String()
	for _, marker := range []string{
		"所有项目", "每个项目", "跨项目", "全局偏好", "以后都", "总是",
		"all projects", "every project", "across projects", "global preference", "always",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func usefulCandidate(candidate TurnCandidate) bool {
	var userText, assistantText strings.Builder
	for _, message := range candidate.Messages {
		switch message.Role {
		case "user":
			userText.WriteString(" ")
			userText.WriteString(message.Content)
		case "assistant":
			assistantText.WriteString(" ")
			assistantText.WriteString(message.Content)
		}
	}
	user := strings.TrimSpace(userText.String())
	assistant := strings.TrimSpace(assistantText.String())
	if user == "" || assistant == "" {
		return false
	}
	normalizedUser := strings.ToLower(strings.Join(strings.Fields(user), " "))
	for _, short := range []string{"hi", "hello", "ok", "okay", "thanks", "thank you", "你好", "好的", "谢谢", "确认", "1"} {
		if normalizedUser == short {
			return false
		}
	}
	return utf8.RuneCountInString(user)+utf8.RuneCountInString(assistant) >= 40
}

func candidateHash(candidate TurnCandidate) (string, error) {
	data, err := json.Marshal(candidate.Messages)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func cloneCandidate(candidate TurnCandidate) TurnCandidate {
	candidate.Messages = append([]llm.Message(nil), candidate.Messages...)
	return candidate
}

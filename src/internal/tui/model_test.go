package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onecode/internal/agent"
	"onecode/internal/conversation"
	"onecode/internal/llm"
	"onecode/internal/memory"
	"onecode/internal/permission"
	"onecode/internal/prompt"
	"onecode/internal/tools"

	"charm.land/lipgloss/v2"
)

type tuiTestProvider struct{}

func (tuiTestProvider) Name() string  { return "Test" }
func (tuiTestProvider) Model() string { return "test-model" }
func (tuiTestProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, llm.StreamOptions) (<-chan llm.StreamEvent, <-chan error) {
	events := make(chan llm.StreamEvent)
	errs := make(chan error)
	close(events)
	close(errs)
	return events, errs
}

func TestStatusBarIncludesContext(t *testing.T) {
	got := statusBar(tuiTestProvider{}, "Ready", "Context ~1k / 200k · 1%", 120)
	for _, want := range []string{"Test", "Ready", "Context ~1k / 200k", "test-model"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected status bar to contain %q, got %q", want, got)
		}
	}
}

func TestStatusBarKeepsStableTerminalWidth(t *testing.T) {
	const width = 120
	statuses := []string{
		"⣾ 正在请求模型 94.9s",
		"⣽ 正在执行工具 95.0s",
		"⣻ tokens 12345 cache read 100/create 20 95.1s",
	}
	for _, status := range statuses {
		got := statusBar(tuiTestProvider{}, status, "Context ~5k / 256k · 2%", width)
		if displayWidth := lipgloss.Width(got); displayWidth != width {
			t.Fatalf("status %q rendered at width %d, want %d", status, displayWidth, width)
		}
	}
}

func TestPermissionBatchFeedbackTransitionsDirectlyToNextRequest(t *testing.T) {
	model := New(nil, nil, t.TempDir(), MemoryDependencies{})
	model.provider = tuiTestProvider{}
	model.agent = agent.New(tuiTestProvider{}, tools.NewRegistry())
	model.state = statePermissionConfirm
	model.pendingPerm = &agent.PermissionEvent{Request: permission.ConfirmationRequest{
		ID: "call_1", Tool: "edit_file", BatchIndex: 1, BatchTotal: 2,
	}}
	model.agentEvents = make(chan agent.Event)

	if view := model.viewPermissionConfirm(); !strings.Contains(view, "tool call 1 of 2") {
		t.Fatalf("expected first batch position, got:\n%s", view)
	}
	next, _ := model.answerPermission(permission.ChoiceAllowOnce)
	if next.state != statePermissionConfirm || next.pendingPerm != nil {
		t.Fatalf("expected acknowledgement state without pending request, got state=%v pending=%+v", next.state, next.pendingPerm)
	}
	if view := next.viewPermissionConfirm(); !strings.Contains(view, "Permission recorded") || !strings.Contains(view, "Running edit_file") {
		t.Fatalf("expected visible permission acknowledgement, got:\n%s", view)
	}

	updatedModel, _ := next.Update(agentEventMsg{Type: agent.EventPermissionRequest, Permission: &agent.PermissionEvent{
		Request: permission.ConfirmationRequest{ID: "call_2", Tool: "bash", BatchIndex: 2, BatchTotal: 2},
	}})
	updated := updatedModel.(Model)
	if updated.state != statePermissionConfirm || updated.permissionFeedback != "" {
		t.Fatalf("expected direct transition to next permission, state=%v feedback=%q", updated.state, updated.permissionFeedback)
	}
	if view := updated.viewPermissionConfirm(); !strings.Contains(view, "tool call 2 of 2") {
		t.Fatalf("expected second batch position, got:\n%s", view)
	}

	progressedModel, _ := next.Update(agentEventMsg{Type: agent.EventProgress, Progress: &agent.ProgressEvent{Message: "继续下一轮"}})
	progressed := progressedModel.(Model)
	if progressed.state != stateStreaming || progressed.permissionFeedback != "" {
		t.Fatalf("expected progress to end permission acknowledgement, state=%v feedback=%q", progressed.state, progressed.permissionFeedback)
	}
}

func TestContextDisplayEstimated(t *testing.T) {
	model := Model{
		conv: conversation.New(),
		contextUsage: conversation.UsageEstimate{
			Used:      42000,
			Limit:     200000,
			Percent:   21,
			Estimated: true,
		},
		contextWindow: conversation.WindowInfo{
			Limit:  200000,
			Source: conversation.WindowSourceInferred,
		},
	}

	got := model.contextDisplay()
	if !strings.Contains(got, "Context ~42k / 200k · 21%") {
		t.Fatalf("unexpected context display: %q", got)
	}
}

func TestContextWindowCommandSavesLocalConfig(t *testing.T) {
	root := t.TempDir()
	model := Model{
		conv:  conversation.New(conversation.WithContextOptions(conversation.ContextOptions{ProjectRoot: root})),
		state: stateIdle,
	}

	next, _, handled := model.handleSlashCommand("/context window 200000")
	if !handled {
		t.Fatal("expected context window command to be handled")
	}
	if next.state != stateIdle {
		t.Fatalf("expected idle state after setting window, got %v", next.state)
	}
	cfg, ok, err := conversation.NewProjectStore(root).LoadLocalConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadLocalConfig returned error: %v", err)
	}
	if !ok || cfg.ContextWindow != 200000 {
		t.Fatalf("expected saved context window, ok=%v cfg=%+v", ok, cfg)
	}
}

func TestContextWindowCommandEntersInputState(t *testing.T) {
	model := New(nil, nil, t.TempDir(), MemoryDependencies{})
	model.state = stateIdle

	next, _, handled := model.handleSlashCommand("/context window")
	if !handled {
		t.Fatal("expected context window command to be handled")
	}
	if next.state != stateContextWindowInput {
		t.Fatalf("expected context window input state, got %v", next.state)
	}
	if next.textarea.Placeholder == mainInputPlaceholder {
		t.Fatal("expected context-specific placeholder")
	}
}

func TestNewInitializesMemoryDependencies(t *testing.T) {
	root := t.TempDir()
	loader := &memory.InstructionLoader{ProjectRoot: root}
	sessions := memory.NewSessionStore(root)
	notes := memory.NewNoteStore(root, "", false)
	worker := memory.NewWorker(notes)
	defer worker.Close()
	instructions := memory.InstructionSet{Content: "project rules"}

	model := New(nil, nil, root, MemoryDependencies{
		InstructionLoader: loader,
		Instructions:      instructions,
		Sessions:          sessions,
		Notes:             notes,
		Worker:            worker,
	})

	if model.instructionLoader != loader || model.sessionStore != sessions || model.noteStore != notes || model.memoryWorker != worker {
		t.Fatal("expected concrete memory dependencies to be retained")
	}
	if model.instructions.Content != instructions.Content {
		t.Fatalf("unexpected instructions: %q", model.instructions.Content)
	}
}

func TestSessionPersistenceAppendsAndSnapshots(t *testing.T) {
	store := memory.NewSessionStore(t.TempDir())
	journal, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	user := llm.Message{Role: "user", Content: "remember this request"}
	if err := journal.AppendMessage(user); err != nil {
		t.Fatal(err)
	}
	model := Model{
		journal:      journal,
		turnMessages: []llm.Message{user},
	}
	assistant := llm.Message{Role: "assistant", Content: "complete response"}
	if err := model.persistConversationEvent(&agent.ConversationEvent{
		Kind:    agent.ConversationAppend,
		Message: &assistant,
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.persistConversationEvent(&agent.ConversationEvent{
		Kind:     agent.ConversationSnapshot,
		Messages: []llm.Message{user, assistant},
	}); err != nil {
		t.Fatal(err)
	}
	if len(model.turnMessages) != 2 {
		t.Fatalf("expected candidate to keep append messages, got %d", len(model.turnMessages))
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	restored, err := store.Load(journal.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Messages) != 2 || restored.Messages[1].Content != assistant.Content {
		t.Fatalf("unexpected restored history: %+v", restored.Messages)
	}
}

func TestSessionPersistenceFailureDisablesJournal(t *testing.T) {
	store := memory.NewSessionStore(t.TempDir())
	journal, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	model := Model{journal: journal}
	message := llm.Message{Role: "assistant", Content: "response"}
	if err := model.persistConversationEvent(&agent.ConversationEvent{Kind: agent.ConversationAppend, Message: &message}); err == nil {
		t.Fatal("expected closed journal write to fail")
	}
	if model.journal != nil {
		t.Fatal("expected failed journal to be disabled")
	}
}

func TestRestoreSessionReplacesStateAfterJournalOpen(t *testing.T) {
	root := t.TempDir()
	store := memory.NewSessionStore(root)
	currentJournal, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer currentJournal.Close()
	targetJournal, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer targetJournal.Close()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.Local)
	model := New(nil, nil, root, MemoryDependencies{})
	model.state = stateSessionLoading
	model.journal = currentJournal
	model.sessionID = currentJournal.ID()
	model.instructions = memory.InstructionSet{Content: "old"}
	model.now = func() time.Time { return now }
	model.conv.AddUser("current session")
	restoredMessages := []llm.Message{
		{Role: "user", Content: "restored request"},
		{Role: "assistant", Content: "restored response"},
	}

	nextModel, _ := model.applyRestoredSession(sessionRestoreMsg{
		journal: targetJournal,
		result: memory.RestoreResult{
			Info:         memory.SessionInfo{ID: targetJournal.ID()},
			Messages:     restoredMessages,
			LastActiveAt: now.Add(-48 * time.Hour),
		},
	})
	next := nextModel.(Model)
	defer next.Close()
	if next.sessionID != targetJournal.ID() || next.journal != targetJournal {
		t.Fatal("expected restored journal to become active")
	}
	if got := next.conv.Messages(); len(got) != 2 || got[0].Content != "restored request" {
		t.Fatalf("unexpected restored conversation: %+v", got)
	}
	if next.pendingResumeGap == "" {
		t.Fatal("expected a one-shot resume gap reminder")
	}
	if err := currentJournal.AppendMessage(llm.Message{Role: "user", Content: "must fail"}); err == nil {
		t.Fatal("expected previous journal to be closed only after successful restore")
	}
}

func TestRestoreSessionFailureKeepsCurrentState(t *testing.T) {
	model := New(nil, nil, t.TempDir(), MemoryDependencies{})
	model.state = stateSessionLoading
	model.conv.AddUser("current session")
	nextModel, _ := model.applyRestoredSession(sessionRestoreMsg{})
	next := nextModel.(Model)
	if got := next.conv.Messages(); len(got) != 1 || got[0].Content != "current session" {
		t.Fatalf("failed restore changed current conversation: %+v", got)
	}
}

type promptCapture struct {
	messages []llm.Message
	tools    []llm.ToolDefinition
	opts     llm.StreamOptions
}

type promptCaptureProvider struct {
	calls chan promptCapture
}

func (p *promptCaptureProvider) Name() string  { return "capture" }
func (p *promptCaptureProvider) Model() string { return "capture-model" }
func (p *promptCaptureProvider) Stream(_ context.Context, messages []llm.Message, definitions []llm.ToolDefinition, opts llm.StreamOptions) (<-chan llm.StreamEvent, <-chan error) {
	p.calls <- promptCapture{messages: messages, tools: definitions, opts: opts}
	events := make(chan llm.StreamEvent, 2)
	errs := make(chan error)
	events <- llm.StreamEvent{Text: "completed"}
	events <- llm.StreamEvent{Done: true, FinishReason: llm.FinishStop}
	close(events)
	close(errs)
	return events, errs
}

func TestPromptContextIncludesInstructionsMemoryAndResumeGap(t *testing.T) {
	root := t.TempDir()
	notes := memory.NewNoteStore(root, "", true)
	if err := os.MkdirAll(notes.ProjectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes.ProjectRoot, "INDEX.md"), []byte("project memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &promptCaptureProvider{calls: make(chan promptCapture, 1)}
	model := Model{
		provider:         provider,
		agent:            agent.New(provider, tools.NewRegistry()),
		conv:             conversation.New(),
		noteStore:        notes,
		instructions:     memory.InstructionSet{Content: "project instructions"},
		pendingResumeGap: "re-check old assumptions",
		curReply:         &strings.Builder{},
	}
	next, _ := model.startAgentRun("perform the task", agent.ModeExecute, agent.RunOptions{Mode: agent.ModeExecute})
	capture := <-provider.calls
	if next.pendingResumeGap != "" {
		t.Fatal("expected resume gap to be consumed after a real run starts")
	}
	kinds := make(map[prompt.ReminderKind]string)
	for _, reminder := range capture.opts.Prompt.Reminders {
		kinds[reminder.Kind] = reminder.Content
	}
	for kind, content := range map[prompt.ReminderKind]string{
		prompt.ReminderInstructions: "project instructions",
		prompt.ReminderMemoryIndex:  "project memory",
		prompt.ReminderResumeGap:    "re-check old assumptions",
	} {
		if !strings.Contains(kinds[kind], content) {
			t.Fatalf("expected %s reminder to contain %q, got %q", kind, content, kinds[kind])
		}
	}
}

type memoryCaptureProvider struct {
	calls chan struct{}
}

func (p *memoryCaptureProvider) Name() string  { return "memory-capture" }
func (p *memoryCaptureProvider) Model() string { return "memory-model" }
func (p *memoryCaptureProvider) Stream(_ context.Context, _ []llm.Message, definitions []llm.ToolDefinition, _ llm.StreamOptions) (<-chan llm.StreamEvent, <-chan error) {
	if definitions != nil {
		panic("memory extraction must not receive tools")
	}
	p.calls <- struct{}{}
	events := make(chan llm.StreamEvent, 1)
	errs := make(chan error)
	events <- llm.StreamEvent{Text: `{"mutations":[]}`, Done: true, FinishReason: llm.FinishStop}
	close(events)
	close(errs)
	return events, errs
}

func TestAutoMemoryRunsOnlyAfterNaturalStop(t *testing.T) {
	provider := &memoryCaptureProvider{calls: make(chan struct{}, 2)}
	store := memory.NewNoteStore(t.TempDir(), "", true)
	worker := memory.NewWorker(store)
	defer worker.Close()
	candidate := []llm.Message{
		{Role: "user", Content: "For every future change in this project, always run the focused Go tests first."},
		{Role: "assistant", Content: "I will preserve that durable project workflow preference."},
	}

	model := Model{
		state:        stateStreaming,
		provider:     provider,
		memoryWorker: worker,
		turnMessages: candidate,
		curReply:     &strings.Builder{},
		currentMode:  agent.ModeExecute,
	}
	updatedModel, _ := model.Update(agentEventMsg{
		Type: agent.EventDone,
		Done: &agent.DoneEvent{Reason: agent.StopModelDone},
	})
	updated := updatedModel.(Model)
	if updated.turnMessages != nil {
		t.Fatal("expected completed candidate buffer to be cleared")
	}
	select {
	case <-provider.calls:
	case <-time.After(time.Second):
		t.Fatal("expected natural stop to enqueue asynchronous memory extraction")
	}

	model.turnMessages = candidate
	model.Update(agentEventMsg{
		Type: agent.EventDone,
		Done: &agent.DoneEvent{Reason: agent.StopCancelled},
	})
	select {
	case <-provider.calls:
		t.Fatal("cancelled stop must not enqueue memory extraction")
	case <-time.After(50 * time.Millisecond):
	}
}

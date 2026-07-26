package agent

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"onecode/internal/conversation"
	"onecode/internal/llm"
	"onecode/internal/permission"
	"onecode/internal/prompt"
	"onecode/internal/tools"
)

type streamScript struct {
	events        []llm.StreamEvent
	err           error
	waitForCancel bool
}

type providerCall struct {
	messages []llm.Message
	tools    []llm.ToolDefinition
	prompt   prompt.Payload
}

type scriptedProvider struct {
	mu      sync.Mutex
	scripts []streamScript
	calls   []providerCall
	callCh  chan struct{}
}

func (p *scriptedProvider) Name() string  { return "mock" }
func (p *scriptedProvider) Model() string { return "mock-model" }

func (p *scriptedProvider) Stream(ctx context.Context, msgs []llm.Message, toolDefs []llm.ToolDefinition, opts llm.StreamOptions) (<-chan llm.StreamEvent, <-chan error) {
	p.mu.Lock()
	index := len(p.calls)
	copiedMsgs := make([]llm.Message, len(msgs))
	copy(copiedMsgs, msgs)
	copiedTools := make([]llm.ToolDefinition, len(toolDefs))
	copy(copiedTools, toolDefs)
	p.calls = append(p.calls, providerCall{messages: copiedMsgs, tools: copiedTools, prompt: opts.Prompt})
	if p.callCh != nil {
		select {
		case p.callCh <- struct{}{}:
		default:
		}
	}
	var script streamScript
	if index < len(p.scripts) {
		script = p.scripts[index]
	}
	p.mu.Unlock()

	events := make(chan llm.StreamEvent, len(script.events)+1)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if script.waitForCancel {
			<-ctx.Done()
			return
		}
		for _, event := range script.events {
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
		if script.err != nil {
			errs <- script.err
		}
	}()
	return events, errs
}

func (p *scriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *scriptedProvider) callAt(i int) providerCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[i]
}

func TestLoopRunsMultipleToolRounds(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "read", Name: "read_file", Input: map[string]interface{}{"path": "a.go"}}},
			{Done: true, FinishReason: llm.FinishToolCalls},
		}},
		{events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "edit", Name: "edit_file", Input: map[string]interface{}{"path": "a.go"}}},
			{Done: true, FinishReason: llm.FinishToolCalls},
		}},
		{events: []llm.StreamEvent{
			{Text: "done"},
			{Done: true, FinishReason: llm.FinishStop},
		}},
	}}
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&fakeTool{name: "read_file", result: "file"}, tools.SafetyReadOnly)
	registry.RegisterWithSafety(&fakeTool{name: "edit_file", result: "edited"}, tools.SafetySideEffect)
	agent := New(provider, registry)
	conv := conversation.New()
	conv.AddUser("do it")

	events := drainEvents(agent.Run(context.Background(), conv, RunOptions{Mode: ModeExecute}))
	done := lastDone(events)
	if done == nil || done.Reason != StopModelDone {
		t.Fatalf("expected StopModelDone, got %+v", done)
	}
	if provider.callCount() != 3 {
		t.Fatalf("expected 3 provider calls, got %d", provider.callCount())
	}
	if conv.MessageCount() != 6 {
		t.Fatalf("expected 6 conversation messages, got %d", conv.MessageCount())
	}
	secondCall := provider.callAt(1)
	if len(secondCall.messages) < 3 || secondCall.messages[2].Role != "tool" {
		t.Fatalf("expected second provider call to include first tool result, got %+v", secondCall.messages)
	}
}

func TestLoopEmitsCommittedConversationAppendEvents(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "read", Name: "read_file", Input: map[string]interface{}{"path": "a.go"}}},
			{Done: true, FinishReason: llm.FinishToolCalls},
		}},
		{events: []llm.StreamEvent{{Text: "done"}, {Done: true, FinishReason: llm.FinishStop}}},
	}}
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&fakeTool{name: "read_file", result: "file"}, tools.SafetyReadOnly)
	conv := conversation.New()
	conv.AddUser("read")

	events := drainEvents(New(provider, registry).Run(context.Background(), conv, RunOptions{Mode: ModeExecute}))
	var changes []*ConversationEvent
	for i := range events {
		if events[i].Type == EventConversation && events[i].Conversation != nil {
			changes = append(changes, events[i].Conversation)
		}
	}
	if len(changes) != 3 {
		t.Fatalf("expected assistant tool call, tool result, and final assistant events; got %+v", changes)
	}
	if changes[0].Kind != ConversationAppend || changes[0].Message == nil || len(changes[0].Message.ToolCalls) != 1 {
		t.Fatalf("unexpected first conversation event: %+v", changes[0])
	}
	if changes[1].Message == nil || changes[1].Message.Role != "tool" || changes[1].Message.ToolResult.ToolUseID != "read" {
		t.Fatalf("unexpected tool result event: %+v", changes[1])
	}
	if changes[2].Message == nil || changes[2].Message.Content != "done" {
		t.Fatalf("unexpected final assistant event: %+v", changes[2])
	}
}

func TestManualCompactEmitsConversationSnapshotBeforeDone(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{{events: []llm.StreamEvent{
		{Text: "<formal_summary>## Task Goal\nContinue the implementation.</formal_summary>"},
		{Done: true, FinishReason: llm.FinishStop},
	}}}}
	agent := New(provider, tools.NewRegistry())
	conv := conversation.New()
	conv.AddUser("implement the feature")
	conv.AddAssistant("working on it")

	events := drainEvents(agent.Compact(context.Background(), conv, conversation.CompactModeManual))
	snapshotIndex := -1
	doneIndex := -1
	for i, event := range events {
		if event.Type == EventConversation && event.Conversation != nil && event.Conversation.Kind == ConversationSnapshot {
			snapshotIndex = i
			if len(event.Conversation.Messages) != len(conv.Messages()) {
				t.Fatalf("snapshot does not contain effective compacted history: %+v", event.Conversation.Messages)
			}
		}
		if event.Type == EventDone {
			doneIndex = i
		}
	}
	if snapshotIndex < 0 || doneIndex < 0 || snapshotIndex >= doneIndex {
		t.Fatalf("expected snapshot before done, snapshot=%d done=%d events=%+v", snapshotIndex, doneIndex, events)
	}
}

func TestLoopStopsAtMaxIterations(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "read", Name: "read_file", Input: map[string]interface{}{}}},
			{Done: true, FinishReason: llm.FinishToolCalls},
		}},
	}}
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&fakeTool{name: "read_file"}, tools.SafetyReadOnly)
	agent := New(provider, registry)

	events := drainEvents(agent.Run(context.Background(), conversation.New(), RunOptions{Mode: ModeExecute, MaxIterations: 1}))
	done := lastDone(events)
	if done == nil || done.Reason != StopMaxIterations {
		t.Fatalf("expected StopMaxIterations, got %+v", done)
	}
}

func TestLoopStopsAfterBadToolLimit(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "bad_1", Name: "missing", Input: map[string]interface{}{}}},
			{Done: true, FinishReason: llm.FinishToolCalls},
		}},
		{events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "bad_2", Name: "missing", Input: map[string]interface{}{}}},
			{Done: true, FinishReason: llm.FinishToolCalls},
		}},
	}}
	agent := New(provider, tools.NewRegistry())

	events := drainEvents(agent.Run(context.Background(), conversation.New(), RunOptions{
		Mode:                   ModeExecute,
		MaxIterations:          5,
		MaxConsecutiveBadTools: 2,
	}))
	done := lastDone(events)
	if done == nil || done.Reason != StopBadToolLimit {
		t.Fatalf("expected StopBadToolLimit, got %+v", done)
	}
}

func TestLoopCancelStopsNextProviderCall(t *testing.T) {
	provider := &scriptedProvider{
		scripts: []streamScript{{waitForCancel: true}},
		callCh:  make(chan struct{}, 1),
	}
	agent := New(provider, tools.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())

	eventCh := agent.Run(ctx, conversation.New(), RunOptions{Mode: ModeExecute})
	<-provider.callCh
	cancel()
	events := drainEvents(eventCh)

	done := lastDone(events)
	if done == nil || done.Reason != StopCancelled {
		t.Fatalf("expected StopCancelled, got %+v", done)
	}
	if provider.callCount() != 1 {
		t.Fatalf("expected 1 provider call, got %d", provider.callCount())
	}
}

func TestLoopEmitsPermissionRequestAndContinuesAfterAllow(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "bash", Name: "bash", Input: map[string]interface{}{"command": "git status"}}},
			{Done: true, FinishReason: llm.FinishToolCalls},
		}},
		{events: []llm.StreamEvent{
			{Text: "done"},
			{Done: true, FinishReason: llm.FinishStop},
		}},
	}}
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&fakeTool{name: "bash", result: "ok"}, tools.SafetySideEffect)
	manager, err := permission.NewManager(permission.ManagerOptions{
		Mode:        permission.ModeDefault,
		ProjectRoot: ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := New(provider, registry, WithPermissionManager(manager))

	var sawPermission bool
	var sawToolResult bool
	events := agent.Run(context.Background(), conversation.New(), RunOptions{Mode: ModeExecute})
	for event := range events {
		switch event.Type {
		case EventPermissionRequest:
			sawPermission = true
			if event.Permission == nil || event.Permission.Request.Tool != "bash" {
				t.Fatalf("expected bash permission request, got %+v", event.Permission)
			}
			agent.RespondPermission(permission.ConfirmationResponse{
				RequestID: event.Permission.Request.ID,
				Choice:    permission.ChoiceAllowOnce,
			})
		case EventToolResult:
			if event.Tool != nil && event.Tool.Name == "bash" && !event.Tool.IsError {
				sawToolResult = true
			}
		}
	}

	if !sawPermission {
		t.Fatal("expected permission request event")
	}
	if !sawToolResult {
		t.Fatal("expected allowed tool result event")
	}
	if provider.callCount() != 2 {
		t.Fatalf("expected loop to continue after permission, got %d provider calls", provider.callCount())
	}
}

func TestLoopPermissionRequestsIncludeToolBatchPosition(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "bash_1", Name: "bash", Input: map[string]interface{}{"command": "git status"}}},
			{ToolCall: &llm.ToolCall{ID: "bash_2", Name: "bash", Input: map[string]interface{}{"command": "git diff --stat"}}},
			{Done: true, FinishReason: llm.FinishToolCalls},
		}},
		{events: []llm.StreamEvent{{Text: "done"}, {Done: true, FinishReason: llm.FinishStop}}},
	}}
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&fakeTool{name: "bash", result: "ok"}, tools.SafetySideEffect)
	manager, err := permission.NewManager(permission.ManagerOptions{Mode: permission.ModeDefault, ProjectRoot: "."})
	if err != nil {
		t.Fatal(err)
	}
	agent := New(provider, registry, WithPermissionManager(manager))

	var positions [][2]int
	for event := range agent.Run(context.Background(), conversation.New(), RunOptions{Mode: ModeExecute}) {
		if event.Type != EventPermissionRequest || event.Permission == nil {
			continue
		}
		request := event.Permission.Request
		positions = append(positions, [2]int{request.BatchIndex, request.BatchTotal})
		agent.RespondPermission(permission.ConfirmationResponse{RequestID: request.ID, Choice: permission.ChoiceAllowOnce})
	}

	want := [][2]int{{1, 2}, {2, 2}}
	if !reflect.DeepEqual(positions, want) {
		t.Fatalf("unexpected permission positions: got %v want %v", positions, want)
	}
}

func TestAgentPassesPromptPayload(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{Text: "done"},
			{Done: true, FinishReason: llm.FinishStop},
		}},
	}}
	agent := New(provider, tools.NewRegistry())

	drainEvents(agent.Run(context.Background(), conversation.New(), RunOptions{Mode: ModeExecute}))

	call := provider.callAt(0)
	if call.prompt.StableSystem == "" {
		t.Fatal("expected stable system prompt")
	}
	if len(call.prompt.Reminders) != 1 {
		t.Fatalf("expected environment reminder, got %d reminders", len(call.prompt.Reminders))
	}
	if call.prompt.Reminders[0].Kind != prompt.ReminderEnvironment {
		t.Fatalf("expected environment reminder, got %q", call.prompt.Reminders[0].Kind)
	}
}

func TestAgentPassesSessionPromptContext(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{{events: []llm.StreamEvent{
		{Text: "done"},
		{Done: true, FinishReason: llm.FinishStop},
	}}}}
	agent := New(provider, tools.NewRegistry())
	drainEvents(agent.Run(context.Background(), conversation.New(), RunOptions{
		Mode: ModeExecute,
		PromptContext: prompt.SessionPromptContext{
			Instructions: "project instructions",
			MemoryIndex:  "memory index",
			ResumeGap:    "resumed after 48 hours",
		},
	}))
	payload := provider.callAt(0).prompt
	for _, kind := range []prompt.ReminderKind{prompt.ReminderInstructions, prompt.ReminderMemoryIndex, prompt.ReminderResumeGap} {
		if !hasReminderKind(payload, kind) {
			t.Fatalf("expected reminder %s, got %+v", kind, payload.Reminders)
		}
	}
}

func TestPromptRemindersDoNotEnterConversation(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{Text: "plan"},
			{Done: true, FinishReason: llm.FinishStop},
		}},
	}}
	agent := New(provider, tools.NewRegistry())
	conv := conversation.New()
	conv.AddUser("make a plan")

	drainEvents(agent.Run(context.Background(), conv, RunOptions{Mode: ModePlan}))

	for _, msg := range conv.Messages() {
		if msg.Role == "system" || msg.Content == "<system-reminder>" {
			t.Fatalf("conversation should not contain system reminders: %+v", conv.Messages())
		}
		if msg.Content != "" && containsSystemReminder(msg.Content) {
			t.Fatalf("conversation should not contain system reminder content: %+v", msg)
		}
	}
}

func TestPromptPayloadByMode(t *testing.T) {
	executeProvider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{{Done: true, FinishReason: llm.FinishStop}}},
	}}
	planProvider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{{Done: true, FinishReason: llm.FinishStop}}},
	}}

	drainEvents(New(executeProvider, tools.NewRegistry()).Run(context.Background(), conversation.New(), RunOptions{Mode: ModeExecute}))
	drainEvents(New(planProvider, tools.NewRegistry()).Run(context.Background(), conversation.New(), RunOptions{Mode: ModePlan}))

	if hasReminderKind(executeProvider.callAt(0).prompt, prompt.ReminderPlanMode) {
		t.Fatalf("execute mode should not include plan reminder: %+v", executeProvider.callAt(0).prompt.Reminders)
	}
	if !hasReminderKind(planProvider.callAt(0).prompt, prompt.ReminderPlanMode) {
		t.Fatalf("plan mode should include plan reminder: %+v", planProvider.callAt(0).prompt.Reminders)
	}
}

func TestLoopUsageAnchorIncludesAssistantMessage(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{Text: "done"},
			{Usage: &llm.Usage{TotalTokens: 42, Available: true}},
			{Done: true, FinishReason: llm.FinishStop},
		}},
	}}
	conv := conversation.New()
	conv.AddUser("hello")

	events := drainEvents(New(provider, tools.NewRegistry()).Run(context.Background(), conv, RunOptions{Mode: ModeExecute}))

	state := conv.ContextState()
	if state.Usage.Anchor.MessageCount != 2 {
		t.Fatalf("expected anchor to include user and assistant messages, got %+v", state.Usage.Anchor)
	}
	if state.Usage.Used != 42 || state.Usage.Estimated {
		t.Fatalf("expected provider usage to anchor context state, got %+v", state.Usage)
	}
	if !hasContextUsage(events, 42) {
		t.Fatalf("expected context usage event with anchored usage, got %+v", events)
	}
}

func TestLoopUsageAnchorIncludesAssistantToolCallBeforeToolResult(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "read", Name: "read_file", Input: map[string]interface{}{"path": "a.go"}}},
			{Usage: &llm.Usage{TotalTokens: 50, Available: true}},
			{Done: true, FinishReason: llm.FinishToolCalls},
		}},
	}}
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&fakeTool{name: "read_file", result: "file"}, tools.SafetyReadOnly)
	conv := conversation.New()
	conv.AddUser("read")

	drainEvents(New(provider, registry).Run(context.Background(), conv, RunOptions{Mode: ModeExecute, MaxIterations: 1}))

	state := conv.ContextState()
	if state.Usage.Anchor.MessageCount != 2 {
		t.Fatalf("expected anchor to include user and assistant tool call only, got %+v", state.Usage.Anchor)
	}
	if conv.MessageCount() != 3 {
		t.Fatalf("expected tool result to be appended after anchor, got %d messages", conv.MessageCount())
	}
}

func TestLoopPostToolResultsAutoCompactsBeforeNextIteration(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "read", Name: "read_file", Input: map[string]interface{}{"path": "a.go"}}},
			{Usage: &llm.Usage{TotalTokens: 85, Available: true}},
			{Done: true, FinishReason: llm.FinishToolCalls},
		}},
		{events: []llm.StreamEvent{
			{Text: "<formal_summary>## Task Goal\nContinue.</formal_summary>"},
			{Done: true, FinishReason: llm.FinishStop},
		}},
	}}
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&fakeTool{name: "read_file", result: "file"}, tools.SafetyReadOnly)
	conv := conversation.New()
	conv.AddUser("read")
	agent := New(provider, registry, WithContextOptions(conversation.ContextOptions{
		ProjectRoot:              t.TempDir(),
		ProviderWindow:           100,
		SummaryReserveTokens:     10,
		AutoSafetyMarginTokens:   10,
		ForceSafetyMarginTokens:  1,
		ToolResultMaxTokens:      10000,
		ToolResultBatchMaxTokens: 10000,
	}))

	events := drainEvents(agent.Run(context.Background(), conv, RunOptions{Mode: ModeExecute, MaxIterations: 1}))

	if provider.callCount() != 2 {
		t.Fatalf("expected normal request and post-tool compact request, got %d calls", provider.callCount())
	}
	if len(provider.callAt(1).tools) != 0 {
		t.Fatalf("expected compact request without tools, got %+v", provider.callAt(1).tools)
	}
	if !hasContextEvent(events, ContextCompactCompleted) {
		t.Fatalf("expected post-tool compact completed event, got %+v", events)
	}
	if !hasConversationChange(events, ConversationSnapshot) {
		t.Fatalf("expected snapshot after post-tool compaction, got %+v", events)
	}
	if len(conv.Messages()) != 1 || !containsSystemSummaryBoundary(conv.Messages()[0].Content) {
		t.Fatalf("expected conversation to be compacted before next iteration, got %+v", conv.Messages())
	}
}

func TestLoopEmergencyCompactAndRetry(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{err: &llm.ContextTooLongError{Message: "too long"}},
		{events: []llm.StreamEvent{
			{Text: "<formal_summary>## Task Goal\nContinue.</formal_summary>"},
			{Done: true, FinishReason: llm.FinishStop},
		}},
		{events: []llm.StreamEvent{
			{Text: "done"},
			{Done: true, FinishReason: llm.FinishStop},
		}},
	}}
	conv := conversation.New()
	conv.AddUser("do it")
	agent := New(provider, tools.NewRegistry(), WithContextOptions(conversation.ContextOptions{
		ProjectRoot: t.TempDir(),
	}))

	events := drainEvents(agent.Run(context.Background(), conv, RunOptions{Mode: ModeExecute}))

	if provider.callCount() != 3 {
		t.Fatalf("expected original request, compact request, and retry; got %d calls", provider.callCount())
	}
	done := lastDone(events)
	if done == nil || done.Reason != StopModelDone {
		t.Fatalf("expected successful retry, got done=%+v events=%+v", done, events)
	}
	if !hasContextEvent(events, ContextEmergencyRetry) {
		t.Fatalf("expected emergency retry context event, got %+v", events)
	}
	if !hasConversationChange(events, ConversationSnapshot) {
		t.Fatalf("expected snapshot after emergency compaction, got %+v", events)
	}
	if len(conv.Messages()) != 2 || !containsSystemSummaryBoundary(conv.Messages()[0].Content) || conv.Messages()[1].Content != "done" {
		t.Fatalf("expected compacted context plus final assistant, got %+v", conv.Messages())
	}
}

func TestLoopEmergencyCompactRetryOnlyOnce(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{err: &llm.ContextTooLongError{Message: "too long"}},
		{events: []llm.StreamEvent{
			{Text: "<formal_summary>## Task Goal\nContinue.</formal_summary>"},
			{Done: true, FinishReason: llm.FinishStop},
		}},
		{err: &llm.ContextTooLongError{Message: "still too long"}},
	}}
	conv := conversation.New()
	conv.AddUser("do it")
	agent := New(provider, tools.NewRegistry(), WithContextOptions(conversation.ContextOptions{
		ProjectRoot: t.TempDir(),
	}))

	events := drainEvents(agent.Run(context.Background(), conv, RunOptions{Mode: ModeExecute}))

	if provider.callCount() != 3 {
		t.Fatalf("expected exactly one retry after emergency compact, got %d calls", provider.callCount())
	}
	done := lastDone(events)
	if done == nil || done.Reason != StopStreamError {
		t.Fatalf("expected stream error after retry failure, got %+v", done)
	}
	if !hasEventError(events) {
		t.Fatalf("expected error event, got %+v", events)
	}
}

func drainEvents(ch <-chan Event) []Event {
	var events []Event
	for event := range ch {
		events = append(events, event)
	}
	return events
}

func lastDone(events []Event) *DoneEvent {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == EventDone {
			return events[i].Done
		}
	}
	return nil
}

func hasReminderKind(payload prompt.Payload, kind prompt.ReminderKind) bool {
	for _, reminder := range payload.Reminders {
		if reminder.Kind == kind {
			return true
		}
	}
	return false
}

func containsSystemReminder(content string) bool {
	return len(content) >= len("<system-reminder>") && (content == "<system-reminder>" ||
		content[:len("<system-reminder>")] == "<system-reminder>")
}

func hasContextEvent(events []Event, kind ContextEventKind) bool {
	for _, event := range events {
		if event.Type == EventContext && event.Context != nil && event.Context.Kind == kind {
			return true
		}
	}
	return false
}

func hasConversationChange(events []Event, kind ConversationEventKind) bool {
	for _, event := range events {
		if event.Type == EventConversation && event.Conversation != nil && event.Conversation.Kind == kind {
			return true
		}
	}
	return false
}

func hasContextUsage(events []Event, used int) bool {
	for _, event := range events {
		if event.Type == EventContext && event.Context != nil && event.Context.Usage.Used == used {
			return true
		}
	}
	return false
}

func hasEventError(events []Event) bool {
	for _, event := range events {
		if event.Type == EventError && event.Err != nil {
			return true
		}
	}
	return false
}

func containsSystemSummaryBoundary(content string) bool {
	return strings.Contains(content, "<context-summary-boundary>")
}

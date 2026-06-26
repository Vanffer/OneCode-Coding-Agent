package agent

import (
	"context"
	"sync"
	"testing"

	"onecode/internal/conversation"
	"onecode/internal/llm"
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
}

type scriptedProvider struct {
	mu      sync.Mutex
	scripts []streamScript
	calls   []providerCall
	callCh  chan struct{}
}

func (p *scriptedProvider) Name() string  { return "mock" }
func (p *scriptedProvider) Model() string { return "mock-model" }

func (p *scriptedProvider) Stream(ctx context.Context, msgs []llm.Message, toolDefs []llm.ToolDefinition) (<-chan llm.StreamEvent, <-chan error) {
	p.mu.Lock()
	index := len(p.calls)
	copiedMsgs := make([]llm.Message, len(msgs))
	copy(copiedMsgs, msgs)
	copiedTools := make([]llm.ToolDefinition, len(toolDefs))
	copy(copiedTools, toolDefs)
	p.calls = append(p.calls, providerCall{messages: copiedMsgs, tools: copiedTools})
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

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"onecode/internal/llm"
	"onecode/internal/permission"
	"onecode/internal/tools"
)

type fakeTool struct {
	name     string
	result   string
	wait     <-chan struct{}
	recorder *toolRecorder
}

func (t *fakeTool) Name() string { return t.name }

func (t *fakeTool) Description() string { return "fake tool" }

func (t *fakeTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *fakeTool) Timeout() time.Duration { return time.Second }

func (t *fakeTool) Execute(ctx context.Context, args map[string]interface{}) tools.Result {
	if t.recorder != nil {
		t.recorder.record("start:" + t.name)
	}
	if t.wait != nil {
		select {
		case <-t.wait:
		case <-ctx.Done():
			return tools.Result{Content: "cancelled", IsError: true}
		}
	}
	if t.recorder != nil {
		t.recorder.record("end:" + t.name)
	}
	if t.result == "" {
		t.result = "ok"
	}
	return tools.Result{Content: t.result}
}

type toolRecorder struct {
	mu     sync.Mutex
	events []string
	ch     chan string
}

func newToolRecorder() *toolRecorder {
	return &toolRecorder{ch: make(chan string, 20)}
}

func (r *toolRecorder) record(event string) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	r.ch <- event
}

func (r *toolRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]string, len(r.events))
	copy(result, r.events)
	return result
}

func TestSchedulerRunsReadOnlyToolsConcurrently(t *testing.T) {
	recorder := newToolRecorder()
	release := make(chan struct{})
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&fakeTool{name: "read_a", wait: release, recorder: recorder}, tools.SafetyReadOnly)
	registry.RegisterWithSafety(&fakeTool{name: "read_b", wait: release, recorder: recorder}, tools.SafetyReadOnly)
	agent := New(nil, registry)

	done := make(chan struct{})
	go func() {
		agent.executeToolCalls(context.Background(), []llm.ToolCall{
			{ID: "a", Name: "read_a", Input: map[string]interface{}{}},
			{ID: "b", Name: "read_b", Input: map[string]interface{}{}},
		}, ModeExecute, make(chan Event, 20))
		close(done)
	}()

	starts := 0
	timeout := time.After(time.Second)
	for starts < 2 {
		select {
		case event := <-recorder.ch:
			if event == "start:read_a" || event == "start:read_b" {
				starts++
			}
		case <-timeout:
			t.Fatalf("expected both read-only tools to start before release, saw %v", recorder.snapshot())
		}
	}
	close(release)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not finish")
	}
}

func TestSchedulerRunsSideEffectToolsSerially(t *testing.T) {
	recorder := newToolRecorder()
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&fakeTool{name: "write_a", recorder: recorder}, tools.SafetySideEffect)
	registry.RegisterWithSafety(&fakeTool{name: "write_b", recorder: recorder}, tools.SafetySideEffect)
	agent := New(nil, registry)

	agent.executeToolCalls(context.Background(), []llm.ToolCall{
		{ID: "a", Name: "write_a", Input: map[string]interface{}{}},
		{ID: "b", Name: "write_b", Input: map[string]interface{}{}},
	}, ModeExecute, make(chan Event, 20))

	want := []string{"start:write_a", "end:write_a", "start:write_b", "end:write_b"}
	got := recorder.snapshot()
	if len(got) != len(want) {
		t.Fatalf("expected events %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d: expected %q, got %q (all events: %v)", i, want[i], got[i], got)
		}
	}
}

func TestSchedulerRejectsDisabledToolInPlanMode(t *testing.T) {
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&fakeTool{name: "edit_file"}, tools.SafetySideEffect)
	agent := New(nil, registry)

	results, bad := agent.executeToolCalls(context.Background(), []llm.ToolCall{
		{ID: "edit", Name: "edit_file", Input: map[string]interface{}{}},
	}, ModePlan, make(chan Event, 20))

	if bad != 1 {
		t.Fatalf("expected 1 bad tool, got %d", bad)
	}
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("expected disabled tool error, got %+v", results)
	}
}

func TestSchedulerUnknownTool(t *testing.T) {
	agent := New(nil, tools.NewRegistry())

	results, bad := agent.executeToolCalls(context.Background(), []llm.ToolCall{
		{ID: "missing", Name: "missing_tool", Input: map[string]interface{}{}},
	}, ModeExecute, make(chan Event, 20))

	if bad != 1 {
		t.Fatalf("expected 1 bad tool, got %d", bad)
	}
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("expected unknown tool error, got %+v", results)
	}
}

func TestSchedulerPermissionDenialReturnsToolError(t *testing.T) {
	recorder := newToolRecorder()
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&fakeTool{name: "bash", recorder: recorder}, tools.SafetySideEffect)
	manager, err := permission.NewManager(permission.ManagerOptions{
		Mode:        permission.ModeDefault,
		ProjectRoot: ".",
		Confirmer:   &permission.StaticConfirmer{Choice: permission.ChoiceDeny},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := New(nil, registry, WithPermissionManager(manager))

	results, bad := agent.executeToolCalls(context.Background(), []llm.ToolCall{
		{ID: "bash", Name: "bash", Input: map[string]interface{}{"command": "git status"}},
	}, ModeExecute, make(chan Event, 20))

	if bad != 0 {
		t.Fatalf("permission denial should not count as bad tool, got %d", bad)
	}
	if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, "权限拒绝") {
		t.Fatalf("expected permission tool error, got %+v", results)
	}
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("denied tool should not execute, got events %v", got)
	}
}

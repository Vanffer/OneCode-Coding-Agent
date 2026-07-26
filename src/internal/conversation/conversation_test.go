package conversation

import (
	"testing"

	"onecode/internal/llm"
)

func TestConversationAddUser(t *testing.T) {
	conv := New()
	conv.AddUser("Hello")

	msgs := conv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected role 'user', got '%s'", msgs[0].Role)
	}
	if msgs[0].Content != "Hello" {
		t.Errorf("expected content 'Hello', got '%s'", msgs[0].Content)
	}
}

func TestConversationAddAssistant(t *testing.T) {
	conv := New()
	conv.AddAssistant("Hi there!")

	msgs := conv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", msgs[0].Role)
	}
	if msgs[0].Content != "Hi there!" {
		t.Errorf("expected content 'Hi there!', got '%s'", msgs[0].Content)
	}
}

func TestConversationMultipleMessages(t *testing.T) {
	conv := New()
	conv.AddUser("What is Go?")
	conv.AddAssistant("Go is a programming language.")
	conv.AddUser("Tell me more.")
	conv.AddAssistant("Go was created by Google.")

	msgs := conv.Messages()
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}

	expected := []struct {
		role    string
		content string
	}{
		{"user", "What is Go?"},
		{"assistant", "Go is a programming language."},
		{"user", "Tell me more."},
		{"assistant", "Go was created by Google."},
	}

	for i, want := range expected {
		if msgs[i].Role != want.role {
			t.Errorf("message %d: expected role '%s', got '%s'", i, want.role, msgs[i].Role)
		}
		if msgs[i].Content != want.content {
			t.Errorf("message %d: expected content '%s', got '%s'", i, want.content, msgs[i].Content)
		}
	}
}

func TestConversationAddAssistantWithToolCalls(t *testing.T) {
	conv := New()
	toolCalls := []llm.ToolCall{
		{
			ID:   "call_1",
			Name: "read_file",
			Input: map[string]interface{}{
				"path": "main.go",
			},
		},
		{
			ID:   "call_2",
			Name: "grep",
			Input: map[string]interface{}{
				"pattern": "func",
			},
		},
	}

	conv.AddAssistantWithToolCalls("I will inspect the files.", toolCalls)

	msgs := conv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", msgs[0].Role)
	}
	if msgs[0].Content != "I will inspect the files." {
		t.Errorf("expected assistant content to be preserved, got %q", msgs[0].Content)
	}
	if len(msgs[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(msgs[0].ToolCalls))
	}
	if msgs[0].ToolCalls[0].ID != "call_1" || msgs[0].ToolCalls[1].ID != "call_2" {
		t.Fatalf("tool calls were not preserved: %+v", msgs[0].ToolCalls)
	}
}

func TestConversationAddAssistantWithToolCallsWithoutContent(t *testing.T) {
	conv := New()
	conv.AddAssistantWithToolCalls("", []llm.ToolCall{
		{ID: "call_1", Name: "read_file", Input: map[string]interface{}{}},
	})

	msgs := conv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "" {
		t.Errorf("expected empty content, got %q", msgs[0].Content)
	}
	if len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msgs[0].ToolCalls))
	}
}

func TestConversationEmpty(t *testing.T) {
	conv := New()
	msgs := conv.Messages()
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}

func TestConversationContextStateDefault(t *testing.T) {
	conv := New()

	state := conv.ContextState()
	if state.Window.Limit != defaultContextWindow {
		t.Fatalf("expected default window %d, got %d", defaultContextWindow, state.Window.Limit)
	}
	if state.Window.Source != WindowSourceDefault {
		t.Fatalf("expected default window source, got %v", state.Window.Source)
	}
	if state.Store == nil {
		t.Fatal("expected project store to be initialized")
	}
	if state.Files == nil {
		t.Fatal("expected file index to be initialized")
	}
}

func TestConversationContextStateWithOptions(t *testing.T) {
	conv := New(WithContextOptions(ContextOptions{
		ProjectRoot:    "/tmp/project",
		ProviderWindow: 200000,
	}))

	state := conv.ContextState()
	if state.ProjectRoot != "/tmp/project" {
		t.Fatalf("expected project root, got %q", state.ProjectRoot)
	}
	if state.Window.Limit != 200000 {
		t.Fatalf("expected provider window 200000, got %d", state.Window.Limit)
	}
	if state.Window.Source != WindowSourceProvider {
		t.Fatalf("expected provider window source, got %v", state.Window.Source)
	}
	if state.Store == nil || state.Store.ProjectRoot != "/tmp/project" {
		t.Fatalf("expected store project root to be initialized, got %+v", state.Store)
	}
}

func TestConversationClearResetsContextRuntimeState(t *testing.T) {
	conv := New()
	conv.AddUser("hello")
	conv.context.Usage = UsageEstimate{Used: 10, Limit: 100, Percent: 10}
	conv.context.Fuse = CompactFuse{ConsecutiveFailures: 2, Tripped: true}

	conv.Clear()

	if conv.MessageCount() != 0 {
		t.Fatalf("expected messages to be cleared, got %d", conv.MessageCount())
	}
	state := conv.ContextState()
	if state.Usage.Used != 0 || state.Fuse.ConsecutiveFailures != 0 || state.Fuse.Tripped {
		t.Fatalf("expected runtime context state to be reset, got usage=%+v fuse=%+v", state.Usage, state.Fuse)
	}
}

func TestConversationRestoreCopiesMessagesAndResetsRuntimeState(t *testing.T) {
	conv := New(WithContextOptions(ContextOptions{ProjectRoot: "/tmp/project", ProviderWindow: 200000}))
	conv.AddUser("old")
	conv.context.Usage = UsageEstimate{Used: 50, Limit: 100}
	conv.context.Fuse = CompactFuse{ConsecutiveFailures: 2, Tripped: true}
	conv.context.Files.Entries = append(conv.context.Files.Entries, FileIndexEntry{Path: "old.go"})
	messages := []llm.Message{
		{Role: "user", Content: "restored"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call", Input: map[string]interface{}{"nested": map[string]interface{}{"value": "original"}}}}},
		{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "call", Content: "result"}},
	}

	conv.Restore(messages)
	messages[0].Content = "mutated"
	messages[1].ToolCalls[0].Input["nested"].(map[string]interface{})["value"] = "mutated"
	messages[2].ToolResult.Content = "mutated"

	got := conv.Messages()
	if got[0].Content != "restored" || got[1].ToolCalls[0].Input["nested"].(map[string]interface{})["value"] != "original" || got[2].ToolResult.Content != "result" {
		t.Fatalf("Restore did not defensively copy messages: %+v", got)
	}
	state := conv.ContextState()
	if state.ProjectRoot != "/tmp/project" || state.Window.Limit != 200000 {
		t.Fatalf("Restore changed context configuration: %+v", state)
	}
	if state.Usage.Used != 0 || state.Fuse.Tripped || len(state.Files.Entries) != 0 {
		t.Fatalf("Restore did not reset runtime state: %+v", state)
	}
}

package conversation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"onecode/internal/llm"
)

func TestToolResultBounderStoresSingleOversizedResult(t *testing.T) {
	root := t.TempDir()
	messages := []llm.Message{
		{Role: "user", Content: "please inspect"},
		{
			Role: "tool",
			ToolResult: &llm.ToolResult{
				ToolUseID: "call_1",
				Content:   strings.Repeat("a", 4000),
			},
		},
	}

	result, err := (&ToolResultBounder{Store: NewProjectStore(root)}).Bound(messages, BoundOptions{
		SingleMaxTokens: 100,
		BatchMaxTokens:  10000,
	})
	if err != nil {
		t.Fatalf("Bound returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected result to change")
	}
	if len(result.Stored) != 1 {
		t.Fatalf("expected 1 stored result, got %d", len(result.Stored))
	}
	if result.Messages[0].Content != "please inspect" {
		t.Fatalf("expected user message to be preserved, got %q", result.Messages[0].Content)
	}
	content := result.Messages[1].ToolResult.Content
	if !strings.Contains(content, storedToolResultMarker) || !strings.Contains(content, result.Stored[0].Path) {
		t.Fatalf("expected stored marker and path in tool result, got:\n%s", content)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(result.Stored[0].Path)))
	if err != nil {
		t.Fatalf("failed to read stored result: %v", err)
	}
	if string(data) != messages[1].ToolResult.Content {
		t.Fatal("stored content did not match original tool result")
	}
}

func TestToolResultBounderBatchStoresLargestFirst(t *testing.T) {
	root := t.TempDir()
	messages := []llm.Message{
		{
			Role:       "tool",
			ToolResult: &llm.ToolResult{ToolUseID: "small", Content: strings.Repeat("s", 400)},
		},
		{
			Role:       "tool",
			ToolResult: &llm.ToolResult{ToolUseID: "large", Content: strings.Repeat("l", 4000)},
		},
		{
			Role:       "tool",
			ToolResult: &llm.ToolResult{ToolUseID: "medium", Content: strings.Repeat("m", 2000)},
		},
	}

	result, err := (&ToolResultBounder{Store: NewProjectStore(root)}).Bound(messages, BoundOptions{
		SingleMaxTokens: 2000,
		BatchMaxTokens:  1000,
	})
	if err != nil {
		t.Fatalf("Bound returned error: %v", err)
	}
	if len(result.Stored) == 0 {
		t.Fatal("expected at least one stored result")
	}
	if result.Stored[0].ToolUseID != "large" {
		t.Fatalf("expected largest result to be stored first, got %+v", result.Stored)
	}
	if !strings.Contains(result.Messages[1].ToolResult.Content, storedToolResultMarker) {
		t.Fatalf("expected large result to be replaced, got:\n%s", result.Messages[1].ToolResult.Content)
	}
	if strings.Contains(result.Messages[0].ToolResult.Content, storedToolResultMarker) {
		t.Fatal("expected small result to remain inline")
	}
}

func TestToolResultBounderIsIdempotent(t *testing.T) {
	root := t.TempDir()
	messages := []llm.Message{
		{
			Role: "tool",
			ToolResult: &llm.ToolResult{
				ToolUseID: "call_1",
				Content:   strings.Repeat("a", 4000),
			},
		},
	}
	bounder := &ToolResultBounder{Store: NewProjectStore(root)}

	first, err := bounder.Bound(messages, BoundOptions{SingleMaxTokens: 100, BatchMaxTokens: 10000})
	if err != nil {
		t.Fatalf("first Bound returned error: %v", err)
	}
	second, err := bounder.Bound(first.Messages, BoundOptions{SingleMaxTokens: 100, BatchMaxTokens: 10000})
	if err != nil {
		t.Fatalf("second Bound returned error: %v", err)
	}
	if second.Changed {
		t.Fatal("expected second bound to be idempotent")
	}
	if len(second.Stored) != 0 {
		t.Fatalf("expected no newly stored results, got %+v", second.Stored)
	}
}

func TestToolResultBounderDoesNotRewriteUserOrAssistantMessages(t *testing.T) {
	root := t.TempDir()
	userContent := strings.Repeat("u", 4000)
	assistantContent := strings.Repeat("a", 4000)
	messages := []llm.Message{
		{Role: "user", Content: userContent},
		{Role: "assistant", Content: assistantContent},
	}

	result, err := (&ToolResultBounder{Store: NewProjectStore(root)}).Bound(messages, BoundOptions{
		SingleMaxTokens: 100,
		BatchMaxTokens:  100,
	})
	if err != nil {
		t.Fatalf("Bound returned error: %v", err)
	}
	if result.Changed {
		t.Fatal("expected no changes for user/assistant messages")
	}
	if result.Messages[0].Content != userContent || result.Messages[1].Content != assistantContent {
		t.Fatal("expected user and assistant content to be preserved")
	}
}

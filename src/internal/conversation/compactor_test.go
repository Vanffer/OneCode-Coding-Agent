package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"onecode/internal/llm"
)

type fakeCompressor struct {
	input CompactInput
	err   error
}

func (f *fakeCompressor) Summarize(_ context.Context, input CompactInput) (CompactOutput, error) {
	f.input = input
	if f.err != nil {
		return CompactOutput{}, f.err
	}
	return CompactOutput{
		Summary: "## Task Goal\nKeep working.",
	}, nil
}

func TestCompactorShouldCompact(t *testing.T) {
	compactor := Compactor{}
	window := WindowInfo{Limit: 200000}
	opts := ContextOptions{
		SummaryReserveTokens:    20000,
		AutoSafetyMarginTokens:  13000,
		ForceSafetyMarginTokens: 3000,
	}

	if !compactor.ShouldCompact(UsageEstimate{Used: 167000}, window, CompactModeAuto, opts, CompactFuse{}) {
		t.Fatal("expected auto compaction at threshold")
	}
	if compactor.ShouldCompact(UsageEstimate{Used: 166999}, window, CompactModeAuto, opts, CompactFuse{}) {
		t.Fatal("did not expect auto compaction below threshold")
	}
	if compactor.ShouldCompact(UsageEstimate{Used: 190000}, window, CompactModeAuto, opts, CompactFuse{Tripped: true}) {
		t.Fatal("did not expect auto compaction after fuse tripped")
	}
	if !compactor.ShouldCompact(UsageEstimate{Used: 197000}, window, CompactModeForce, opts, CompactFuse{Tripped: true}) {
		t.Fatal("expected force compaction to ignore fuse")
	}
	if !compactor.ShouldCompact(UsageEstimate{Used: 1}, window, CompactModeManual, opts, CompactFuse{}) {
		t.Fatal("expected manual compaction to be allowed")
	}
	if !compactor.ShouldCompact(UsageEstimate{Used: 1}, window, CompactModeEmergency, opts, CompactFuse{}) {
		t.Fatal("expected emergency compaction to be allowed")
	}
}

func TestCompactorCompactPreservesRecentMessages(t *testing.T) {
	messages := []llm.Message{
		{Role: "user", Content: "old 1"},
		{Role: "assistant", Content: "old 2"},
		{Role: "user", Content: "old 3"},
		{Role: "assistant", Content: "recent 1"},
		{Role: "user", Content: "recent 2"},
		{Role: "assistant", Content: "recent 3"},
	}
	state := ContextState{
		Window: WindowInfo{Limit: 200000},
		Usage:  UsageEstimate{Used: 1000, Limit: 200000},
		Files: &FileIndex{Entries: []FileIndexEntry{
			{Path: "src/main.go", Preview: "package main", Reason: "read"},
		}},
	}
	compressor := &fakeCompressor{}

	result, err := (Compactor{}).Compact(context.Background(), messages, state, compressor, CompactModeAuto, ContextOptions{
		RecentTokens:         1,
		RecentMinMessages:    3,
		SummaryReserveTokens: 1000,
	})
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	if len(compressor.input.Messages) != 2 {
		t.Fatalf("expected older 2 messages to be summarized, got %d", len(compressor.input.Messages))
	}
	if len(result.Messages) != 5 {
		t.Fatalf("expected boundary + adjusted recent messages, got %d", len(result.Messages))
	}
	if !strings.Contains(result.Messages[0].Content, contextSummaryBoundaryMark) {
		t.Fatalf("expected boundary message, got %q", result.Messages[0].Content)
	}
	if result.Messages[1].Content != "old 3" ||
		result.Messages[2].Content != "recent 1" ||
		result.Messages[3].Content != "recent 2" ||
		result.Messages[4].Content != "recent 3" {
		t.Fatalf("recent messages not preserved: %+v", result.Messages)
	}
	if !strings.Contains(result.Messages[0].Content, "src/main.go") {
		t.Fatalf("expected file index in boundary, got:\n%s", result.Messages[0].Content)
	}
	if len(result.Statuses) < 2 ||
		result.Statuses[0].Kind != ContextStatusCompactStarted ||
		result.Statuses[len(result.Statuses)-1].Kind != ContextStatusCompactCompleted {
		t.Fatalf("expected started and completed statuses, got %+v", result.Statuses)
	}
}

func TestCompactorCompactDoesNotSplitToolCallGroup(t *testing.T) {
	messages := []llm.Message{
		{Role: "user", Content: "old"},
		{Role: "user", Content: "inspect files"},
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "read_file", Input: map[string]interface{}{"path": "a.go"}},
				{ID: "call_2", Name: "grep", Input: map[string]interface{}{"pattern": "TODO"}},
			},
		},
		{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "call_1", Content: "file"}},
		{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "call_2", Content: "matches"}},
	}
	compressor := &fakeCompressor{}

	result, err := (Compactor{}).Compact(context.Background(), messages, ContextState{
		Window: WindowInfo{Limit: 200000},
	}, compressor, CompactModeAuto, ContextOptions{
		RecentTokens:         1,
		RecentMinMessages:    1,
		SummaryReserveTokens: 1000,
	})
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	if len(compressor.input.Messages) != 1 || compressor.input.Messages[0].Content != "old" {
		t.Fatalf("expected only unrelated old message to be summarized, got %+v", compressor.input.Messages)
	}
	if len(result.Messages) != 5 {
		t.Fatalf("expected boundary plus full tool group, got %+v", result.Messages)
	}
	if result.Messages[1].Content != "inspect files" ||
		len(result.Messages[2].ToolCalls) != 2 ||
		result.Messages[3].ToolResult.ToolUseID != "call_1" ||
		result.Messages[4].ToolResult.ToolUseID != "call_2" {
		t.Fatalf("tool call group was split: %+v", result.Messages)
	}
}

func TestCompactorCompactFailureStatus(t *testing.T) {
	compressor := &fakeCompressor{err: errors.New("model failed")}

	result, err := (Compactor{}).Compact(context.Background(), []llm.Message{
		{Role: "user", Content: "hello"},
	}, ContextState{Window: WindowInfo{Limit: 100}}, compressor, CompactModeManual, ContextOptions{})
	if err == nil {
		t.Fatal("expected compact error")
	}
	if len(result.Statuses) == 0 || result.Statuses[len(result.Statuses)-1].Kind != ContextStatusCompactFailed {
		t.Fatalf("expected compact failed status, got %+v", result.Statuses)
	}
}

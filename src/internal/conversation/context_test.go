package conversation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"onecode/internal/llm"
)

func TestPreflightBoundsToolResults(t *testing.T) {
	root := t.TempDir()
	conv := New(WithContextOptions(ContextOptions{ProjectRoot: root, ProviderWindow: 100000}))
	conv.AddUser("keep me")
	conv.AddToolResult(llm.ToolResult{
		ToolUseID: "call_1",
		Content:   strings.Repeat("x", 4000),
	})

	result, err := conv.Preflight(context.Background(), nil, ContextOptions{
		ProjectRoot:              root,
		ProviderWindow:           100000,
		ToolResultMaxTokens:      100,
		ToolResultBatchMaxTokens: 10000,
	})
	if err != nil {
		t.Fatalf("Preflight returned error: %v", err)
	}
	if len(result.BoundedToolResults) != 1 {
		t.Fatalf("expected one bounded tool result, got %+v", result.BoundedToolResults)
	}
	msgs := conv.Messages()
	if msgs[0].Content != "keep me" {
		t.Fatalf("expected user message to be preserved, got %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[1].ToolResult.Content, storedToolResultMarker) {
		t.Fatalf("expected stored marker, got:\n%s", msgs[1].ToolResult.Content)
	}
	if result.Usage.Used <= 0 {
		t.Fatalf("expected usage update, got %+v", result.Usage)
	}
}

func TestPreflightAutoCompacts(t *testing.T) {
	root := t.TempDir()
	conv := New(WithContextOptions(ContextOptions{ProjectRoot: root, ProviderWindow: 50000}))
	conv.AddUser("older content")
	conv.UpdateUsage(llm.Usage{TotalTokens: 49980, Available: true})
	compressor := &fakeCompressor{}

	result, err := conv.Preflight(context.Background(), compressor, ContextOptions{
		ProjectRoot:              root,
		ProviderWindow:           50000,
		SummaryReserveTokens:     10,
		AutoSafetyMarginTokens:   10,
		ForceSafetyMarginTokens:  1,
		ToolResultMaxTokens:      10000,
		ToolResultBatchMaxTokens: 10000,
	})
	if err != nil {
		t.Fatalf("Preflight returned error: %v", err)
	}
	if !result.Compacted || result.CompactMode != CompactModeAuto {
		t.Fatalf("expected auto compaction, got %+v", result)
	}
	if len(conv.Messages()) != 1 || !strings.Contains(conv.Messages()[0].Content, contextSummaryBoundaryMark) {
		t.Fatalf("expected compacted boundary message, got %+v", conv.Messages())
	}
	if conv.ContextState().Fuse.Tripped || conv.ContextState().Fuse.ConsecutiveFailures != 0 {
		t.Fatalf("expected fuse reset after successful auto compact, got %+v", conv.ContextState().Fuse)
	}
}

func TestPreflightAutoCompactFailureTripsFuse(t *testing.T) {
	root := t.TempDir()
	conv := New(WithContextOptions(ContextOptions{ProjectRoot: root, ProviderWindow: 50000}))
	conv.AddUser("older content")
	conv.UpdateUsage(llm.Usage{TotalTokens: 49980, Available: true})
	compressor := &fakeCompressor{err: errors.New("model failed")}

	var last PreflightResult
	for i := 0; i < 3; i++ {
		result, err := conv.Preflight(context.Background(), compressor, ContextOptions{
			ProjectRoot:              root,
			ProviderWindow:           50000,
			SummaryReserveTokens:     10,
			AutoSafetyMarginTokens:   10,
			ForceSafetyMarginTokens:  1,
			MaxCompactFailures:       3,
			ToolResultMaxTokens:      10000,
			ToolResultBatchMaxTokens: 10000,
		})
		if err != nil {
			t.Fatalf("auto compact failure should not abort preflight, got %v", err)
		}
		last = result
	}

	state := conv.ContextState()
	if !state.Fuse.Tripped || state.Fuse.ConsecutiveFailures != 3 {
		t.Fatalf("expected fuse to trip after 3 failures, got %+v", state.Fuse)
	}
	found := false
	for _, status := range last.Statuses {
		if status.Kind == ContextStatusCompactFuseTripped {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected fuse tripped status, got %+v", last.Statuses)
	}
}

func TestPreflightForceCompactFailureReturnsError(t *testing.T) {
	root := t.TempDir()
	conv := New(WithContextOptions(ContextOptions{ProjectRoot: root, ProviderWindow: 50000}))
	conv.AddUser("older content")
	conv.UpdateUsage(llm.Usage{TotalTokens: 49999, Available: true})

	_, err := conv.Preflight(context.Background(), &fakeCompressor{err: errors.New("model failed")}, ContextOptions{
		ProjectRoot:              root,
		ProviderWindow:           50000,
		SummaryReserveTokens:     10,
		AutoSafetyMarginTokens:   10,
		ForceSafetyMarginTokens:  1,
		ToolResultMaxTokens:      10000,
		ToolResultBatchMaxTokens: 10000,
	})
	if err == nil {
		t.Fatal("expected force compact failure to abort preflight")
	}
}

func TestPostToolResultsAutoCompacts(t *testing.T) {
	root := t.TempDir()
	conv := New(WithContextOptions(ContextOptions{ProjectRoot: root, ProviderWindow: 50000}))
	conv.AddUser("inspect")
	conv.AddAssistantWithToolCalls("", []llm.ToolCall{
		{ID: "call_1", Name: "read_file", Input: map[string]interface{}{"path": "a.go"}},
	})
	conv.UpdateUsage(llm.Usage{TotalTokens: 49980, Available: true})
	conv.AddToolResult(llm.ToolResult{ToolUseID: "call_1", Content: "file"})
	compressor := &fakeCompressor{}

	result, err := conv.PostToolResults(context.Background(), compressor, ContextOptions{
		ProjectRoot:              root,
		ProviderWindow:           50000,
		SummaryReserveTokens:     10,
		AutoSafetyMarginTokens:   10,
		ForceSafetyMarginTokens:  1,
		ToolResultMaxTokens:      10000,
		ToolResultBatchMaxTokens: 10000,
	})
	if err != nil {
		t.Fatalf("PostToolResults returned error: %v", err)
	}
	if !result.Compacted || result.CompactMode != CompactModeAuto {
		t.Fatalf("expected post-tool auto compaction, got %+v", result)
	}
	if len(conv.Messages()) != 1 || !strings.Contains(conv.Messages()[0].Content, contextSummaryBoundaryMark) {
		t.Fatalf("expected compacted boundary message, got %+v", conv.Messages())
	}
}

func TestConversationUpdateUsageAnchor(t *testing.T) {
	conv := New(WithContextOptions(ContextOptions{ProviderWindow: 1000}))
	conv.AddUser("hello")
	conv.UpdateUsage(llm.Usage{InputTokens: 30, OutputTokens: 10, Available: true})

	state := conv.ContextState()
	if state.Usage.Used != 40 {
		t.Fatalf("expected usage total 40, got %+v", state.Usage)
	}
	if state.Usage.Estimated {
		t.Fatal("expected provider usage not to be marked estimated")
	}
	if state.Usage.Anchor.MessageCount != 1 {
		t.Fatalf("expected anchor message count 1, got %+v", state.Usage.Anchor)
	}
}

func TestConversationSetContextWindow(t *testing.T) {
	root := t.TempDir()
	conv := New(WithContextOptions(ContextOptions{ProjectRoot: root}))

	window, err := conv.SetContextWindow(context.Background(), 200000)
	if err != nil {
		t.Fatalf("SetContextWindow returned error: %v", err)
	}
	if window.Limit != 200000 || window.Source != WindowSourceLocal {
		t.Fatalf("expected local window, got %+v", window)
	}
	cfg, ok, err := NewProjectStore(root).LoadLocalConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadLocalConfig returned error: %v", err)
	}
	if !ok || cfg.ContextWindow != 200000 {
		t.Fatalf("expected saved local config, ok=%v cfg=%+v", ok, cfg)
	}
	if filepath.ToSlash(conv.ContextState().Store.ContextDir) == "" {
		t.Fatal("expected context dir to be initialized")
	}
}

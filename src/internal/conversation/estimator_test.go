package conversation

import (
	"testing"

	"onecode/internal/llm"
)

func TestTokenEstimatorEstimateText(t *testing.T) {
	estimator := TokenEstimator{}

	if got := estimator.EstimateText(""); got != 0 {
		t.Fatalf("expected empty text to estimate 0, got %d", got)
	}
	if got := estimator.EstimateText("abcdefghijklmnop"); got != 4 {
		t.Fatalf("expected 16 chars to estimate 4 tokens, got %d", got)
	}
	if got := estimator.EstimateText("hello"); got != 2 {
		t.Fatalf("expected rounded estimate for 5 chars to be 2, got %d", got)
	}
}

func TestTokenEstimatorEstimateMessageIncludesToolData(t *testing.T) {
	estimator := TokenEstimator{}
	msg := llm.Message{
		Role:    "assistant",
		Content: "I will inspect files",
		ToolCalls: []llm.ToolCall{
			{
				ID:    "call_1",
				Name:  "read_file",
				Input: map[string]interface{}{"path": "main.go"},
			},
		},
	}
	got := estimator.EstimateMessage(msg)
	base := messageTokenOverhead + estimator.EstimateText(msg.Role) + estimator.EstimateText(msg.Content)
	if got <= base {
		t.Fatalf("expected tool call data to increase estimate, base=%d got=%d", base, got)
	}

	toolMsg := llm.Message{
		Role: "tool",
		ToolResult: &llm.ToolResult{
			ToolUseID: "call_1",
			Content:   "line 1\nline 2\nline 3",
		},
	}
	if toolEstimate := estimator.EstimateMessage(toolMsg); toolEstimate <= messageTokenOverhead {
		t.Fatalf("expected tool result content to be estimated, got %d", toolEstimate)
	}
}

func TestTokenEstimatorEstimateWithoutAnchor(t *testing.T) {
	estimator := TokenEstimator{}
	messages := []llm.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	got := estimator.Estimate(messages, WindowInfo{Limit: 100}, UsageAnchor{})
	if got.Used <= 0 {
		t.Fatalf("expected positive estimate, got %+v", got)
	}
	if got.Limit != 100 {
		t.Fatalf("expected limit 100, got %d", got.Limit)
	}
	if !got.Estimated {
		t.Fatal("expected estimate without anchor to be marked estimated")
	}
	if got.Percent != got.Used {
		t.Fatalf("expected percent to be used/100*100, got used=%d percent=%d", got.Used, got.Percent)
	}
}

func TestTokenEstimatorUsesAnchorForIncrementalMessages(t *testing.T) {
	estimator := TokenEstimator{}
	messages := []llm.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
		{Role: "user", Content: "third"},
	}
	anchor := UsageAnchor{
		MessageCount: 2,
		Usage: llm.Usage{
			TotalTokens: 100,
			Available:   true,
		},
	}

	got := estimator.Estimate(messages, WindowInfo{Limit: 200}, anchor)
	want := 100 + estimator.EstimateMessage(messages[2])
	if got.Used != want {
		t.Fatalf("expected anchored estimate %d, got %d", want, got.Used)
	}
	if !got.Estimated {
		t.Fatal("expected added messages after anchor to be marked estimated")
	}
	if got.Percent != want*100/200 {
		t.Fatalf("expected percent %d, got %d", want*100/200, got.Percent)
	}
}

func TestTokenEstimatorAnchorCoveringAllMessagesIsNotEstimated(t *testing.T) {
	estimator := TokenEstimator{}
	messages := []llm.Message{
		{Role: "user", Content: "first"},
	}
	anchor := UsageAnchor{
		MessageCount: 1,
		Usage: llm.Usage{
			InputTokens:  30,
			OutputTokens: 10,
			Available:    true,
			Cache: llm.CacheUsage{
				CreationInputTokens: 5,
				ReadInputTokens:     2,
			},
		},
	}

	got := estimator.Estimate(messages, WindowInfo{Limit: 100}, anchor)
	if got.Used != 47 {
		t.Fatalf("expected usage total from input/output/cache, got %d", got.Used)
	}
	if got.Estimated {
		t.Fatal("expected fully anchored usage not to be marked estimated")
	}
}

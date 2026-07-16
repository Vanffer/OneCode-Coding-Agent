package agent

import (
	"context"
	"strings"
	"testing"

	"onecode/internal/conversation"
	"onecode/internal/llm"
)

func TestProviderCompressorSummarize(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{Text: "<analysis_draft>draft</analysis_draft>"},
			{Text: "<formal_summary>## Task Goal\nKeep going.</formal_summary>"},
			{Done: true, FinishReason: llm.FinishStop},
		}},
	}}

	output, err := (providerCompressor{provider: provider}).Summarize(context.Background(), conversation.CompactInput{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	if !strings.Contains(output.Summary, "Keep going") {
		t.Fatalf("expected formal summary, got %q", output.Summary)
	}
	call := provider.callAt(0)
	if len(call.tools) != 0 {
		t.Fatalf("expected compression request without tools, got %+v", call.tools)
	}
	if call.prompt.StableSystem != compactStablePrompt {
		t.Fatalf("expected compact stable prompt, got %q", call.prompt.StableSystem)
	}
	if len(call.messages) != 1 || !strings.Contains(call.messages[0].Content, "Do not call tools") {
		t.Fatalf("expected compact prompt message, got %+v", call.messages)
	}
}

func TestProviderCompressorRejectsToolCall(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "call_1", Name: "read_file"}},
			{Done: true, FinishReason: llm.FinishToolCalls},
		}},
	}}

	_, err := (providerCompressor{provider: provider}).Summarize(context.Background(), conversation.CompactInput{})
	if err == nil {
		t.Fatal("expected tool call rejection")
	}
	if !strings.Contains(err.Error(), "不允许工具调用") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProviderCompressorRequiresFormalSummary(t *testing.T) {
	provider := &scriptedProvider{scripts: []streamScript{
		{events: []llm.StreamEvent{
			{Text: "plain summary"},
			{Done: true, FinishReason: llm.FinishStop},
		}},
	}}

	_, err := (providerCompressor{provider: provider}).Summarize(context.Background(), conversation.CompactInput{})
	if err == nil {
		t.Fatal("expected missing formal summary error")
	}
}

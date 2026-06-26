package agent

import (
	"context"
	"testing"

	"onecode/internal/llm"
)

func TestCollectModelResponse(t *testing.T) {
	stream := make(chan llm.StreamEvent, 5)
	errs := make(chan error)
	close(errs)
	events := make(chan Event, 10)

	stream <- llm.StreamEvent{Text: "hello "}
	stream <- llm.StreamEvent{ToolCall: &llm.ToolCall{ID: "call_1", Name: "read_file", Input: map[string]interface{}{"path": "main.go"}}}
	stream <- llm.StreamEvent{Usage: &llm.Usage{InputTokens: 3, OutputTokens: 5, TotalTokens: 8, Available: true}}
	stream <- llm.StreamEvent{Text: "world"}
	stream <- llm.StreamEvent{Done: true, FinishReason: llm.FinishToolCalls}
	close(stream)

	var a Agent
	response, err := a.collectModelResponse(context.Background(), stream, errs, events, 1)
	if err != nil {
		t.Fatalf("collectModelResponse returned error: %v", err)
	}
	if response.Text != "hello world" {
		t.Fatalf("expected full text, got %q", response.Text)
	}
	if len(response.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(response.ToolCalls))
	}
	if response.ToolCalls[0].ID != "call_1" {
		t.Fatalf("expected call_1, got %q", response.ToolCalls[0].ID)
	}
	if !response.Usage.Available || response.Usage.TotalTokens != 8 {
		t.Fatalf("expected available usage with total 8, got %+v", response.Usage)
	}
	if response.FinishReason != llm.FinishToolCalls {
		t.Fatalf("expected FinishToolCalls, got %v", response.FinishReason)
	}

	var sawText, sawUsage bool
	for len(events) > 0 {
		event := <-events
		switch event.Type {
		case EventText:
			sawText = true
		case EventUsage:
			sawUsage = true
		}
	}
	if !sawText {
		t.Fatal("expected text event")
	}
	if !sawUsage {
		t.Fatal("expected usage event")
	}
}

func TestCollectModelResponseStreamError(t *testing.T) {
	stream := make(chan llm.StreamEvent)
	close(stream)
	errs := make(chan error, 1)
	errs <- &llm.LLMError{Message: "boom"}
	close(errs)
	events := make(chan Event, 10)

	var a Agent
	_, err := a.collectModelResponse(context.Background(), stream, errs, events, 1)
	if err == nil {
		t.Fatal("expected stream error")
	}
}

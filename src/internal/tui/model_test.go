package tui

import (
	"context"
	"strings"
	"testing"

	"onecode/internal/conversation"
	"onecode/internal/llm"
)

type tuiTestProvider struct{}

func (tuiTestProvider) Name() string  { return "Test" }
func (tuiTestProvider) Model() string { return "test-model" }
func (tuiTestProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, llm.StreamOptions) (<-chan llm.StreamEvent, <-chan error) {
	events := make(chan llm.StreamEvent)
	errs := make(chan error)
	close(events)
	close(errs)
	return events, errs
}

func TestStatusBarIncludesContext(t *testing.T) {
	got := statusBar(tuiTestProvider{}, "Ready", "Context ~1k / 200k · 1%", 120)
	for _, want := range []string{"Test", "Ready", "Context ~1k / 200k", "test-model"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected status bar to contain %q, got %q", want, got)
		}
	}
}

func TestContextDisplayEstimated(t *testing.T) {
	model := Model{
		conv: conversation.New(),
		contextUsage: conversation.UsageEstimate{
			Used:      42000,
			Limit:     200000,
			Percent:   21,
			Estimated: true,
		},
		contextWindow: conversation.WindowInfo{
			Limit:  200000,
			Source: conversation.WindowSourceInferred,
		},
	}

	got := model.contextDisplay()
	if !strings.Contains(got, "Context ~42k / 200k · 21%") {
		t.Fatalf("unexpected context display: %q", got)
	}
}

func TestContextWindowCommandSavesLocalConfig(t *testing.T) {
	root := t.TempDir()
	model := Model{
		conv:  conversation.New(conversation.WithContextOptions(conversation.ContextOptions{ProjectRoot: root})),
		state: stateIdle,
	}

	next, _, handled := model.handleSlashCommand("/context window 200000")
	if !handled {
		t.Fatal("expected context window command to be handled")
	}
	if next.state != stateIdle {
		t.Fatalf("expected idle state after setting window, got %v", next.state)
	}
	cfg, ok, err := conversation.NewProjectStore(root).LoadLocalConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadLocalConfig returned error: %v", err)
	}
	if !ok || cfg.ContextWindow != 200000 {
		t.Fatalf("expected saved context window, ok=%v cfg=%+v", ok, cfg)
	}
}

func TestContextWindowCommandEntersInputState(t *testing.T) {
	model := New(nil, nil, t.TempDir())
	model.state = stateIdle

	next, _, handled := model.handleSlashCommand("/context window")
	if !handled {
		t.Fatal("expected context window command to be handled")
	}
	if next.state != stateContextWindowInput {
		t.Fatalf("expected context window input state, got %v", next.state)
	}
	if next.textarea.Placeholder == mainInputPlaceholder {
		t.Fatal("expected context-specific placeholder")
	}
}

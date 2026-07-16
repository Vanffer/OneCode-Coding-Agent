package conversation

import (
	"strings"
	"testing"

	"onecode/internal/llm"
)

func TestSummaryBuilderBuildInputSnapshotsSlices(t *testing.T) {
	builder := SummaryBuilder{}
	messages := []llm.Message{{Role: "user", Content: "hello"}}
	files := []FileIndexEntry{{Path: "main.go", Preview: "package main"}}
	stored := []StoredToolResult{{Path: ".onecode/context/tool-results/call.txt"}}

	input := builder.BuildInput(messages, files, stored, 1000)
	messages[0].Content = "changed"
	files[0].Path = "changed.go"
	stored[0].Path = "changed.txt"

	if input.Messages[0].Content != "hello" {
		t.Fatalf("expected messages snapshot, got %+v", input.Messages)
	}
	if input.FileIndex[0].Path != "main.go" {
		t.Fatalf("expected file index snapshot, got %+v", input.FileIndex)
	}
	if input.StoredResults[0].Path != ".onecode/context/tool-results/call.txt" {
		t.Fatalf("expected stored results snapshot, got %+v", input.StoredResults)
	}
}

func TestSummaryBuilderPromptContainsRequirements(t *testing.T) {
	builder := SummaryBuilder{}
	input := CompactInput{
		Messages: []llm.Message{{Role: "user", Content: "build this feature"}},
		FileIndex: []FileIndexEntry{{
			Path:    "src/main.go",
			Preview: "package main",
			Reason:  "read",
		}},
		StoredResults: []StoredToolResult{{
			Path:    ".onecode/context/tool-results/call.txt",
			Bytes:   123,
			Preview: "long output",
		}},
		BudgetTokens: 1000,
	}

	prompt := builder.Prompt(input)
	for _, want := range []string{
		"Do not call tools",
		"<analysis_draft>",
		"<formal_summary>",
		"## Task Goal",
		"src/main.go",
		".onecode/context/tool-results/call.txt",
		"build this feature",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestSummaryBuilderParseOutputKeepsOnlyFormalSummary(t *testing.T) {
	builder := SummaryBuilder{}
	raw := `<analysis_draft>
private notes that should be discarded
</analysis_draft>

<formal_summary>
## Task Goal
Ship context management.
</formal_summary>`

	summary, err := builder.ParseOutput(raw)
	if err != nil {
		t.Fatalf("ParseOutput returned error: %v", err)
	}
	if strings.Contains(summary, "private notes") {
		t.Fatalf("expected draft to be discarded, got %q", summary)
	}
	if !strings.Contains(summary, "Ship context management") {
		t.Fatalf("expected formal summary, got %q", summary)
	}
}

func TestSummaryBuilderParseOutputRequiresFormalSummary(t *testing.T) {
	_, err := (SummaryBuilder{}).ParseOutput("plain text")
	if err == nil {
		t.Fatal("expected missing formal summary error")
	}
}

func TestSummaryBuilderBoundaryMessage(t *testing.T) {
	msg := (SummaryBuilder{}).BoundaryMessage("## Task Goal\nKeep working.", []FileIndexEntry{
		{Path: "src/main.go", Preview: "package main", Reason: "read"},
	})

	if msg.Role != "user" {
		t.Fatalf("expected boundary message to use user role, got %q", msg.Role)
	}
	for _, want := range []string{
		contextSummaryBoundaryMark,
		"Keep working",
		"re-read",
		"Do not invent",
		"src/main.go",
		"package main",
	} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("expected boundary message to contain %q, got:\n%s", want, msg.Content)
		}
	}
}

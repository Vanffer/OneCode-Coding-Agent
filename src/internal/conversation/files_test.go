package conversation

import (
	"strconv"
	"strings"
	"testing"

	"onecode/internal/llm"
)

func TestFileIndexObserveReadFile(t *testing.T) {
	idx := &FileIndex{}
	idx.ObserveToolCall(llm.ToolCall{
		Name:  "read_file",
		Input: map[string]interface{}{"path": "src/main.go"},
	}, llm.ToolResult{
		Content: "1\tpackage main\n2\tfunc main() {}",
	})

	recent := idx.Recent(10)
	if len(recent) == 0 {
		t.Fatal("expected file index entry")
	}
	if recent[0].Path != "src/main.go" {
		t.Fatalf("expected read path, got %+v", recent[0])
	}
	if recent[0].Reason != "read" {
		t.Fatalf("expected read reason, got %+v", recent[0])
	}
	if !strings.Contains(recent[0].Preview, "package main") {
		t.Fatalf("expected preview from tool result, got %q", recent[0].Preview)
	}
}

func TestFileIndexObserveEditFile(t *testing.T) {
	idx := &FileIndex{}
	idx.ObserveToolCall(llm.ToolCall{
		Name:  "edit_file",
		Input: map[string]interface{}{"path": "src/main.go"},
	}, llm.ToolResult{
		Content: "编辑成功",
	})

	recent := idx.Recent(1)
	if len(recent) != 1 {
		t.Fatalf("expected one entry, got %d", len(recent))
	}
	if recent[0].Reason != "edited" {
		t.Fatalf("expected edited reason, got %+v", recent[0])
	}
}

func TestFileIndexObserveStoredToolResult(t *testing.T) {
	idx := &FileIndex{}
	idx.ObserveStoredToolResult(StoredToolResult{
		Path:    ".onecode/context/tool-results/call.txt",
		Preview: "stored preview",
	})

	recent := idx.Recent(1)
	if len(recent) != 1 {
		t.Fatalf("expected one entry, got %d", len(recent))
	}
	if recent[0].Path != ".onecode/context/tool-results/call.txt" {
		t.Fatalf("expected stored result path, got %+v", recent[0])
	}
	if recent[0].Reason != "stored tool result" {
		t.Fatalf("expected stored reason, got %+v", recent[0])
	}
}

func TestFileIndexUpdatesDuplicatePath(t *testing.T) {
	idx := &FileIndex{}
	idx.ObserveToolCall(llm.ToolCall{
		Name:  "read_file",
		Input: map[string]interface{}{"path": "src/main.go"},
	}, llm.ToolResult{Content: "old"})
	idx.ObserveToolCall(llm.ToolCall{
		Name:  "write_file",
		Input: map[string]interface{}{"path": "src/main.go"},
	}, llm.ToolResult{Content: "new"})

	recent := idx.Recent(10)
	if len(recent) != 1 {
		t.Fatalf("expected duplicate path to update, got %+v", recent)
	}
	if recent[0].Reason != "edited" || recent[0].Preview != "new" {
		t.Fatalf("expected duplicate path to update reason and preview, got %+v", recent[0])
	}
}

func TestFileIndexLimitsEntries(t *testing.T) {
	idx := &FileIndex{}
	for i := 0; i < defaultFileIndexLimit+5; i++ {
		idx.ObserveToolCall(llm.ToolCall{
			Name:  "read_file",
			Input: map[string]interface{}{"path": "src/file_" + strconv.Itoa(i) + ".go"},
		}, llm.ToolResult{
			Content: strings.Repeat("x", i+1),
		})
	}

	recent := idx.Recent(defaultFileIndexLimit + 10)
	if len(recent) != defaultFileIndexLimit {
		t.Fatalf("expected index to be limited to %d entries, got %d", defaultFileIndexLimit, len(recent))
	}
}

func TestFileIndexPreviewLimit(t *testing.T) {
	idx := &FileIndex{}
	idx.ObserveToolCall(llm.ToolCall{
		Name:  "read_file",
		Input: map[string]interface{}{"path": "big.txt"},
	}, llm.ToolResult{Content: strings.Repeat("x", defaultFileIndexPreviewSize+100)})

	recent := idx.Recent(1)
	if len(recent) != 1 {
		t.Fatalf("expected one entry, got %d", len(recent))
	}
	if len([]rune(recent[0].Preview)) > defaultFileIndexPreviewSize+len("\n... truncated") {
		t.Fatalf("expected preview to be limited, got %d runes", len([]rune(recent[0].Preview)))
	}
}

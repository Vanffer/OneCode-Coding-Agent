package conversation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"onecode/internal/llm"
)

func TestProjectStoreEnsureCreatesDirsAndPreservesGitignore(t *testing.T) {
	root := t.TempDir()
	store := NewProjectStore(root)
	if err := os.MkdirAll(filepath.Join(root, contextDirName), 0755); err != nil {
		t.Fatalf("failed to create context dir: %v", err)
	}
	gitignore := filepath.Join(root, contextDirName, contextGitignore)
	if err := os.WriteFile(gitignore, []byte("# custom\ncustom.log\n"), 0644); err != nil {
		t.Fatalf("failed to write gitignore: %v", err)
	}

	if err := store.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure returned error: %v", err)
	}

	if info, err := os.Stat(filepath.Join(root, contextDirName, toolResultsDirName)); err != nil || !info.IsDir() {
		t.Fatalf("expected tool results dir, info=%+v err=%v", info, err)
	}
	data, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatalf("failed to read gitignore: %v", err)
	}
	content := string(data)
	for _, want := range []string{"# custom", "custom.log", "local.yaml", "tool-results/"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected gitignore to contain %q, got:\n%s", want, content)
		}
	}
	if strings.Count(content, "local.yaml") != 1 || strings.Count(content, "tool-results/") != 1 {
		t.Fatalf("expected required rules once, got:\n%s", content)
	}
}

func TestProjectStoreStoreToolResult(t *testing.T) {
	root := t.TempDir()
	store := NewProjectStore(root)
	result := llm.ToolResult{
		ToolUseID: "../call 1",
		Content:   "full tool result",
	}

	stored, err := store.StoreToolResult(context.Background(), result, "")
	if err != nil {
		t.Fatalf("StoreToolResult returned error: %v", err)
	}
	if stored.ToolUseID != result.ToolUseID {
		t.Fatalf("expected tool use id to be preserved, got %q", stored.ToolUseID)
	}
	if stored.Bytes != len([]byte(result.Content)) {
		t.Fatalf("expected byte count %d, got %d", len([]byte(result.Content)), stored.Bytes)
	}
	if !strings.HasPrefix(stored.Path, ".onecode/context/tool-results/") {
		t.Fatalf("expected project-relative context path, got %q", stored.Path)
	}
	if strings.Contains(stored.Path, "..") || strings.Contains(stored.Path, " ") {
		t.Fatalf("expected sanitized path, got %q", stored.Path)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(stored.Path)))
	if err != nil {
		t.Fatalf("failed to read stored result: %v", err)
	}
	if string(data) != result.Content {
		t.Fatalf("expected stored content %q, got %q", result.Content, string(data))
	}
	if stored.Preview != result.Content {
		t.Fatalf("expected generated preview, got %q", stored.Preview)
	}
}

func TestProjectStoreLocalConfig(t *testing.T) {
	root := t.TempDir()
	store := NewProjectStore(root)

	if _, ok, err := store.LoadLocalConfig(context.Background()); err != nil || ok {
		t.Fatalf("expected missing local config, ok=%v err=%v", ok, err)
	}

	if err := store.SaveLocalConfig(context.Background(), LocalConfig{ContextWindow: 200000}); err != nil {
		t.Fatalf("SaveLocalConfig returned error: %v", err)
	}
	cfg, ok, err := store.LoadLocalConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadLocalConfig returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected local config to exist")
	}
	if cfg.ContextWindow != 200000 {
		t.Fatalf("expected context window 200000, got %d", cfg.ContextWindow)
	}
}

func TestProjectStoreRejectsOutsideContextDir(t *testing.T) {
	root := t.TempDir()
	store := &ProjectStore{
		ProjectRoot: root,
		ContextDir:  filepath.Join(root, "outside"),
	}

	err := store.Ensure(context.Background())
	if err == nil {
		t.Fatal("expected outside context dir to be rejected")
	}
	if !strings.Contains(err.Error(), "上下文目录外") {
		t.Fatalf("unexpected error: %v", err)
	}
}

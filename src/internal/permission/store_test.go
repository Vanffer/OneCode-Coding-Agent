package permission

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStoreMissingFiles(t *testing.T) {
	root := testProjectRoot(t)
	store := NewFileStore(
		filepath.Join(root, "missing-user.yaml"),
		filepath.Join(root, "missing-project.yaml"),
		filepath.Join(root, ".onecode", "missing-local.yaml"),
		root,
	)
	sets, mode, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(sets) != 0 || mode != ModeDefault {
		t.Fatalf("expected empty default config, got sets=%v mode=%s", sets, mode)
	}
}

func TestFileStoreLoadAndModePrecedence(t *testing.T) {
	root := testProjectRoot(t)
	user := filepath.Join(root, "user.yaml")
	project := filepath.Join(root, ".onecode", "permissions.yaml")
	local := filepath.Join(root, ".onecode", "permissions.local.yaml")
	if err := os.MkdirAll(filepath.Dir(project), 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, user, "mode: strict\nrules:\n  - \"Bash(git *): allow\"\n")
	mustWrite(t, project, "mode: bypassPermissions\nrules:\n  - \"Bash(git push *): deny\"\n")
	mustWrite(t, local, "mode: acceptEdits\nrules:\n  - \"Bash(git push origin dev): allow\"\n")

	store := NewFileStore(user, project, local, root)
	sets, mode, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if mode != ModeAcceptEdits {
		t.Fatalf("expected local mode to win, got %s", mode)
	}
	if len(sets) != 3 {
		t.Fatalf("expected 3 rulesets, got %d", len(sets))
	}
}

func TestFileStoreRejectsBadRule(t *testing.T) {
	root := testProjectRoot(t)
	user := filepath.Join(root, "user.yaml")
	mustWrite(t, user, "rules:\n  - \"bad\"\n")

	store := NewFileStore(user, "", "", root)
	_, _, err := store.Load(context.Background())
	if err == nil {
		t.Fatal("expected bad rule error")
	}
}

func TestFileStoreAppendLocalRule(t *testing.T) {
	root := testProjectRoot(t)
	local := filepath.Join(root, ".onecode", "permissions.local.yaml")
	store := NewFileStore("", "", local, root)

	rule := Rule{Tool: "bash", Pattern: "git status", Action: ActionAllow, Scope: ScopeLocal}
	if err := store.AppendLocalRule(context.Background(), rule); err != nil {
		t.Fatalf("AppendLocalRule returned error: %v", err)
	}
	data, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Bash(git status): allow") {
		t.Fatalf("expected exact rule in local config, got:\n%s", string(data))
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

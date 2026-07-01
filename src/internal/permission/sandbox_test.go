package permission

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSandboxAllowsProjectPath(t *testing.T) {
	root := testProjectRoot(t)
	file := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	sandbox, err := NewSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.CheckPath("src/main.go", PathCheckOptions{}); err != nil {
		t.Fatalf("expected project path to be allowed: %v", err)
	}
}

func TestSandboxRejectsParentEscape(t *testing.T) {
	root := testProjectRoot(t)
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(outside)
	})

	sandbox, err := NewSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.CheckPath("../secret.txt", PathCheckOptions{}); err == nil {
		t.Fatal("expected parent path escape to be rejected")
	}
}

func TestSandboxMissingLeaf(t *testing.T) {
	root := testProjectRoot(t)
	sandbox, err := NewSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.CheckPath("new/file.go", PathCheckOptions{AllowMissingLeaf: true}); err != nil {
		t.Fatalf("expected missing leaf inside root to be allowed: %v", err)
	}
	if _, err := sandbox.CheckPath("../new/file.go", PathCheckOptions{AllowMissingLeaf: true}); err == nil {
		t.Fatal("expected missing leaf outside root to be rejected")
	}
}

func TestSandboxRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := testProjectRoot(t)
	outside := testProjectRoot(t)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	sandbox, err := NewSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.CheckPath("link", PathCheckOptions{}); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

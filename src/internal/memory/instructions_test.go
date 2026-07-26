package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstructionLoaderPriorityAndMissingFiles(t *testing.T) {
	project := t.TempDir()
	userRoot := filepath.Join(t.TempDir(), ".onecode")
	mustWriteInstruction(t, filepath.Join(project, ".onecode", "ONECODE.md"), "local rules")
	mustWriteInstruction(t, filepath.Join(project, "ONECODE.md"), "root rules")
	mustWriteInstruction(t, filepath.Join(userRoot, "ONECODE.md"), "user rules")

	got := (InstructionLoader{ProjectRoot: project, UserRoot: userRoot}).Load()
	localAt := strings.Index(got.Content, "local rules")
	rootAt := strings.Index(got.Content, "root rules")
	userAt := strings.Index(got.Content, "user rules")
	if localAt < 0 || rootAt <= localAt || userAt <= rootAt {
		t.Fatalf("unexpected instruction order: %q", got.Content)
	}
	if len(got.Sources) != 3 || len(got.Warnings) != 0 {
		t.Fatalf("unexpected load result: sources=%v warnings=%v", got.Sources, got.Warnings)
	}
}

func TestInstructionLoaderNestedIncludeAndQuotedPath(t *testing.T) {
	project := t.TempDir()
	mustWriteInstruction(t, filepath.Join(project, "ONECODE.md"), "before\n@include \"docs/team rules.md\"\nafter")
	mustWriteInstruction(t, filepath.Join(project, "docs", "team rules.md"), "team\n@include nested.md")
	mustWriteInstruction(t, filepath.Join(project, "docs", "nested.md"), "nested")

	got := (InstructionLoader{ProjectRoot: project}).Load()
	if !strings.Contains(got.Content, "before\nteam\nnested\nafter") {
		t.Fatalf("include was not expanded in place: %q", got.Content)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", got.Warnings)
	}
}

func TestInstructionLoaderIncludeCycleAndDepth(t *testing.T) {
	project := t.TempDir()
	mustWriteInstruction(t, filepath.Join(project, "ONECODE.md"), "@include a.md")
	mustWriteInstruction(t, filepath.Join(project, "a.md"), "a\n@include b.md")
	mustWriteInstruction(t, filepath.Join(project, "b.md"), "b\n@include a.md\n@include c.md")
	mustWriteInstruction(t, filepath.Join(project, "c.md"), "c\n@include d.md")
	mustWriteInstruction(t, filepath.Join(project, "d.md"), "d\n@include e.md")
	mustWriteInstruction(t, filepath.Join(project, "e.md"), "e\n@include f.md")
	mustWriteInstruction(t, filepath.Join(project, "f.md"), "too deep")

	got := (InstructionLoader{ProjectRoot: project}).Load()
	if strings.Contains(got.Content, "too deep") {
		t.Fatalf("expected include beyond depth limit to be skipped: %q", got.Content)
	}
	if len(got.Warnings) < 2 {
		t.Fatalf("expected cycle and depth warnings, got %+v", got.Warnings)
	}
}

func TestInstructionLoaderRejectsEscapeAndContinues(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteInstruction(t, filepath.Join(parent, "outside.md"), "outside secret")
	mustWriteInstruction(t, filepath.Join(project, "ONECODE.md"), "keep\n@include ../outside.md\nstill keep")

	got := (InstructionLoader{ProjectRoot: project}).Load()
	if strings.Contains(got.Content, "outside secret") || !strings.Contains(got.Content, "still keep") {
		t.Fatalf("escape handling returned unexpected content: %q", got.Content)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0].Message, "超出允许目录") {
		t.Fatalf("expected escape warning, got %+v", got.Warnings)
	}
}

func TestInstructionLoaderRejectsSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.md")
	mustWriteInstruction(t, outside, "outside secret")
	link := filepath.Join(project, "linked.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation is not available: %v", err)
	}
	mustWriteInstruction(t, filepath.Join(project, "ONECODE.md"), "@include linked.md")

	got := (InstructionLoader{ProjectRoot: project}).Load()
	if strings.Contains(got.Content, "outside secret") || len(got.Warnings) == 0 {
		t.Fatalf("expected symlink escape to be rejected: %+v", got)
	}
}

func TestInstructionLoaderWarnsForMalformedAndMissingInclude(t *testing.T) {
	project := t.TempDir()
	mustWriteInstruction(t, filepath.Join(project, "ONECODE.md"), "@include\n@include missing.md\nvalid")

	got := (InstructionLoader{ProjectRoot: project}).Load()
	if !strings.Contains(got.Content, "valid") || len(got.Warnings) != 2 {
		t.Fatalf("expected valid content and two warnings, got %+v", got)
	}
	if got.Warnings[0].Line != 1 || got.Warnings[1].Line != 2 {
		t.Fatalf("unexpected warning lines: %+v", got.Warnings)
	}
}

func mustWriteInstruction(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

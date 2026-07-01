package agent

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestBuildRequestContext(t *testing.T) {
	ctx := buildRequestContext(context.Background(), RunOptions{
		Mode:             ModePlan,
		ReminderInterval: 7,
	}, 3)

	if ctx.Mode != ModePlan.String() {
		t.Fatalf("Mode = %q, want %q", ctx.Mode, ModePlan.String())
	}
	if ctx.Iteration != 3 {
		t.Fatalf("Iteration = %d, want 3", ctx.Iteration)
	}
	if ctx.CWD == "" {
		t.Fatal("expected CWD")
	}
	if ctx.OS != runtime.GOOS {
		t.Fatalf("OS = %q, want %q", ctx.OS, runtime.GOOS)
	}
	if ctx.Arch != runtime.GOARCH {
		t.Fatalf("Arch = %q, want %q", ctx.Arch, runtime.GOARCH)
	}
	if ctx.Shell == "" {
		t.Fatal("expected Shell")
	}
	if ctx.Now.IsZero() {
		t.Fatal("expected Now")
	}
	if ctx.ReminderInterval != 7 {
		t.Fatalf("ReminderInterval = %d, want 7", ctx.ReminderInterval)
	}
}

func TestLimitGitStatus(t *testing.T) {
	var builder strings.Builder
	for i := 0; i < gitStatusMaxLines+5; i++ {
		builder.WriteString(fmt.Sprintf(" M file_%02d.go\n", i))
	}

	status := limitGitStatus(builder.String())
	lines := strings.Split(status, "\n")
	if len(lines) != gitStatusMaxLines+1 {
		t.Fatalf("expected %d lines including truncation marker, got %d", gitStatusMaxLines+1, len(lines))
	}
	if lines[len(lines)-1] != "... truncated" {
		t.Fatalf("expected truncation marker, got %q", lines[len(lines)-1])
	}
	if strings.Contains(status, "file_24.go") {
		t.Fatalf("expected lines beyond limit to be omitted, got:\n%s", status)
	}
}

func TestLimitGitStatusEmpty(t *testing.T) {
	if got := limitGitStatus(" \n\t "); got != "" {
		t.Fatalf("expected empty status, got %q", got)
	}
}

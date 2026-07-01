package prompt

import (
	"strings"
	"testing"
	"time"
)

func TestBuildPayloadIncludesEnvironmentReminder(t *testing.T) {
	runtime, err := NewRuntime(BuildOptions{})
	if err != nil {
		t.Fatalf("NewRuntime returned error: %v", err)
	}

	payload := runtime.BuildPayload(RequestContext{
		Mode:      "execute",
		Iteration: 3,
		CWD:       `E:\src\go\OneCode Coding Agent`,
		OS:        "windows",
		Arch:      "amd64",
		Shell:     "cmd",
		GitStatus: "## feature/prompt-runtime\n M src/internal/prompt/reminder.go",
		Now:       time.Date(2026, 6, 30, 8, 0, 0, 0, time.UTC),
	})

	if payload.StableSystem == "" {
		t.Fatal("expected stable system prompt")
	}
	if len(payload.Reminders) != 1 {
		t.Fatalf("expected 1 reminder, got %d", len(payload.Reminders))
	}
	reminder := payload.Reminders[0]
	if reminder.Kind != ReminderEnvironment {
		t.Fatalf("expected environment reminder, got %q", reminder.Kind)
	}
	for _, want := range []string{
		"<system-reminder>",
		"Working directory: E:\\src\\go\\OneCode Coding Agent",
		"OS: windows",
		"Arch: amd64",
		"Shell: cmd",
		"Date: 2026-06-30",
		"Mode: execute",
		"Iteration: 3",
		"Git status (--porcelain=v1 -b):",
		"## feature/prompt-runtime",
		" M src/internal/prompt/reminder.go",
	} {
		if !strings.Contains(reminder.Content, want) {
			t.Fatalf("expected environment reminder to contain %q, got:\n%s", want, reminder.Content)
		}
	}
}

func TestBuildPayloadKeepsDynamicContextOutOfStableSystem(t *testing.T) {
	runtime, err := NewRuntime(BuildOptions{})
	if err != nil {
		t.Fatalf("NewRuntime returned error: %v", err)
	}
	payload := runtime.BuildPayload(RequestContext{
		Mode:      "plan",
		Iteration: 1,
		CWD:       "dynamic-workspace",
		OS:        "windows",
		Now:       time.Date(2026, 6, 30, 8, 0, 0, 0, time.UTC),
	})

	for _, forbidden := range []string{"dynamic-workspace", "2026-06-30", "Iteration: 1", "Git status"} {
		if strings.Contains(payload.StableSystem, forbidden) {
			t.Fatalf("stable prompt should not contain %q:\n%s", forbidden, payload.StableSystem)
		}
	}
}

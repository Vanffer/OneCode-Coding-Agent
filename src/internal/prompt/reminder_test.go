package prompt

import (
	"strings"
	"testing"
	"time"
)

func TestPlanReminderCadence(t *testing.T) {
	full := []int{1, 6, 11}
	for _, iteration := range full {
		if !ShouldInjectFullPlanReminder(iteration, 5) {
			t.Fatalf("expected full reminder at iteration %d", iteration)
		}
	}

	compact := []int{2, 3, 4, 5, 7, 10}
	for _, iteration := range compact {
		if ShouldInjectFullPlanReminder(iteration, 5) {
			t.Fatalf("expected compact reminder at iteration %d", iteration)
		}
	}
}

func TestBuildPayloadPlanReminderCadence(t *testing.T) {
	runtime, err := NewRuntime(BuildOptions{})
	if err != nil {
		t.Fatalf("NewRuntime returned error: %v", err)
	}
	now := time.Date(2026, 6, 30, 8, 0, 0, 0, time.UTC)

	first := runtime.BuildPayload(RequestContext{Mode: "plan", Iteration: 1, Now: now})
	if len(first.Reminders) != 2 {
		t.Fatalf("expected environment and plan reminders, got %d", len(first.Reminders))
	}
	if !strings.Contains(first.Reminders[1].Content, "You are currently in Plan Mode") {
		t.Fatalf("expected full plan reminder, got:\n%s", first.Reminders[1].Content)
	}

	second := runtime.BuildPayload(RequestContext{Mode: "plan", Iteration: 2, Now: now})
	if len(second.Reminders) != 2 {
		t.Fatalf("expected environment and plan reminders, got %d", len(second.Reminders))
	}
	if strings.Contains(second.Reminders[1].Content, "Wait for the user to run /do") {
		t.Fatalf("expected compact plan reminder, got:\n%s", second.Reminders[1].Content)
	}
	if !strings.Contains(second.Reminders[1].Content, "Still in Plan Mode") {
		t.Fatalf("expected compact plan reminder, got:\n%s", second.Reminders[1].Content)
	}
}

func TestExecuteModeSkipsPlanReminder(t *testing.T) {
	runtime, err := NewRuntime(BuildOptions{})
	if err != nil {
		t.Fatalf("NewRuntime returned error: %v", err)
	}
	payload := runtime.BuildPayload(RequestContext{
		Mode:      "execute",
		Iteration: 1,
		Now:       time.Date(2026, 6, 30, 8, 0, 0, 0, time.UTC),
	})
	if len(payload.Reminders) != 1 {
		t.Fatalf("expected only environment reminder, got %d", len(payload.Reminders))
	}
	if payload.Reminders[0].Kind != ReminderEnvironment {
		t.Fatalf("expected environment reminder, got %q", payload.Reminders[0].Kind)
	}
}

func TestResumeGapOnlyAppearsOnFirstIteration(t *testing.T) {
	runtime, err := NewRuntime(BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := RequestContext{
		Mode: "execute",
		Now:  time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC),
		Session: SessionPromptContext{
			Instructions: "rules",
			MemoryIndex:  "memory",
			ResumeGap:    "resumed after a long gap",
		},
	}
	ctx.Iteration = 1
	first := runtime.BuildPayload(ctx)
	ctx.Iteration = 2
	second := runtime.BuildPayload(ctx)
	if !hasReminder(first.Reminders, ReminderResumeGap) {
		t.Fatal("expected resume gap on first iteration")
	}
	if hasReminder(second.Reminders, ReminderResumeGap) {
		t.Fatal("resume gap must not repeat after first iteration")
	}
	if !hasReminder(second.Reminders, ReminderInstructions) || !hasReminder(second.Reminders, ReminderMemoryIndex) {
		t.Fatal("instructions and memory should remain on later iterations")
	}
}

func hasReminder(reminders []Reminder, kind ReminderKind) bool {
	for _, reminder := range reminders {
		if reminder.Kind == kind {
			return true
		}
	}
	return false
}

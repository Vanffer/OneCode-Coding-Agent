package prompt

import (
	"fmt"
	"strings"
	"time"
)

// ReminderKind identifies the role of a dynamic system reminder.
type ReminderKind string

const (
	ReminderEnvironment ReminderKind = "environment"
	ReminderPlanMode    ReminderKind = "plan_mode"
)

// Reminder is dynamic system-level prompt content for a single model request.
type Reminder struct {
	Kind    ReminderKind
	Content string
}

// ShouldInjectFullPlanReminder reports whether this iteration needs the full
// Plan Mode reminder. Iterations are 1-based.
func ShouldInjectFullPlanReminder(iteration int, interval int) bool {
	if iteration <= 1 {
		return true
	}
	interval = normalizeReminderInterval(interval)
	return (iteration-1)%interval == 0
}

func buildEnvironmentReminder(ctx RequestContext) Reminder {
	now := ctx.Now
	if now.IsZero() {
		now = time.Now()
	}
	var builder strings.Builder
	builder.WriteString("<system-reminder>\n")
	builder.WriteString("Environment:\n")
	builder.WriteString(fmt.Sprintf("- Working directory: %s\n", emptyDefault(ctx.CWD, "unknown")))
	builder.WriteString(fmt.Sprintf("- OS: %s\n", emptyDefault(ctx.OS, "unknown")))
	builder.WriteString(fmt.Sprintf("- Arch: %s\n", emptyDefault(ctx.Arch, "unknown")))
	builder.WriteString(fmt.Sprintf("- Shell: %s\n", emptyDefault(ctx.Shell, "unknown")))
	builder.WriteString(fmt.Sprintf("- Date: %s\n", now.Format("2006-01-02")))
	builder.WriteString(fmt.Sprintf("- Mode: %s\n", emptyDefault(ctx.Mode, "execute")))
	builder.WriteString(fmt.Sprintf("- Iteration: %d\n", ctx.Iteration))
	if gitStatus := strings.TrimSpace(ctx.GitStatus); gitStatus != "" {
		builder.WriteString("- Git status (--porcelain=v1 -b):\n")
		for _, line := range strings.Split(normalizeNewlines(gitStatus), "\n") {
			builder.WriteString("  ")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	builder.WriteString("</system-reminder>")
	content := builder.String()
	return Reminder{Kind: ReminderEnvironment, Content: content}
}

func buildPlanModeReminder(ctx RequestContext) Reminder {
	if ShouldInjectFullPlanReminder(ctx.Iteration, ctx.ReminderInterval) {
		return Reminder{
			Kind: ReminderPlanMode,
			Content: `<system-reminder>
You are currently in Plan Mode.
Use only read-only inspection tools such as read_file, glob, and grep.
Do not modify files, edit files, create files, or run side-effect commands.
Your goal is to analyze the task and produce a concrete implementation plan.
Wait for the user to run /do before performing changes.
</system-reminder>`,
		}
	}

	return Reminder{
		Kind:    ReminderPlanMode,
		Content: `<system-reminder>Still in Plan Mode: read-only inspection only.</system-reminder>`,
	}
}

func emptyDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func normalizeNewlines(value string) string {
	return strings.ReplaceAll(value, "\r\n", "\n")
}

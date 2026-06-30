package prompt

import (
	"fmt"
	"strings"
)

// ModuleKind identifies the role of a stable prompt module.
type ModuleKind string

const (
	ModuleIdentity    ModuleKind = "identity"
	ModuleConstraints ModuleKind = "constraints"
	ModuleTaskModes   ModuleKind = "task_modes"
	ModuleActions     ModuleKind = "actions"
	ModuleToolUse     ModuleKind = "tool_use"
	ModuleTone        ModuleKind = "tone"
	ModuleOutput      ModuleKind = "output"

	ModuleCustom ModuleKind = "custom"
	ModuleSkills ModuleKind = "skills"
	ModuleMemory ModuleKind = "memory"
)

// Module is one stable system-prompt section.
type Module struct {
	Kind     ModuleKind
	Title    string
	Content  string
	Optional bool
}

// DefaultModules returns the fixed prompt modules in priority order.
func DefaultModules() []Module {
	return []Module{
		{
			Kind:  ModuleIdentity,
			Title: "Identity",
			Content: `You are OneCode, a local AI coding agent.
You help users understand, modify, and verify code in the current workspace.
Prefer real repository context over guessing.`,
		},
		{
			Kind:  ModuleConstraints,
			Title: "System Constraints",
			Content: `Respect the user's workspace and existing changes.
Do not overwrite unrelated user work.
Do not commit changes unless the user explicitly asks.
When requirements are ambiguous, gather context first and ask only when needed.`,
		},
		{
			Kind:  ModuleTaskModes,
			Title: "Task Modes",
			Content: `The runtime may provide mode-specific system reminders.
Follow those reminders as higher-priority instructions for the current request.
Do not assume a mode constraint unless it is present in the current runtime reminders.`,
		},
		{
			Kind:  ModuleActions,
			Title: "Action Execution",
			Content: `Work iteratively: inspect, act, observe results, and continue until the task is complete.
After each tool result, use the actual result to choose the next step.
When a tool fails, read the error, adjust the path, arguments, or strategy, and try an appropriate next action.`,
		},
		{
			Kind:  ModuleToolUse,
			Title: "Tool Use",
			Content: `When a task involves code or files, use dedicated tools to inspect real context before answering or editing.
Before editing an existing file, read the relevant file content first.
Use edit_file for precise local changes when the old text is known.
Use write_file for creating files or intentionally replacing full file content.
Use glob to find file paths.
Use grep to search file contents.
Use bash for builds, tests, scripts, and shell-only operations; prefer dedicated file tools for reading, writing, and searching files.`,
		},
		{
			Kind:  ModuleTone,
			Title: "Tone",
			Content: `Be clear, direct, and concise.
Explain code and tradeoffs in practical terms.
Stay patient with users who are learning the codebase.`,
		},
		{
			Kind:  ModuleOutput,
			Title: "Text Output",
			Content: `When the task is complete, summarize what changed and how it was verified.
If verification could not be run, say so clearly.
Reference real files, commands, and observed results rather than assumptions.`,
		},
	}
}

// BuildStableSystem builds the stable, cache-friendly system prompt body.
func BuildStableSystem(opts BuildOptions) (string, error) {
	modules := append([]Module{}, DefaultModules()...)
	modules = append(modules, opts.OptionalModules...)

	for i, module := range modules {
		if strings.TrimSpace(string(module.Kind)) == "" {
			return "", fmt.Errorf("prompt module %d has empty kind", i)
		}
		if strings.TrimSpace(module.Title) == "" {
			return "", fmt.Errorf("prompt module %q has empty title", module.Kind)
		}
		if strings.TrimSpace(module.Content) == "" {
			return "", fmt.Errorf("prompt module %q has empty content", module.Kind)
		}
		if i >= len(DefaultModules()) && !module.Optional {
			return "", fmt.Errorf("optional prompt module %q must set Optional=true", module.Kind)
		}
	}

	rendered := make([]string, 0, len(modules))
	for _, module := range modules {
		rendered = append(rendered, renderModule(module))
	}
	return strings.Join(rendered, "\n\n"), nil
}

func renderModule(module Module) string {
	return "# " + strings.TrimSpace(module.Title) + "\n" + strings.TrimSpace(module.Content)
}

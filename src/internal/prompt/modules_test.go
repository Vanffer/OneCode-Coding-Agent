package prompt

import (
	"strings"
	"testing"
)

func TestDefaultModules(t *testing.T) {
	modules := DefaultModules()
	want := []ModuleKind{
		ModuleIdentity,
		ModuleConstraints,
		ModuleTaskModes,
		ModuleActions,
		ModuleToolUse,
		ModuleTone,
		ModuleOutput,
	}
	if len(modules) != len(want) {
		t.Fatalf("expected %d modules, got %d", len(want), len(modules))
	}
	for i, kind := range want {
		if modules[i].Kind != kind {
			t.Fatalf("module %d: expected %q, got %q", i, kind, modules[i].Kind)
		}
		if modules[i].Optional {
			t.Fatalf("module %q should not be optional", modules[i].Kind)
		}
	}
}

func TestBuildStableSystem(t *testing.T) {
	stable, err := BuildStableSystem(BuildOptions{
		OptionalModules: []Module{
			{Kind: ModuleCustom, Title: "Custom", Content: "Custom instruction.", Optional: true},
		},
	})
	if err != nil {
		t.Fatalf("BuildStableSystem returned error: %v", err)
	}

	expectedOrder := []string{
		"# Identity",
		"# System Constraints",
		"# Task Modes",
		"# Action Execution",
		"# Tool Use",
		"# Tone",
		"# Text Output",
		"# Custom",
	}
	last := -1
	for _, marker := range expectedOrder {
		idx := strings.Index(stable, marker)
		if idx < 0 {
			t.Fatalf("missing marker %q in stable prompt:\n%s", marker, stable)
		}
		if idx <= last {
			t.Fatalf("marker %q appeared out of order", marker)
		}
		last = idx
	}
	if !strings.Contains(stable, "\n\n# System Constraints") {
		t.Fatalf("expected blank line between modules, got:\n%s", stable)
	}
}

func TestBuildStableSystemRejectsInvalidOptionalModule(t *testing.T) {
	_, err := BuildStableSystem(BuildOptions{
		OptionalModules: []Module{
			{Kind: ModuleCustom, Title: "Custom", Content: "Custom instruction."},
		},
	})
	if err == nil {
		t.Fatal("expected invalid optional module error")
	}
}

func TestStableSystemExcludesDynamicContext(t *testing.T) {
	stable, err := BuildStableSystem(BuildOptions{})
	if err != nil {
		t.Fatalf("BuildStableSystem returned error: %v", err)
	}
	for _, forbidden := range []string{
		"Working directory:",
		"Iteration:",
		"2026-06-30",
		"Mode:",
	} {
		if strings.Contains(stable, forbidden) {
			t.Fatalf("stable prompt should not contain dynamic value %q:\n%s", forbidden, stable)
		}
	}
}

package tools

import (
	"context"
	"testing"
	"time"
)

type registryTestTool struct {
	name string
}

func (t registryTestTool) Name() string { return t.name }

func (t registryTestTool) Description() string { return "test tool" }

func (t registryTestTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t registryTestTool) Timeout() time.Duration { return time.Second }

func (t registryTestTool) Execute(context.Context, map[string]interface{}) Result {
	return Result{Content: "ok"}
}

func TestRegistryDefaultSafety(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&ReadFileTool{})
	registry.Register(&GlobTool{})
	registry.Register(&GrepTool{})
	registry.Register(&WriteFileTool{})
	registry.Register(&EditFileTool{})
	registry.Register(&BashTool{})

	tests := []struct {
		name string
		want Safety
	}{
		{"read_file", SafetyReadOnly},
		{"glob", SafetyReadOnly},
		{"grep", SafetyReadOnly},
		{"write_file", SafetySideEffect},
		{"edit_file", SafetySideEffect},
		{"bash", SafetySideEffect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := registry.Safety(tt.name)
			if !ok {
				t.Fatalf("expected tool %q to be registered", tt.name)
			}
			if got != tt.want {
				t.Fatalf("expected safety %v, got %v", tt.want, got)
			}
		})
	}
}

func TestRegistryDefinitionsBySafety(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&ReadFileTool{})
	registry.Register(&WriteFileTool{})
	registry.Register(&GlobTool{})
	registry.Register(&BashTool{})
	registry.Register(&GrepTool{})

	defs := registry.ToToolDefinitionsBySafety(map[Safety]bool{
		SafetyReadOnly: true,
	})
	got := make([]string, 0, len(defs))
	for _, def := range defs {
		got = append(got, def.Name)
	}

	want := []string{"read_file", "glob", "grep"}
	if len(got) != len(want) {
		t.Fatalf("expected %d definitions, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("definition %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestRegistryRegisterWithSafety(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterWithSafety(registryTestTool{name: "custom_read"}, SafetyReadOnly)

	safety, ok := registry.Safety("custom_read")
	if !ok {
		t.Fatal("expected custom tool to be registered")
	}
	if safety != SafetyReadOnly {
		t.Fatalf("expected read-only safety, got %v", safety)
	}
}

func TestRegistryUnknownSafety(t *testing.T) {
	registry := NewRegistry()

	safety, ok := registry.Safety("missing")
	if ok {
		t.Fatal("expected missing tool lookup to fail")
	}
	if safety != SafetySideEffect {
		t.Fatalf("expected conservative side-effect safety, got %v", safety)
	}
}

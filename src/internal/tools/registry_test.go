package tools

import (
	"context"
	"strings"
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

func TestBuiltInToolDescriptionsReinforcePromptRules(t *testing.T) {
	tests := []struct {
		tool Tool
		want []string
	}{
		{&ReadFileTool{}, []string{"读取真实上下文", "编辑已有文件前必须先读取"}},
		{&EditFileTool{}, []string{"编辑已有文件前必须先用 read_file", "工具失败时阅读错误"}},
		{&WriteFileTool{}, []string{"write_file 有文件副作用", "覆盖已有文件前必须先用 read_file"}},
		{&GlobTool{}, []string{"查找文件路径时优先使用 glob"}},
		{&GrepTool{}, []string{"搜索文件内容时优先使用 grep", "glob 参数限制"}},
		{&BashTool{}, []string{"bash 可能有副作用", "优先使用专用工具"}},
	}

	for _, tt := range tests {
		t.Run(tt.tool.Name(), func(t *testing.T) {
			description := tt.tool.Description()
			for _, want := range tt.want {
				if !strings.Contains(description, want) {
					t.Fatalf("description for %s should contain %q, got:\n%s", tt.tool.Name(), want, description)
				}
			}
		})
	}
}

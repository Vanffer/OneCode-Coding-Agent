package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"onecode/internal/config"
	"onecode/internal/tools"
)

type fakeToolCaller struct {
	name string
	args map[string]interface{}
	res  CallToolResult
	err  error
}

func (c *fakeToolCaller) CallTool(_ context.Context, name string, args map[string]interface{}) (CallToolResult, error) {
	c.name = name
	c.args = args
	return c.res, c.err
}

func TestRemoteToolName(t *testing.T) {
	name, err := RemoteToolName("github", "search_issues")
	if err != nil {
		t.Fatalf("RemoteToolName returned error: %v", err)
	}
	if name != "github.search_issues" {
		t.Fatalf("expected github.search_issues, got %s", name)
	}
}

func TestRemoteToolNameSanitize(t *testing.T) {
	name, err := RemoteToolName("my server", "search/issues")
	if err != nil {
		t.Fatalf("RemoteToolName returned error: %v", err)
	}
	if name != "my_server.search_issues" {
		t.Fatalf("unexpected sanitized name: %s", name)
	}
	if _, err := RemoteToolName("!!!", "???"); err == nil {
		t.Fatal("expected empty sanitized name error")
	}
}

func TestRemoteToolDefaultSchema(t *testing.T) {
	tool, err := NewRemoteTool("server", MCPTool{Name: "echo"}, &fakeToolCaller{})
	if err != nil {
		t.Fatalf("NewRemoteTool returned error: %v", err)
	}
	if tool.Schema()["type"] != "object" {
		t.Fatalf("expected object schema, got %+v", tool.Schema())
	}
}

func TestMCPToolDefinitions(t *testing.T) {
	caller := &fakeToolCaller{}
	remote, err := NewRemoteTool("github", MCPTool{
		Name:        "search",
		Description: "Search issues",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string"},
			},
		},
	}, caller)
	if err != nil {
		t.Fatalf("NewRemoteTool returned error: %v", err)
	}
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(remote, tools.SafetyReadOnly)
	defs := registry.ToToolDefinitions()
	if len(defs) != 1 {
		t.Fatalf("expected one definition, got %d", len(defs))
	}
	if defs[0].Name != "github.search" || defs[0].Description != "Search issues" {
		t.Fatalf("unexpected definition: %+v", defs[0])
	}
	if defs[0].Schema["type"] != "object" {
		t.Fatalf("expected schema to be preserved, got %+v", defs[0].Schema)
	}
}

func TestRemoteToolTextResult(t *testing.T) {
	caller := &fakeToolCaller{res: CallToolResult{Content: []MCPContent{{Type: "text", Text: "hello"}}}}
	remote, err := NewRemoteTool("server", MCPTool{Name: "echo"}, caller)
	if err != nil {
		t.Fatalf("NewRemoteTool returned error: %v", err)
	}
	result := remote.Execute(context.Background(), map[string]interface{}{"message": "hi"})
	if result.IsError || result.Content != "hello" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if caller.name != "echo" {
		t.Fatalf("expected remote tool name echo, got %s", caller.name)
	}
}

func TestRemoteToolNonTextResult(t *testing.T) {
	caller := &fakeToolCaller{res: CallToolResult{Content: []MCPContent{{Type: "image"}}}}
	remote, err := NewRemoteTool("server", MCPTool{Name: "render"}, caller)
	if err != nil {
		t.Fatalf("NewRemoteTool returned error: %v", err)
	}
	result := remote.Execute(context.Background(), nil)
	if !strings.Contains(result.Content, "image") {
		t.Fatalf("expected non-text content type in result, got %+v", result)
	}
}

func TestRemoteToolMCPErrorResult(t *testing.T) {
	caller := &fakeToolCaller{res: CallToolResult{Content: []MCPContent{{Type: "text", Text: "bad"}}, IsError: true}}
	remote, err := NewRemoteTool("server", MCPTool{Name: "fail"}, caller)
	if err != nil {
		t.Fatalf("NewRemoteTool returned error: %v", err)
	}
	result := remote.Execute(context.Background(), nil)
	if !result.IsError || result.Content != "bad" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRemoteToolCallError(t *testing.T) {
	caller := &fakeToolCaller{err: errors.New("transport down")}
	remote, err := NewRemoteTool("server", MCPTool{Name: "fail"}, caller)
	if err != nil {
		t.Fatalf("NewRemoteTool returned error: %v", err)
	}
	result := remote.Execute(context.Background(), nil)
	if !result.IsError || !strings.Contains(result.Content, "transport down") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestSafetyForMCPToolDefault(t *testing.T) {
	if got := SafetyForMCPTool(config.MCPConfig{}, "search"); got != tools.SafetySideEffect {
		t.Fatalf("expected side-effect safety, got %v", got)
	}
}

func TestSafetyForMCPToolServerReadOnly(t *testing.T) {
	if got := SafetyForMCPTool(config.MCPConfig{ReadOnly: true}, "search"); got != tools.SafetyReadOnly {
		t.Fatalf("expected read-only safety, got %v", got)
	}
}

func TestSafetyForMCPToolOverride(t *testing.T) {
	readOnly := true
	sideEffect := false
	cfg := config.MCPConfig{
		ReadOnly: true,
		Tools: map[string]config.MCPToolConfig{
			"search": {ReadOnly: &readOnly},
			"write":  {ReadOnly: &sideEffect},
		},
	}
	if got := SafetyForMCPTool(cfg, "search"); got != tools.SafetyReadOnly {
		t.Fatalf("expected read-only override, got %v", got)
	}
	if got := SafetyForMCPTool(cfg, "write"); got != tools.SafetySideEffect {
		t.Fatalf("expected side-effect override, got %v", got)
	}
}

func TestSafetyIgnoresAnnotations(t *testing.T) {
	cfg := config.MCPConfig{}
	tool := MCPTool{Name: "search", Annotations: map[string]interface{}{"readOnlyHint": true}}
	if got := SafetyForMCPTool(cfg, tool.Name); got != tools.SafetySideEffect {
		t.Fatalf("expected annotations to be ignored, got %v", got)
	}
}

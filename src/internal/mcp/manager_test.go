package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"onecode/internal/config"
	"onecode/internal/tools"
)

type managerStaticTool struct {
	name string
}

func (t managerStaticTool) Name() string        { return t.name }
func (t managerStaticTool) Description() string { return "static" }
func (t managerStaticTool) Schema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t managerStaticTool) Timeout() time.Duration { return time.Second }
func (t managerStaticTool) Execute(context.Context, map[string]interface{}) tools.Result {
	return tools.Result{Content: "ok"}
}

func managerTransport(tools []MCPTool) *fakeTransport {
	transport := newFakeTransport()
	transport.responses["initialize"] = InitializeResult{ProtocolVersion: ProtocolVersion}
	transport.responses["tools/list"] = ListToolsResult{Tools: tools}
	return transport
}

func TestManagerDiscoverMultipleServers(t *testing.T) {
	manager := NewManager(map[string]config.MCPConfig{
		"a": {Type: "stdio", Command: "a"},
		"b": {Type: "stdio", Command: "b"},
	})
	manager.SetTransportFactory(func(name string, cfg config.MCPConfig) (Transport, error) {
		return managerTransport([]MCPTool{{Name: "echo_" + name}}), nil
	})

	result := manager.Discover(context.Background())
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", result.Errors)
	}
	if len(result.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(result.Sessions))
	}
}

func TestManagerDiscoverStartFailureIsolation(t *testing.T) {
	manager := NewManager(map[string]config.MCPConfig{
		"bad":  {Type: "stdio", Command: "bad"},
		"good": {Type: "stdio", Command: "good"},
	})
	manager.SetTransportFactory(func(name string, cfg config.MCPConfig) (Transport, error) {
		transport := managerTransport([]MCPTool{{Name: "echo"}})
		if name == "bad" {
			transport.errs["start"] = context.Canceled
		}
		return transport, nil
	})

	result := manager.Discover(context.Background())
	if len(result.Sessions) != 1 || result.Sessions[0].Name != "good" {
		t.Fatalf("expected good session only, got %+v", result.Sessions)
	}
	if len(result.Errors) != 1 || result.Errors[0].Stage != StageStart {
		t.Fatalf("expected one start error, got %+v", result.Errors)
	}
}

func TestManagerDiscoverInitializeFailureIsolation(t *testing.T) {
	manager := NewManager(map[string]config.MCPConfig{
		"bad":  {Type: "stdio", Command: "bad"},
		"good": {Type: "stdio", Command: "good"},
	})
	manager.SetTransportFactory(func(name string, cfg config.MCPConfig) (Transport, error) {
		transport := managerTransport([]MCPTool{{Name: "echo"}})
		if name == "bad" {
			transport.errs["initialize"] = context.Canceled
		}
		return transport, nil
	})

	result := manager.Discover(context.Background())
	if len(result.Sessions) != 1 || result.Sessions[0].Name != "good" {
		t.Fatalf("expected good session only, got %+v", result.Sessions)
	}
	if len(result.Errors) != 1 || result.Errors[0].Stage != StageInitialize {
		t.Fatalf("expected one initialize error, got %+v", result.Errors)
	}
}

func TestManagerDiscoverListToolsFailureIsolation(t *testing.T) {
	manager := NewManager(map[string]config.MCPConfig{
		"bad":  {Type: "stdio", Command: "bad"},
		"good": {Type: "stdio", Command: "good"},
	})
	manager.SetTransportFactory(func(name string, cfg config.MCPConfig) (Transport, error) {
		transport := managerTransport([]MCPTool{{Name: "echo"}})
		if name == "bad" {
			transport.errs["tools/list"] = context.Canceled
		}
		return transport, nil
	})

	result := manager.Discover(context.Background())
	if len(result.Sessions) != 1 || result.Sessions[0].Name != "good" {
		t.Fatalf("expected good session only, got %+v", result.Sessions)
	}
	if len(result.Errors) != 1 || result.Errors[0].Stage != StageListTools {
		t.Fatalf("expected one list tools error, got %+v", result.Errors)
	}
}

func TestManagerDiscoverEmptyTools(t *testing.T) {
	manager := NewManager(map[string]config.MCPConfig{
		"empty": {Type: "stdio", Command: "empty"},
	})
	manager.SetTransportFactory(func(name string, cfg config.MCPConfig) (Transport, error) {
		return managerTransport(nil), nil
	})

	result := manager.Discover(context.Background())
	if len(result.Errors) != 0 || len(result.Sessions) != 1 {
		t.Fatalf("expected empty server to succeed, got sessions=%+v errors=%+v", result.Sessions, result.Errors)
	}
	if len(result.Sessions[0].Tools) != 0 {
		t.Fatalf("expected no tools, got %+v", result.Sessions[0].Tools)
	}
}

func TestManagerRegisterTools(t *testing.T) {
	manager := NewManager(map[string]config.MCPConfig{
		"github": {
			Type:     "stdio",
			Command:  "github-mcp",
			ReadOnly: true,
		},
	})
	manager.SetTransportFactory(func(name string, cfg config.MCPConfig) (Transport, error) {
		return managerTransport([]MCPTool{{
			Name:        "search",
			Description: "Search issues",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		}}), nil
	})
	result := manager.Discover(context.Background())
	registry := tools.NewRegistry()
	manager.RegisterTools(registry, &result)

	if len(result.Errors) != 0 {
		t.Fatalf("expected no register errors, got %+v", result.Errors)
	}
	if !registry.Has("github.search") {
		t.Fatal("expected github.search to be registered")
	}
	if safety, ok := registry.Safety("github.search"); !ok || safety != tools.SafetyReadOnly {
		t.Fatalf("expected read-only safety, got %v ok=%v", safety, ok)
	}
}

func TestManagerRegisterToolConflict(t *testing.T) {
	manager := NewManager(map[string]config.MCPConfig{})
	result := DiscoverResult{Sessions: []*ServerSession{{
		Name:   "github",
		Config: config.MCPConfig{},
		Client: NewClient("github", managerTransport(nil)),
		Tools:  []MCPTool{{Name: "search"}},
	}}}
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(managerStaticTool{name: "github.search"}, tools.SafetyReadOnly)

	manager.RegisterTools(registry, &result)
	if len(result.Errors) != 1 || result.Errors[0].Stage != StageRegister {
		t.Fatalf("expected register conflict error, got %+v", result.Errors)
	}
}

func TestManagerRegisterInvalidSchema(t *testing.T) {
	manager := NewManager(map[string]config.MCPConfig{})
	result := DiscoverResult{Sessions: []*ServerSession{{
		Name:   "github",
		Config: config.MCPConfig{},
		Client: NewClient("github", managerTransport(nil)),
		Tools:  []MCPTool{{Name: "bad", InputSchema: map[string]interface{}{"type": "string"}}},
	}}}
	registry := tools.NewRegistry()

	manager.RegisterTools(registry, &result)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Err.Error(), "schema") {
		t.Fatalf("expected schema register error, got %+v", result.Errors)
	}
}

func TestManagerErrorsDoNotLeakSecrets(t *testing.T) {
	manager := NewManager(map[string]config.MCPConfig{
		"remote": {
			Type: "http",
			URL:  "https://example.invalid/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer secret-token",
			},
		},
	})
	manager.SetTransportFactory(func(name string, cfg config.MCPConfig) (Transport, error) {
		return nil, context.Canceled
	})

	result := manager.Discover(context.Background())
	if len(result.Errors) != 1 {
		t.Fatalf("expected one error, got %+v", result.Errors)
	}
	if strings.Contains(result.Errors[0].Err.Error(), "secret-token") {
		t.Fatalf("error leaked secret: %v", result.Errors[0].Err)
	}
}

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeTransport struct {
	started   bool
	closed    bool
	calls     []fakeTransportCall
	responses map[string]interface{}
	errs      map[string]error
}

type fakeTransportCall struct {
	method string
	params interface{}
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		responses: map[string]interface{}{},
		errs:      map[string]error{},
	}
}

func (t *fakeTransport) Start(context.Context) error {
	t.started = true
	return t.errs["start"]
}

func (t *fakeTransport) Request(_ context.Context, method string, params interface{}, result interface{}) error {
	t.calls = append(t.calls, fakeTransportCall{method: method, params: params})
	if err := t.errs[method]; err != nil {
		return err
	}
	response := t.responses[method]
	if result == nil || response == nil {
		return nil
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

func (t *fakeTransport) Close() error {
	t.closed = true
	return t.errs["close"]
}

func TestClientInitialize(t *testing.T) {
	transport := newFakeTransport()
	transport.responses["initialize"] = InitializeResult{ProtocolVersion: ProtocolVersion}
	client := NewClient("test", transport)

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}
	if !transport.started {
		t.Fatal("expected transport to start")
	}
	if len(transport.calls) != 1 || transport.calls[0].method != "initialize" {
		t.Fatalf("expected initialize call, got %+v", transport.calls)
	}
}

func TestClientListTools(t *testing.T) {
	transport := newFakeTransport()
	transport.responses["tools/list"] = ListToolsResult{Tools: []MCPTool{{Name: "echo"}}}
	client := NewClient("test", transport)

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestClientCallTool(t *testing.T) {
	transport := newFakeTransport()
	transport.responses["tools/call"] = CallToolResult{
		Content: []MCPContent{{Type: "text", Text: "ok"}},
	}
	client := NewClient("test", transport)

	result, err := client.CallTool(context.Background(), "echo", map[string]interface{}{"message": "hi"})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := transport.calls[0].params.(CallToolParams); got.Name != "echo" {
		t.Fatalf("expected call params for echo, got %+v", got)
	}
}

func TestClientProtocolError(t *testing.T) {
	transport := newFakeTransport()
	transport.errs["tools/list"] = errors.New("protocol failed")
	client := NewClient("test", transport)

	if _, err := client.ListTools(context.Background()); err == nil {
		t.Fatal("expected protocol error")
	}
}

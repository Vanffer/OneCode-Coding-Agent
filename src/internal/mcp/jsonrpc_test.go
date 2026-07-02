package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONRPCRequest(t *testing.T) {
	req := NewRequest(7, "tools/list", map[string]interface{}{"cursor": "next"})
	if req.JSONRPC != "2.0" || req.ID != 7 || req.Method != "tools/list" {
		t.Fatalf("unexpected request: %+v", req)
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"jsonrpc":"2.0"`) {
		t.Fatalf("request should include jsonrpc version, got %s", data)
	}
}

func TestJSONRPCResponseID(t *testing.T) {
	resp, err := DecodeResponse([]byte(`{"jsonrpc":"2.0","id":42,"result":{"ok":true}}`))
	if err != nil {
		t.Fatalf("DecodeResponse returned error: %v", err)
	}
	if resp.ID != 42 {
		t.Fatalf("expected id 42, got %d", resp.ID)
	}
}

func TestJSONRPCErrorResponse(t *testing.T) {
	resp, err := DecodeResponse([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"missing"}}`))
	if err != nil {
		t.Fatalf("DecodeResponse returned error: %v", err)
	}
	err = ErrorFromResponse(resp)
	if err == nil {
		t.Fatal("expected JSON-RPC error")
	}
	if !strings.Contains(err.Error(), "-32601") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestJSONRPCDecodeResult(t *testing.T) {
	resp, err := DecodeResponse([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"echo"}]}}`))
	if err != nil {
		t.Fatalf("DecodeResponse returned error: %v", err)
	}
	var result ListToolsResult
	if err := DecodeResult(resp, &result); err != nil {
		t.Fatalf("DecodeResult returned error: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "echo" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestJSONRPCInvalidJSON(t *testing.T) {
	if _, err := DecodeResponse([]byte(`{bad`)); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

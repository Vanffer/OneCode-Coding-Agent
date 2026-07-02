package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPTransportRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Method != "tools/list" {
			t.Fatalf("expected tools/list, got %s", req.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]interface{}{
				"tools": []map[string]interface{}{{"name": "echo"}},
			},
		})
	}))
	defer server.Close()

	transport := NewHTTPTransport("test", server.URL, nil, server.Client())
	var result ListToolsResult
	if err := transport.Request(context.Background(), "tools/list", nil, &result); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "echo" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestHTTPTransportHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("expected Authorization header, got %q", got)
		}
		var req JSONRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  map[string]interface{}{},
		})
	}))
	defer server.Close()

	transport := NewHTTPTransport("test", server.URL, map[string]string{
		"Authorization": "Bearer token",
	}, server.Client())
	if err := transport.Request(context.Background(), "initialize", nil, &InitializeResult{}); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
}

func TestHTTPTransportResponseIDMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      999,
			"result":  map[string]interface{}{},
		})
	}))
	defer server.Close()

	transport := NewHTTPTransport("test", server.URL, nil, server.Client())
	err := transport.Request(context.Background(), "initialize", nil, &InitializeResult{})
	if err == nil || !strings.Contains(err.Error(), "id 不匹配") {
		t.Fatalf("expected id mismatch error, got %v", err)
	}
}

func TestHTTPTransportStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadGateway)
	}))
	defer server.Close()

	transport := NewHTTPTransport("test", server.URL, nil, server.Client())
	err := transport.Request(context.Background(), "initialize", nil, &InitializeResult{})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestHTTPTransportJSONRPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req JSONRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error": map[string]interface{}{
				"code":    -32601,
				"message": "missing method",
			},
		})
	}))
	defer server.Close()

	transport := NewHTTPTransport("test", server.URL, nil, server.Client())
	err := transport.Request(context.Background(), "missing", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "missing method") {
		t.Fatalf("expected JSON-RPC error, got %v", err)
	}
}

func TestHTTPTransportCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := NewHTTPTransport("test", server.URL, nil, server.Client())
	err := transport.Request(ctx, "initialize", nil, &InitializeResult{})
	if err == nil {
		t.Fatal("expected cancel error")
	}
}

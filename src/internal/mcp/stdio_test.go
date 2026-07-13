package mcp

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func stdioTestTransport(env map[string]string) *StdioTransport {
	return NewStdioTransport("test", "go", []string{"run", "./testdata/stdio_server.go"}, env)
}

func TestStdioTransport(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not available")
	}
	transport := stdioTestTransport(nil)
	if err := transport.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer transport.Close()

	var initResult InitializeResult
	if err := transport.Request(context.Background(), "initialize", nil, &initResult); err != nil {
		t.Fatalf("initialize returned error: %v", err)
	}
	if initResult.ServerInfo.Name != "test-stdio" {
		t.Fatalf("unexpected init result: %+v", initResult)
	}

	var listResult ListToolsResult
	if err := transport.Request(context.Background(), "tools/list", nil, &listResult); err != nil {
		t.Fatalf("tools/list returned error: %v", err)
	}
	if len(listResult.Tools) != 1 || listResult.Tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", listResult)
	}
}

func TestStdioTransportEnvAndArgs(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not available")
	}
	transport := stdioTestTransport(map[string]string{"ONECODE_TEST_ENV": "ok"})
	if err := transport.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer transport.Close()
}

func TestStdioTransportConcurrentRequests(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not available")
	}
	transport := stdioTestTransport(nil)
	if err := transport.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer transport.Close()

	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			var result ListToolsResult
			errs <- transport.Request(context.Background(), "tools/list", nil, &result)
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent request returned error: %v", err)
		}
	}
}

func TestStdioTransportCancel(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not available")
	}
	transport := stdioTestTransport(map[string]string{"MCP_DELAY": "1"})
	if err := transport.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer transport.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var result ListToolsResult
	err := transport.Request(ctx, "tools/list", nil, &result)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
}

func TestStdioTransportProcessExit(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not available")
	}
	transport := stdioTestTransport(map[string]string{"MCP_EXIT_ON_START": "1"})
	if err := transport.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer transport.Close()

	var result ListToolsResult
	err := transport.Request(context.Background(), "tools/list", nil, &result)
	if err == nil {
		t.Fatal("expected process exit error")
	}
}

func TestStdioTransportClose(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not available")
	}
	transport := stdioTestTransport(nil)
	if err := transport.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	var result ListToolsResult
	if err := transport.Request(context.Background(), "tools/list", nil, &result); err == nil {
		t.Fatal("expected closed transport error")
	}
}

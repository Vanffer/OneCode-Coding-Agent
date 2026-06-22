package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFile(t *testing.T) {
	tool := &ReadFileTool{}

	// 测试读取不存在的文件
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": "nonexistent.txt",
	})
	if !result.IsError {
		t.Error("Expected error for nonexistent file")
	}

	// 创建临时文件
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("line1\nline2\nline3"), 0644)

	// 测试读取存在的文件
	result = tool.Execute(context.Background(), map[string]interface{}{
		"path": tmpFile,
	})
	if result.IsError {
		t.Errorf("Unexpected error: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("Expected content")
	}
	t.Logf("Read result:\n%s", result.Content)
}

func TestWriteFile(t *testing.T) {
	tool := &WriteFileTool{}
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	// 测试写入文件
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":    tmpFile,
		"content": "hello world",
	})
	if result.IsError {
		t.Errorf("Unexpected error: %s", result.Content)
	}

	// 验证文件内容
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("Expected 'hello world', got '%s'", string(data))
	}
}

func TestEditFile(t *testing.T) {
	tool := &EditFileTool{}
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("hello world"), 0644)

	// 测试唯一匹配替换
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":       tmpFile,
		"old_string": "hello",
		"new_string": "goodbye",
	})
	if result.IsError {
		t.Errorf("Unexpected error: %s", result.Content)
	}

	// 验证替换结果
	data, _ := os.ReadFile(tmpFile)
	if string(data) != "goodbye world" {
		t.Errorf("Expected 'goodbye world', got '%s'", string(data))
	}

	// 测试无匹配
	result = tool.Execute(context.Background(), map[string]interface{}{
		"path":       tmpFile,
		"old_string": "nonexistent",
		"new_string": "new",
	})
	if !result.IsError {
		t.Error("Expected error for no match")
	}
}

func TestBash(t *testing.T) {
	tool := &BashTool{}

	// 测试执行命令
	result := tool.Execute(context.Background(), map[string]interface{}{
		"command": "echo hello",
	})
	if result.IsError {
		t.Errorf("Unexpected error: %s", result.Content)
	}
	t.Logf("Bash result:\n%s", result.Content)
}

func TestGlob(t *testing.T) {
	tool := &GlobTool{}

	// 测试 glob 搜索
	result := tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "*.go",
		"path":    ".",
	})
	if result.IsError {
		t.Errorf("Unexpected error: %s", result.Content)
	}
	t.Logf("Glob result:\n%s", result.Content)

	// 测试 **/*.go 模式
	result2 := tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "**/*.go",
		"path":    ".",
	})
	if result2.IsError {
		t.Errorf("Unexpected error: %s", result2.Content)
	}
	t.Logf("Glob result (**/*.go):\n%s", result2.Content)
}

func TestGrep(t *testing.T) {
	tool := &GrepTool{}

	// 测试 grep 搜索
	result := tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "func Test",
		"path":    ".",
		"glob":    "*_test.go",
	})
	if result.IsError {
		t.Errorf("Unexpected error: %s", result.Content)
	}
	t.Logf("Grep result:\n%s", result.Content)
}

func TestRegistry(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&ReadFileTool{})
	registry.Register(&WriteFileTool{})
	registry.Register(&EditFileTool{})
	registry.Register(&BashTool{})
	registry.Register(&GlobTool{})
	registry.Register(&GrepTool{})

	// 测试 List
	tools := registry.List()
	if len(tools) != 6 {
		t.Errorf("Expected 6 tools, got %d", len(tools))
	}

	// 测试 Get
	tool, ok := registry.Get("read_file")
	if !ok {
		t.Error("Expected to find read_file")
	}
	if tool.Name() != "read_file" {
		t.Errorf("Expected read_file, got %s", tool.Name())
	}

	// 测试 Get 不存在的工具
	_, ok = registry.Get("nonexistent")
	if ok {
		t.Error("Expected not to find nonexistent")
	}

	// 测试 ToToolDefinitions
	defs := registry.ToToolDefinitions()
	if len(defs) != 6 {
		t.Errorf("Expected 6 definitions, got %d", len(defs))
	}
}

func TestRegistryExecute(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&ReadFileTool{})
	registry.Register(&BashTool{})

	// 测试执行存在的工具
	result := registry.Execute(context.Background(), "read_file", map[string]interface{}{
		"path": "nonexistent.txt",
	})
	if !result.IsError {
		t.Error("Expected error for nonexistent file")
	}

	// 测试执行不存在的工具
	result = registry.Execute(context.Background(), "nonexistent", map[string]interface{}{})
	if !result.IsError {
		t.Error("Expected error for nonexistent tool")
	}

	// 测试执行 bash
	result = registry.Execute(context.Background(), "bash", map[string]interface{}{
		"command": "echo hello",
	})
	if result.IsError {
		t.Errorf("Unexpected error: %s", result.Content)
	}
}

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"onecode/internal/tools/searchutil"
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

func TestEditFileExactReplacement(t *testing.T) {
	tool := &EditFileTool{}

	tests := []struct {
		name      string
		initial   string
		args      map[string]interface{}
		expected  string
		wantError bool
	}{
		{
			name:    "duplicate old string requires replace all",
			initial: "a\nb\na",
			args: map[string]interface{}{
				"old_string": "a",
				"new_string": "x",
			},
			wantError: true,
		},
		{
			name:    "replace all duplicates",
			initial: "a\nb\na",
			args: map[string]interface{}{
				"old_string":  "a",
				"new_string":  "x",
				"replace_all": true,
			},
			expected: "x\nb\nx",
		},
		{
			name:    "empty new string deletes exact text",
			initial: "hello world",
			args: map[string]interface{}{
				"old_string": " world",
				"new_string": "",
			},
			expected: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.txt")
			if err := os.WriteFile(tmpFile, []byte(tt.initial), 0644); err != nil {
				t.Fatalf("Failed to write test file: %v", err)
			}

			tt.args["path"] = tmpFile
			result := tool.Execute(context.Background(), tt.args)
			if tt.wantError {
				if !result.IsError {
					t.Fatal("Expected error")
				}
				return
			}
			if result.IsError {
				t.Fatalf("Unexpected error: %s", result.Content)
			}

			data, err := os.ReadFile(tmpFile)
			if err != nil {
				t.Fatalf("Failed to read test file: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(data))
			}
		})
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

func TestGlobMatchPattern(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		path     string
		expected bool
	}{
		{
			name:     "bare filename pattern matches nested file",
			pattern:  "*.go",
			path:     "src/internal/tools/glob.go",
			expected: true,
		},
		{
			name:     "double star matches root file",
			pattern:  "**/*.go",
			path:     "main.go",
			expected: true,
		},
		{
			name:     "double star matches nested file",
			pattern:  "**/*.go",
			path:     "src/internal/tools/glob.go",
			expected: true,
		},
		{
			name:     "path prefix restricts matches",
			pattern:  "src/**/*.go",
			path:     "src/internal/tools/glob.go",
			expected: true,
		},
		{
			name:     "path prefix rejects other directories",
			pattern:  "src/**/*.go",
			path:     "cmd/onecode/main.go",
			expected: false,
		},
		{
			name:     "single star does not cross directories",
			pattern:  "src/*.go",
			path:     "src/internal/tools/glob.go",
			expected: false,
		},
		{
			name:     "single star matches one path segment",
			pattern:  "src/*.go",
			path:     "src/main.go",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, err := searchutil.MatchPattern(tt.pattern, tt.path)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if matched != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, matched)
			}
		})
	}
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

func TestGrepGlobUsesRelativePath(t *testing.T) {
	tool := &GrepTool{}
	tmpDir := t.TempDir()

	srcFile := filepath.Join(tmpDir, "src", "internal", "match.go")
	cmdFile := filepath.Join(tmpDir, "cmd", "main.go")
	if err := os.MkdirAll(filepath.Dir(srcFile), 0755); err != nil {
		t.Fatalf("Failed to create src dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cmdFile), 0755); err != nil {
		t.Fatalf("Failed to create cmd dir: %v", err)
	}
	if err := os.WriteFile(srcFile, []byte("package internal\nfunc target() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to write src file: %v", err)
	}
	if err := os.WriteFile(cmdFile, []byte("package main\nfunc target() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to write cmd file: %v", err)
	}

	result := tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "target",
		"path":    tmpDir,
		"glob":    "src/**/*.go",
	})
	if result.IsError {
		t.Fatalf("Unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "src") || strings.Contains(result.Content, "cmd") {
		t.Fatalf("Expected only src match, got:\n%s", result.Content)
	}
}

func TestGrepSkipsBinaryFiles(t *testing.T) {
	tool := &GrepTool{}
	tmpDir := t.TempDir()
	binFile := filepath.Join(tmpDir, "data.bin")
	if err := os.WriteFile(binFile, []byte{'t', 'a', 'r', 'g', 'e', 't', 0, 'x'}, 0644); err != nil {
		t.Fatalf("Failed to write binary file: %v", err)
	}

	result := tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "target",
		"path":    tmpDir,
	})
	if result.IsError {
		t.Fatalf("Unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "没有找到匹配的内容") {
		t.Fatalf("Expected no content match, got:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "binary=1") {
		t.Fatalf("Expected binary skip summary, got:\n%s", result.Content)
	}
}

func TestGrepRejectsInvalidGlob(t *testing.T) {
	tool := &GrepTool{}
	result := tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "target",
		"path":    ".",
		"glob":    "[",
	})
	if !result.IsError {
		t.Fatal("Expected invalid glob error")
	}
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

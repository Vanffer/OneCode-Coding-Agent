package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// readFileMaxLines 读文件最大行数
	readFileMaxLines = 2000
	// readFileMaxSize 读文件最大字节数
	readFileMaxSize = 256 * 1024 // 256KB
)

// ReadFileTool 实现 read_file 工具
type ReadFileTool struct{}

// readFileArgs read_file 的参数
type readFileArgs struct {
	Path string `json:"path"`
}

func (t *ReadFileTool) Name() string { return "read_file" }

func (t *ReadFileTool) Description() string {
	return "读取文件内容，返回带行号的文本。文件不存在或不可读时返回错误。"
}

func (t *ReadFileTool) Timeout() time.Duration { return 30 * time.Second }

func (t *ReadFileTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "要读取的文件路径",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]interface{}) Result {
	// 解析参数
	var a readFileArgs
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return Result{Content: "参数序列化失败: " + err.Error(), IsError: true}
	}
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return Result{Content: "参数解析失败: " + err.Error(), IsError: true}
	}

	if a.Path == "" {
		return Result{Content: "path 参数不能为空", IsError: true}
	}

	// 检查是否是目录
	info, err := os.Stat(a.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Content: fmt.Sprintf("文件不存在: %s", a.Path), IsError: true}
		}
		return Result{Content: fmt.Sprintf("无法访问文件: %s", err.Error()), IsError: true}
	}
	if info.IsDir() {
		return Result{Content: fmt.Sprintf("路径是目录，不是文件: %s", a.Path), IsError: true}
	}

	// 读取文件
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return Result{Content: fmt.Sprintf("读取文件失败: %s", err.Error()), IsError: true}
	}

	// 检查大小
	if len(data) > readFileMaxSize {
		data = data[:readFileMaxSize]
	}

	// 按行分割并加行号
	lines := strings.Split(string(data), "\n")
	truncated := false
	if len(lines) > readFileMaxLines {
		lines = lines[:readFileMaxLines]
		truncated = true
	}

	var result strings.Builder
	for i, line := range lines {
		result.WriteString(fmt.Sprintf("%d\t%s\n", i+1, line))
	}

	if truncated {
		result.WriteString(fmt.Sprintf("\n[truncated: showing first %d lines]\n", readFileMaxLines))
	}

	return Result{Content: result.String(), IsError: false}
}

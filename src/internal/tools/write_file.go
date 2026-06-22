package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// WriteFileTool 实现 write_file 工具
type WriteFileTool struct{}

// writeFileArgs write_file 的参数
type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *WriteFileTool) Name() string { return "write_file" }

func (t *WriteFileTool) Description() string {
	return "创建或覆盖文件，自动创建父目录。返回写入的字节数。"
}

func (t *WriteFileTool) Timeout() time.Duration { return 30 * time.Second }

func (t *WriteFileTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "要写入的文件路径",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "要写入的内容",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]interface{}) Result {
	// 解析参数
	var a writeFileArgs
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

	// 创建父目录
	dir := filepath.Dir(a.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return Result{Content: fmt.Sprintf("创建父目录失败: %s", err.Error()), IsError: true}
	}

	// 写入文件
	data := []byte(a.Content)
	if err := os.WriteFile(a.Path, data, 0644); err != nil {
		return Result{Content: fmt.Sprintf("写入文件失败: %s", err.Error()), IsError: true}
	}

	return Result{
		Content: fmt.Sprintf("写入成功: %s (%d bytes)", a.Path, len(data)),
		IsError: false,
	}
}

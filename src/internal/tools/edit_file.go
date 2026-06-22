package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// EditFileTool 实现 edit_file 工具
type EditFileTool struct{}

// editFileArgs edit_file 的参数
type editFileArgs struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

func (t *EditFileTool) Name() string { return "edit_file" }

func (t *EditFileTool) Description() string {
	return "基于原文唯一匹配替换文件内容。old_string 必须在文件中恰好出现一次，否则返回错误。"
}

func (t *EditFileTool) Timeout() time.Duration { return 30 * time.Second }

func (t *EditFileTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "要编辑的文件路径",
			},
			"old_string": map[string]interface{}{
				"type":        "string",
				"description": "要替换的原文（必须在文件中恰好出现一次）",
			},
			"new_string": map[string]interface{}{
				"type":        "string",
				"description": "替换后的新文本",
			},
		},
		"required": []string{"path", "old_string", "new_string"},
	}
}

func (t *EditFileTool) Execute(ctx context.Context, args map[string]interface{}) Result {
	// 解析参数
	var a editFileArgs
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
	if a.OldString == "" {
		return Result{Content: "old_string 参数不能为空", IsError: true}
	}

	// 读取文件
	data, err := os.ReadFile(a.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Content: fmt.Sprintf("文件不存在: %s", a.Path), IsError: true}
		}
		return Result{Content: fmt.Sprintf("读取文件失败: %s", err.Error()), IsError: true}
	}

	content := string(data)

	// 检查匹配次数
	count := strings.Count(content, a.OldString)
	if count == 0 {
		return Result{
			Content: "未找到匹配: old_string 在文件中不存在",
			IsError: true,
		}
	}
	if count > 1 {
		return Result{
			Content: fmt.Sprintf("匹配到 %d 处，old_string 不唯一，请提供更长上下文", count),
			IsError: true,
		}
	}

	// 唯一匹配，执行替换
	newContent := strings.Replace(content, a.OldString, a.NewString, 1)

	// 写回文件
	if err := os.WriteFile(a.Path, []byte(newContent), 0644); err != nil {
		return Result{Content: fmt.Sprintf("写回文件失败: %s", err.Error()), IsError: true}
	}

	return Result{Content: "替换成功", IsError: false}
}

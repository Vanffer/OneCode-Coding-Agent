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
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (t *EditFileTool) Name() string { return "edit_file" }

func (t *EditFileTool) Description() string {
	return `基于精确文本匹配编辑文件内容。
old_string 必须和文件中的内容完全一致；默认必须恰好出现一次。
如需替换所有匹配位置，设置 replace_all=true。

适用场景：修改函数、更新配置、插入或删除一段明确文本。
不适用：创建新文件（用 write_file）、按行号编辑、正则替换、模糊匹配。
关键规则：编辑已有文件前必须先用 read_file 读取相关内容；工具失败时阅读错误并调整 old_string 或策略。
配合建议：先用 read_file 确认要替换的原文，old_string 尽量包含足够上下文以保证唯一。`
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
				"description": "要替换的原文，必须和文件内容完全匹配；默认必须恰好出现一次",
			},
			"new_string": map[string]interface{}{
				"type":        "string",
				"description": "替换后的新文本；可以为空字符串，用于删除 old_string",
			},
			"replace_all": map[string]interface{}{
				"type":        "boolean",
				"description": "是否替换所有匹配位置；默认 false，要求 old_string 唯一",
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
	count := strings.Count(content, a.OldString)
	if count == 0 {
		return Result{Content: "未找到匹配: old_string 在文件中不存在", IsError: true}
	}
	if count > 1 && !a.ReplaceAll {
		return Result{
			Content: fmt.Sprintf("匹配到 %d 处，old_string 不唯一；请提供更长上下文，或设置 replace_all=true", count),
			IsError: true,
		}
	}

	replaceCount := 1
	if a.ReplaceAll {
		replaceCount = -1
	}
	newContent := strings.Replace(content, a.OldString, a.NewString, replaceCount)
	if err := os.WriteFile(a.Path, []byte(newContent), 0644); err != nil {
		return Result{Content: fmt.Sprintf("写回文件失败: %s", err.Error()), IsError: true}
	}

	if a.ReplaceAll {
		return Result{Content: fmt.Sprintf("替换成功（共 %d 处）", count), IsError: false}
	}
	return Result{Content: "替换成功", IsError: false}
}

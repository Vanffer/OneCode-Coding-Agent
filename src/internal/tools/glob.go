package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"onecode/internal/tools/searchutil"
)

const (
	// globMaxResults glob 最大返回结果数
	globMaxResults = 100
)

// GlobTool 实现 glob 工具
type GlobTool struct{}

// globArgs glob 的参数
type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

func (t *GlobTool) Name() string { return "glob" }

func (t *GlobTool) Description() string {
	return `按 glob 模式查找文件路径，返回匹配的文件列表。
适用场景：查找特定类型的文件、定位代码文件、列出目录结构。
不适用：搜索文件内容（用 grep）、读取文件内容（用 read_file）。
返回格式：每行一个文件路径，最多100个结果。
关键规则：需要查找文件路径时优先使用 glob，不要猜路径或用 bash 代替。
配合建议：用 glob 找到文件后，用 read_file 读取内容。`
}

func (t *GlobTool) Timeout() time.Duration { return 30 * time.Second }

func (t *GlobTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "glob 模式，如 **/*.go",
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "搜索的根目录（可选，默认为当前工作目录）",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GlobTool) Execute(ctx context.Context, args map[string]interface{}) Result {
	// 解析参数
	var a globArgs
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return Result{Content: "参数序列化失败: " + err.Error(), IsError: true}
	}
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return Result{Content: "参数解析失败: " + err.Error(), IsError: true}
	}

	if a.Pattern == "" {
		return Result{Content: "pattern 参数不能为空", IsError: true}
	}
	if err := searchutil.ValidateGlobPattern(a.Pattern); err != nil {
		return Result{Content: fmt.Sprintf("glob 模式无效: %s", err.Error()), IsError: true}
	}

	// 默认路径为当前目录
	root := searchutil.NormalizeSearchRoot(a.Path)
	if err := searchutil.ValidateSearchRoot(root); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}

	// 收集匹配的文件
	var matches []string
	truncated := false

	_, err = searchutil.WalkSearchFiles(ctx, root, func(path, relPath string) error {
		// 匹配模式
		matched, err := searchutil.MatchPattern(a.Pattern, relPath)
		if err != nil {
			return nil // 跳过匹配错误
		}
		if matched {
			if len(matches) >= globMaxResults {
				truncated = true
				return filepath.SkipAll
			}
			matches = append(matches, path)
		}

		return nil
	})

	if err != nil && err != context.DeadlineExceeded {
		return Result{Content: fmt.Sprintf("搜索失败: %s", err.Error()), IsError: true}
	}

	if len(matches) == 0 {
		return Result{Content: "没有找到匹配的文件", IsError: false}
	}

	// 排序
	sort.Strings(matches)

	// 格式化输出
	var result strings.Builder
	for _, m := range matches {
		result.WriteString(m)
		result.WriteString("\n")
	}

	if truncated {
		result.WriteString(fmt.Sprintf("\n[showing first %d of more matches]\n", globMaxResults))
	}

	return Result{Content: result.String(), IsError: false}
}

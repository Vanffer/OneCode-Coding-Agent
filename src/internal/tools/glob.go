package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	return "按 glob 模式查找文件，返回匹配的文件路径列表。支持 ** 通配符。"
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

	// 默认路径为当前目录
	root := a.Path
	if root == "" {
		root = "."
	}

	// 检查根目录是否存在
	info, err := os.Stat(root)
	if err != nil {
		return Result{Content: fmt.Sprintf("路径不存在: %s", root), IsError: true}
	}
	if !info.IsDir() {
		return Result{Content: fmt.Sprintf("路径不是目录: %s", root), IsError: true}
	}

	// 收集匹配的文件
	var matches []string
	truncated := false

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过无法访问的文件
		}

		// 检查 context
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 匹配模式
		matched, err := matchPattern(a.Pattern, path)
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

// matchPattern 简化的 glob 匹配，支持 **
func matchPattern(pattern, path string) (bool, error) {
	// 使用 filepath.Match 基础匹配
	// 对于 ** 模式，需要特殊处理
	if strings.Contains(pattern, "**") {
		// 简化处理：将 ** 替换为任意路径
		parts := strings.Split(pattern, "**")
		if len(parts) == 2 {
			suffix := parts[1]

			// 检查路径是否匹配 suffix
			base := filepath.Base(path)
			// 移除前导分隔符（支持 / 和 \）
			suffix = strings.TrimPrefix(suffix, "/")
			suffix = strings.TrimPrefix(suffix, "\\")

			if suffix == "" {
				return true, nil
			}

			matched, err := filepath.Match(suffix, base)
			if err != nil {
				return false, err
			}
			return matched, nil
		}
	}

	// 基础匹配
	base := filepath.Base(path)
	return filepath.Match(pattern, base)
}

package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// grepMaxResults grep 最大返回结果数
	grepMaxResults = 100
	// grepMaxLineLength 单行最大长度
	grepMaxLineLength = 1024 * 1024 // 1MB
)

// GrepTool 实现 grep 工具
type GrepTool struct{}

// grepArgs grep 的参数
type grepArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Glob    string `json:"glob,omitempty"`
}

func (t *GrepTool) Name() string { return "grep" }

func (t *GrepTool) Description() string {
	return "在文件内容中搜索匹配的文本，返回 file:line:content 列表。支持正则表达式。"
}

func (t *GrepTool) Timeout() time.Duration { return 30 * time.Second }

func (t *GrepTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "搜索模式（RE2 正则表达式）",
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "搜索的根目录（可选，默认为当前工作目录）",
			},
			"glob": map[string]interface{}{
				"type":        "string",
				"description": "文件名过滤模式（可选，如 *.go）",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) Execute(ctx context.Context, args map[string]interface{}) Result {
	// 解析参数
	var a grepArgs
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

	// 编译正则
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return Result{Content: fmt.Sprintf("正则表达式无效: %s", err.Error()), IsError: true}
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

	// 收集匹配结果
	type match struct {
		file    string
		line    int
		content string
	}
	var matches []match
	truncated := false
	totalMatches := 0

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

		// 跳过目录
		if d.IsDir() {
			return nil
		}

		// 文件名过滤
		if a.Glob != "" {
			matched, err := filepath.Match(a.Glob, d.Name())
			if err != nil || !matched {
				return nil
			}
		}

		// 搜索文件内容
		file, err := os.Open(path)
		if err != nil {
			return nil // 跳过无法打开的文件
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, grepMaxLineLength), grepMaxLineLength)

		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()

			if re.MatchString(line) {
				totalMatches++
				if len(matches) < grepMaxResults {
					matches = append(matches, match{
						file:    path,
						line:    lineNum,
						content: line,
					})
				} else {
					truncated = true
				}
			}
		}

		if err := scanner.Err(); err != nil {
			return nil // 跳过扫描出错的文件
		}

		return nil
	})

	if err != nil && err != context.DeadlineExceeded {
		return Result{Content: fmt.Sprintf("搜索失败: %s", err.Error()), IsError: true}
	}

	if len(matches) == 0 {
		return Result{Content: "没有找到匹配的内容", IsError: false}
	}

	// 格式化输出
	var result strings.Builder
	for _, m := range matches {
		result.WriteString(fmt.Sprintf("%s:%d:%s\n", m.file, m.line, m.content))
	}

	if truncated {
		result.WriteString(fmt.Sprintf("\n[showing first %d of %d matches]\n", grepMaxResults, totalMatches))
	}

	return Result{Content: result.String(), IsError: false}
}

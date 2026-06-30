package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"onecode/internal/tools/searchutil"
)

const (
	// grepMaxResults grep 最大返回结果数
	grepMaxResults = 100
	// grepMaxLineLength 单行最大长度
	grepMaxLineLength = 1024 * 1024 // 1MB
	// grepMaxFileSize 单个文件最大扫描大小
	grepMaxFileSize = 10 * 1024 * 1024 // 10MB
	// grepBinaryProbeSize 二进制文件检测读取字节数
	grepBinaryProbeSize = 8 * 1024
)

// GrepTool 实现 grep 工具
type GrepTool struct{}

// grepArgs grep 的参数
type grepArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Glob    string `json:"glob,omitempty"`
}

type grepMatch struct {
	file    string
	line    int
	content string
}

type grepStats struct {
	skippedUnreadable int
	skippedBinary     int
	skippedLarge      int
	skippedScanError  int
}

func (t *GrepTool) Name() string { return "grep" }

func (t *GrepTool) Description() string {
	return `在文件内容中搜索匹配的文本，支持正则表达式。
适用场景：查找函数定义、搜索代码中的特定文本、定位错误信息。
不适用：查找文件路径（用 glob）、读取整个文件（用 read_file）。
返回格式：每行 "文件路径:行号:内容"，最多100个结果。
关键规则：需要搜索文件内容时优先使用 grep；可用 glob 参数限制文件范围。
配合建议：通常可以使用 grep 定位后，用 read_file 读取上下文。`
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
				"description": "文件路径过滤 glob（可选，如 **/*.go 或 src/**/*.go）",
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
	if a.Glob != "" {
		if err := searchutil.ValidateGlobPattern(a.Glob); err != nil {
			return Result{Content: fmt.Sprintf("glob 模式无效: %s", err.Error()), IsError: true}
		}
	}

	// 默认路径为当前目录
	root := searchutil.NormalizeSearchRoot(a.Path)
	if err := searchutil.ValidateSearchRoot(root); err != nil {
		return Result{Content: err.Error(), IsError: true}
	}

	var matches []grepMatch
	var stats grepStats
	truncated := false

	skippedUnreadable, err := searchutil.WalkSearchFiles(ctx, root, func(path, relPath string) error {
		if a.Glob != "" {
			matched, err := searchutil.MatchPattern(a.Glob, relPath)
			if err != nil || !matched {
				return nil
			}
		}

		stop, err := grepFile(ctx, path, re, &matches, &stats)
		if err != nil {
			return err
		}
		if stop {
			truncated = true
			return filepath.SkipAll
		}

		return nil
	})
	stats.skippedUnreadable += skippedUnreadable

	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		return Result{Content: fmt.Sprintf("搜索失败: %s", err.Error()), IsError: true}
	}

	if len(matches) == 0 {
		content := "没有找到匹配的内容"
		if summary := stats.summary(); summary != "" {
			content += "\n" + summary
		}
		return Result{Content: content, IsError: false}
	}

	// 格式化输出
	var result strings.Builder
	for _, m := range matches {
		result.WriteString(fmt.Sprintf("%s:%d:%s\n", m.file, m.line, m.content))
	}

	if truncated {
		result.WriteString(fmt.Sprintf("\n[showing first %d matches; search stopped early]\n", grepMaxResults))
	}
	if summary := stats.summary(); summary != "" {
		result.WriteString(summary)
		result.WriteString("\n")
	}

	return Result{Content: result.String(), IsError: false}
}

func grepFile(ctx context.Context, path string, re *regexp.Regexp, matches *[]grepMatch, stats *grepStats) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		stats.skippedUnreadable++
		return false, nil
	}
	if info.Size() > grepMaxFileSize {
		stats.skippedLarge++
		return false, nil
	}

	file, err := os.Open(path)
	if err != nil {
		stats.skippedUnreadable++
		return false, nil
	}
	defer file.Close()

	binary, err := isLikelyBinary(file)
	if err != nil {
		stats.skippedUnreadable++
		return false, nil
	}
	if binary {
		stats.skippedBinary++
		return false, nil
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, grepMaxLineLength), grepMaxLineLength)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum%128 == 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			default:
			}
		}

		line := scanner.Text()
		if !re.MatchString(line) {
			continue
		}

		*matches = append(*matches, grepMatch{
			file:    path,
			line:    lineNum,
			content: line,
		})
		if len(*matches) >= grepMaxResults {
			return true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		stats.skippedScanError++
		return false, nil
	}

	return false, nil
}

func isLikelyBinary(file *os.File) (bool, error) {
	buf := make([]byte, grepBinaryProbeSize)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	return bytes.IndexByte(buf[:n], 0) >= 0, nil
}

func (s grepStats) summary() string {
	if s.skippedUnreadable == 0 && s.skippedBinary == 0 && s.skippedLarge == 0 && s.skippedScanError == 0 {
		return ""
	}

	var parts []string
	if s.skippedUnreadable > 0 {
		parts = append(parts, fmt.Sprintf("unreadable=%d", s.skippedUnreadable))
	}
	if s.skippedBinary > 0 {
		parts = append(parts, fmt.Sprintf("binary=%d", s.skippedBinary))
	}
	if s.skippedLarge > 0 {
		parts = append(parts, fmt.Sprintf("large=%d", s.skippedLarge))
	}
	if s.skippedScanError > 0 {
		parts = append(parts, fmt.Sprintf("scan_error=%d", s.skippedScanError))
	}
	return "[skipped files: " + strings.Join(parts, ", ") + "]"
}

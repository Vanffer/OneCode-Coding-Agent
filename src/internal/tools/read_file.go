package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// readFileDefaultLimit 默认读取行数
	readFileDefaultLimit = 2000
	// readFileMaxLimit 最大读取行数
	readFileMaxLimit = 5000
	// readFileMaxLineLength 单行最大字节数
	readFileMaxLineLength = 1024 * 1024 // 1MB
)

// ReadFileTool 实现 read_file 工具
type ReadFileTool struct{}

// readFileArgs read_file 的参数
type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"` // 起始行（从0开始，默认0）
	Limit  int    `json:"limit,omitempty"`  // 读取行数（默认2000，最大5000）
}

func (t *ReadFileTool) Name() string { return "read_file" }

func (t *ReadFileTool) Description() string {
	return `读取文件内容，返回带行号的文本。
适用场景：查看代码、读取配置文件、检查文件内容。
不适用：搜索文件中的特定内容（用 grep）、查找文件路径（用 glob）。
返回格式：每行 "行号\t内容"，超过 limit 时截断并提示 offset。
关键规则：涉及代码或文件时，优先用 read_file 读取真实上下文；编辑已有文件前必须先读取相关内容。
配合建议：先用 glob 找到文件路径，再用 read_file 读取内容。`
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
			"offset": map[string]interface{}{
				"type":        "integer",
				"description": "起始行号（从0开始，默认0）",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "读取行数（默认2000，最大5000）",
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

	// 设置默认值
	if a.Limit <= 0 {
		a.Limit = readFileDefaultLimit
	}
	if a.Limit > readFileMaxLimit {
		a.Limit = readFileMaxLimit
	}
	if a.Offset < 0 {
		a.Offset = 0
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

	// 打开文件
	file, err := os.Open(a.Path)
	if err != nil {
		return Result{Content: fmt.Sprintf("打开文件失败: %s", err.Error()), IsError: true}
	}
	defer file.Close()

	// 逐行读取
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, readFileMaxLineLength), readFileMaxLineLength)

	// 跳过 offset 之前的行
	for i := 0; i < a.Offset; i++ {
		if !scanner.Scan() {
			break
		}
	}

	// 读取 limit 行
	var result strings.Builder
	lineNum := a.Offset
	count := 0
	truncated := false

	for count < a.Limit {
		if !scanner.Scan() {
			break
		}
		lineNum++
		count++
		result.WriteString(fmt.Sprintf("%d\t%s\n", lineNum, scanner.Text()))
	}

	// 检查是否还有更多内容
	if count == a.Limit && scanner.Scan() {
		truncated = true
	}

	if err := scanner.Err(); err != nil {
		return Result{Content: fmt.Sprintf("读取文件出错: %s", err.Error()), IsError: true}
	}

	if truncated {
		result.WriteString(fmt.Sprintf("\n[truncated: showing lines %d-%d, more content available with offset=%d]\n",
			a.Offset+1, lineNum, lineNum+1))
	}

	if count == 0 {
		return Result{Content: fmt.Sprintf("文件为空或 offset 超出范围: %s", a.Path), IsError: false}
	}

	return Result{Content: result.String(), IsError: false}
}

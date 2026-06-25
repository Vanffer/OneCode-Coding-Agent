package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	// bashMaxOutput 命令输出最大字符数
	bashMaxOutput = 30000
	// bashTimeout 命令执行超时
	bashTimeout = 5 * time.Minute
)

// BashTool 实现 bash 工具
type BashTool struct{}

// bashArgs bash 的参数
type bashArgs struct {
	Command string `json:"command"`
}

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Description() string {
	return `执行 shell 命令，返回 stdout、stderr 和退出码。
适用场景：运行构建命令、执行脚本、查看系统信息、安装依赖。
不适用：读取文件内容（用 read_file）、搜索文件内容（用 grep）。
返回格式：stdout + stderr 文本，超长截断；退出码非0表示失败。
配合建议：如有必要，先用 glob/grep 定位文件，再用 bash 执行相关命令。`
}

func (t *BashTool) Timeout() time.Duration { return bashTimeout }

func (t *BashTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "要执行的 shell 命令",
			},
		},
		"required": []string{"command"},
	}
}

func (t *BashTool) Execute(ctx context.Context, args map[string]interface{}) Result {
	// 解析参数
	var a bashArgs
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return Result{Content: "参数序列化失败: " + err.Error(), IsError: true}
	}
	if err := json.Unmarshal(argsJSON, &a); err != nil {
		return Result{Content: "参数解析失败: " + err.Error(), IsError: true}
	}

	if a.Command == "" {
		return Result{Content: "command 参数不能为空", IsError: true}
	}

	// 根据平台选择 shell
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", a.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", a.Command)
	}

	// 捕获 stdout 和 stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// 执行命令
	err = cmd.Run()

	// 格式化输出
	var result strings.Builder

	if stdout.Len() > 0 {
		output := stdout.String()
		if len(output) > bashMaxOutput {
			output = output[:bashMaxOutput] + "\n[truncated]"
		}
		result.WriteString(fmt.Sprintf("stdout:\n%s\n", output))
	}

	if stderr.Len() > 0 {
		output := stderr.String()
		if len(output) > bashMaxOutput {
			output = output[:bashMaxOutput] + "\n[truncated]"
		}
		result.WriteString(fmt.Sprintf("stderr:\n%s\n", output))
	}

	// 获取退出码
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			return Result{
				Content: fmt.Sprintf("命令超时 (%v)", bashTimeout),
				IsError: true,
			}
		} else {
			return Result{
				Content: fmt.Sprintf("命令执行失败: %s", err.Error()),
				IsError: true,
			}
		}
	}

	result.WriteString(fmt.Sprintf("exit_code: %d", exitCode))

	// 非零退出码不标 IsError，让模型自行判断
	return Result{Content: result.String(), IsError: false}
}

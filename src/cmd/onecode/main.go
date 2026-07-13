package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"onecode/internal/config"
	"onecode/internal/mcp"
	"onecode/internal/prompt"
	"onecode/internal/tools"
	"onecode/internal/tui"
)

const version = "0.1.0"

func main() {
	// 获取当前工作目录
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	// 加载配置
	cfg, err := config.LoadMerged(userConfigPath(), ".onecode/config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	// 打印启动横幅
	fmt.Println(prompt.RenderBanner(version, cwd))

	// 创建工具注册中心
	registry := tools.NewRegistry()
	registry.RegisterWithSafety(&tools.ReadFileTool{}, tools.SafetyReadOnly)
	registry.RegisterWithSafety(&tools.WriteFileTool{}, tools.SafetySideEffect)
	registry.RegisterWithSafety(&tools.EditFileTool{}, tools.SafetySideEffect)
	registry.RegisterWithSafety(&tools.BashTool{}, tools.SafetySideEffect)
	registry.RegisterWithSafety(&tools.GlobTool{}, tools.SafetyReadOnly)
	registry.RegisterWithSafety(&tools.GrepTool{}, tools.SafetyReadOnly)

	mcpManager := mcp.NewManager(cfg.MCPServers)
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	discoverResult := mcpManager.Discover(startupCtx)
	cancelStartup()
	mcpManager.RegisterTools(registry, &discoverResult)
	printMCPWarnings(discoverResult.Errors)
	defer mcpManager.Close()

	// 创建 TUI 模型
	model := tui.New(cfg.Providers, registry, cwd)

	// 启动 TUI
	p := tea.NewProgram(model, tea.WithOutput(os.Stderr))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func userConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".onecode", "config.yaml")
}

func printMCPWarnings(errors []mcp.ServerError) {
	for _, serverErr := range errors {
		if serverErr.Err == nil {
			continue
		}
		fmt.Fprintf(os.Stderr, "Warning: MCP server %s %s failed: %s\n", serverErr.Server, serverErr.Stage, serverErr.Err)
	}
}

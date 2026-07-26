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
	"onecode/internal/memory"
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

	// 组装项目指令、会话存档与自动记忆。三者故障彼此隔离。
	home, _ := os.UserHomeDir()
	userInstructionRoot := ""
	if home != "" {
		userInstructionRoot = filepath.Join(home, ".onecode")
	}
	instructionLoader := &memory.InstructionLoader{
		ProjectRoot: cwd,
		UserRoot:    userInstructionRoot,
	}
	instructions := instructionLoader.Load()
	printInstructionWarnings(instructions.Warnings)

	sessionStore := memory.NewSessionStore(cwd)
	go cleanupExpiredSessions(sessionStore)

	noteStore := memory.NewNoteStore(cwd, home, cfg.Memory.Enabled)
	memoryWorker := memory.NewWorker(noteStore)
	defer memoryWorker.Close()

	// 创建 TUI 模型
	model := tui.New(cfg.Providers, registry, cwd, tui.MemoryDependencies{
		InstructionLoader: instructionLoader,
		Instructions:      instructions,
		Sessions:          sessionStore,
		Notes:             noteStore,
		Worker:            memoryWorker,
	})

	// 启动 TUI
	p := tea.NewProgram(model, tea.WithOutput(os.Stderr))
	finalModel, err := p.Run()
	if final, ok := finalModel.(tui.Model); ok {
		if closeErr := final.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: close session failed: %s\n", closeErr)
		}
	}
	if err != nil {
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

func printInstructionWarnings(warnings []memory.LoadWarning) {
	for _, warning := range warnings {
		location := warning.Path
		if warning.Line > 0 {
			location = fmt.Sprintf("%s:%d", location, warning.Line)
		}
		fmt.Fprintf(os.Stderr, "Warning: instruction %s: %s\n", location, warning.Message)
	}
}

func cleanupExpiredSessions(store *memory.SessionStore) {
	if err := store.Cleanup(time.Now().Add(-30 * 24 * time.Hour)); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cleanup expired sessions failed: %s\n", err)
	}
}

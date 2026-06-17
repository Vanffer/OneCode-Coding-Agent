package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"onecode/internal/config"
	"onecode/internal/prompt"
	"onecode/internal/tui"
)

const version = "0.1.0"

func main() {
	// 加载配置
	cfg, err := config.Load(".onecode/config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	// 获取当前工作目录
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "unknown"
	}

	// 打印启动横幅
	fmt.Println(prompt.RenderBanner(version, cwd))

	// 创建 TUI 模型
	model := tui.New(cfg.Providers)

	// 启动 TUI
	p := tea.NewProgram(model, tea.WithOutput(os.Stderr))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

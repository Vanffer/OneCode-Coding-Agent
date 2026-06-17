package tui

import "charm.land/lipgloss/v2"

var (
	// 状态栏样式
	statusBarLeftStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("62")).
				Foreground(lipgloss.Color("230")).
				Padding(0, 1)

	statusBarMiddleStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("235")).
				Foreground(lipgloss.Color("252")).
				Padding(0, 1)

	statusBarRightStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("62")).
				Foreground(lipgloss.Color("230")).
				Padding(0, 1)

	// spinner 样式
	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205"))

	// 错误样式
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	// 提示符样式
	promptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Bold(true)
)

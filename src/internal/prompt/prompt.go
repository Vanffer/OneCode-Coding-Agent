package prompt

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed banner.txt
var dogAndText string

// ANSI 颜色代码（彩虹渐变）
var gradientColors = []string{
	"\033[38;5;196m", // 红
	"\033[38;5;208m", // 橙
	"\033[38;5;220m", // 黄
	"\033[38;5;46m",  // 绿
	"\033[38;5;51m",  // 青
	"\033[38;5;27m",  // 蓝
	"\033[38;5;129m", // 紫
	"\033[38;5;201m", // 粉
}

const reset = "\033[0m"

// gradientText 将文本应用彩虹渐变色
func gradientText(text string) string {
	var result strings.Builder
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			result.WriteString("\n")
			continue
		}
		colorIdx := 0
		for _, ch := range line {
			if ch == ' ' || ch == '\t' {
				result.WriteString(string(ch))
			} else {
				result.WriteString(gradientColors[colorIdx%len(gradientColors)])
				result.WriteString(string(ch))
				colorIdx++
			}
		}
		result.WriteString(reset + "\n")
	}

	return result.String()
}

// RenderBanner 渲染完整的启动横幅
// 小狗和 ONE CODE CODING CLI 应用彩虹渐变，版本号和工作目录保持原色
func RenderBanner(version, cwd string) string {
	// 渲染渐变色的小狗和文字
	banner := gradientText(dogAndText)

	// 渲染普通色的版本信息
	banner += fmt.Sprintf("  Version: %s\n", version)
	banner += fmt.Sprintf("  Working Directory: %s\n", cwd)

	return banner
}

package agent

import (
	"fmt"
	"sort"
	"strings"
)

func formatArgsPreview(args map[string]interface{}) string {
	if len(args) == 0 {
		return ""
	}

	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		val := fmt.Sprintf("%v", args[key])
		if len(val) > 50 {
			val = val[:50] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s: %s", key, val))
	}

	result := strings.Join(parts, ", ")
	return truncateResult(result, 100)
}

func truncateResult(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func stopReasonMessage(reason StopReason) string {
	switch reason {
	case StopModelDone:
		return "任务完成"
	case StopMaxIterations:
		return "已达到 Agent loop 迭代上限"
	case StopCancelled:
		return "任务已取消"
	case StopBadToolLimit:
		return "连续请求未知或禁用工具，已停止"
	case StopStreamError:
		return "模型流式请求出错，已停止"
	case StopToolError:
		return "工具执行出现不可继续的错误，已停止"
	default:
		return "Agent loop 已停止"
	}
}

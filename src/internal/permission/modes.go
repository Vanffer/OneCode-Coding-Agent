package permission

import (
	"fmt"
	"strings"

	"onecode/internal/tools"
)

var modeMatrix = map[Mode]map[tools.ToolCategory]Action{
	ModeStrict: {
		tools.CategoryRead:    ActionAsk,
		tools.CategoryWrite:   ActionAsk,
		tools.CategoryCommand: ActionAsk,
		tools.CategoryNetwork: ActionAsk,
		tools.CategoryMCP:     ActionAsk,
		tools.CategoryUnknown: ActionAsk,
	},
	ModeDefault: {
		tools.CategoryRead:    ActionAllow,
		tools.CategoryWrite:   ActionAsk,
		tools.CategoryCommand: ActionAsk,
		tools.CategoryNetwork: ActionAsk,
		tools.CategoryMCP:     ActionAsk,
		tools.CategoryUnknown: ActionAsk,
	},
	ModeAcceptEdits: {
		tools.CategoryRead:    ActionAllow,
		tools.CategoryWrite:   ActionAllow,
		tools.CategoryCommand: ActionAsk,
		tools.CategoryNetwork: ActionAsk,
		tools.CategoryMCP:     ActionAsk,
		tools.CategoryUnknown: ActionAsk,
	},
	ModePlan: {
		tools.CategoryRead:    ActionAllow,
		tools.CategoryWrite:   ActionDeny,
		tools.CategoryCommand: ActionDeny,
		tools.CategoryNetwork: ActionDeny,
		tools.CategoryMCP:     ActionDeny,
		tools.CategoryUnknown: ActionDeny,
	},
	ModeBypass: {
		tools.CategoryRead:    ActionAllow,
		tools.CategoryWrite:   ActionAllow,
		tools.CategoryCommand: ActionAllow,
		tools.CategoryNetwork: ActionAllow,
		tools.CategoryMCP:     ActionAllow,
		tools.CategoryUnknown: ActionAsk,
	},
}

func modeDecision(mode Mode, category tools.ToolCategory) Action {
	byCategory, ok := modeMatrix[mode]
	if !ok {
		return ActionAsk
	}
	action, ok := byCategory[category]
	if !ok {
		return ActionAsk
	}
	return action
}

func categoryForRequest(req Request) tools.ToolCategory {
	if req.Category != "" {
		return req.Category
	}
	tool := NormalizeToolName(req.Tool)
	switch tool {
	case "read_file", "glob", "grep":
		return tools.CategoryRead
	case "write_file", "edit_file":
		return tools.CategoryWrite
	case "bash":
		return tools.CategoryCommand
	case "webfetch", "web_fetch", "websearch", "web_search":
		return tools.CategoryNetwork
	}
	if strings.HasPrefix(tool, "mcp__") {
		return tools.CategoryMCP
	}
	if req.Safety == tools.SafetyReadOnly {
		return tools.CategoryRead
	}
	return tools.CategoryUnknown
}

func modeReason(mode Mode, category tools.ToolCategory, action Action) string {
	switch action {
	case ActionAllow:
		return fmt.Sprintf("%s 模式允许 %s 类工具", modeDisplayName(mode), categoryDisplayName(category))
	case ActionDeny:
		return fmt.Sprintf("%s 模式禁用 %s 类工具", modeDisplayName(mode), categoryDisplayName(category))
	default:
		return fmt.Sprintf("%s 模式需要确认 %s 类工具", modeDisplayName(mode), categoryDisplayName(category))
	}
}

func modeDisplayName(mode Mode) string {
	switch mode {
	case ModeAcceptEdits:
		return "AcceptEdits"
	case ModeBypass:
		return "BypassPermissions"
	default:
		if mode == "" {
			return "Default"
		}
		return string(mode)
	}
}

func categoryDisplayName(category tools.ToolCategory) string {
	switch category {
	case tools.CategoryRead:
		return "只读"
	case tools.CategoryWrite:
		return "文件修改"
	case tools.CategoryCommand:
		return "命令"
	case tools.CategoryNetwork:
		return "网络"
	case tools.CategoryMCP:
		return "MCP"
	default:
		return "未知"
	}
}

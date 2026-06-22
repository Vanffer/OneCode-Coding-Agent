package agent

import (
	"context"
	"fmt"
	"strings"

	"onecode/internal/conversation"
	"onecode/internal/llm"
	"onecode/internal/tools"
)

// Agent 持有 provider 与注册中心，执行单轮闭环。
type Agent struct {
	provider llm.Provider
	registry *tools.Registry
}

// New 创建 Agent
func New(p llm.Provider, r *tools.Registry) *Agent {
	return &Agent{
		provider: p,
		registry: r,
	}
}

// Phase 工具事件阶段。
type Phase int

const (
	PhaseStart Phase = iota // 工具开始执行
	PhaseEnd                // 工具执行完毕
)

// ToolEvent 一次工具调用的开始/结束（供 TUI 渲染工具行与结果摘要）。
type ToolEvent struct {
	Name    string // 工具名称
	Args    string // 参数预览（用于 ● name(args)）
	Phase   Phase  // 开始 or 结束
	Result  string // PhaseEnd 时的结果摘要（过长截断）
	IsError bool   // PhaseEnd 时是否错误
}

// Event 单轮闭环对外事件流元素，TUI 据非零字段分派渲染。
// Text、Tool、Done、Err 四个字段互斥——同一事件只会有一个非零。
type Event struct {
	Text string     // 文本增量（preamble 或最终答复）
	Tool *ToolEvent // 工具调用开始/结束
	Done bool       // 本轮结束
	Err  error      // 出错（不中断会话，TUI 展示后回到 idle）
}

// Run 执行单轮闭环，返回事件 channel。
// 内部流程：请求 LLM（带工具）→ 收集 tool_use → Registry.Execute → 回灌 → 再次请求 LLM → 最终文本。
// conv 在 Run 内被修改（追加 assistant/tool 消息），调用方无需额外操作。
func (a *Agent) Run(ctx context.Context, conv *conversation.Conversation) <-chan Event {
	events := make(chan Event, 1)

	go func() {
		defer close(events)

		// 第一轮：带工具请求 LLM
		toolDefs := a.registry.ToToolDefinitions()
		stream, errs := a.provider.Stream(ctx, conv.Messages(), toolDefs)

		// 收集流式事件
		var toolCall *llm.ToolCall
		var textBuilder strings.Builder

		for {
			select {
			case event, ok := <-stream:
				if !ok {
					// stream 关闭，检查是否有工具调用
					if toolCall != nil {
						a.executeTool(ctx, conv, toolCall, events)
						a.secondRound(ctx, conv, events)
						return
					}
					// 没有工具调用，直接结束
					if textBuilder.Len() > 0 {
						conv.AddAssistant(textBuilder.String())
					}
					events <- Event{Done: true}
					return
				}

				if event.Text != "" {
					textBuilder.WriteString(event.Text)
					events <- Event{Text: event.Text}
				}

				if event.ToolCall != nil {
					// 只取第一个工具调用
					if toolCall == nil {
						toolCall = event.ToolCall
					}
				}

				if event.Done {
					// 流式结束
					if toolCall != nil {
						a.executeTool(ctx, conv, toolCall, events)
						a.secondRound(ctx, conv, events)
						return
					}
					// 没有工具调用，直接结束
					if textBuilder.Len() > 0 {
						conv.AddAssistant(textBuilder.String())
					}
					events <- Event{Done: true}
					return
				}

			case err, ok := <-errs:
				if !ok {
					continue
				}
				events <- Event{Err: err}
				return

			case <-ctx.Done():
				events <- Event{Err: ctx.Err()}
				return
			}
		}
	}()

	return events
}

// executeTool 执行工具并发送事件
func (a *Agent) executeTool(ctx context.Context, conv *conversation.Conversation, toolCall *llm.ToolCall, events chan<- Event) {
	// 参数预览
	argsPreview := formatArgsPreview(toolCall.Input)

	// 工具开始事件
	events <- Event{Tool: &ToolEvent{
		Name:  toolCall.Name,
		Args:  argsPreview,
		Phase: PhaseStart,
	}}

	// 执行工具
	result := a.registry.Execute(ctx, toolCall.Name, toolCall.Input)

	// 结果摘要
	resultSummary := truncateResult(result.Content, 200)

	// 工具结束事件
	events <- Event{Tool: &ToolEvent{
		Name:    toolCall.Name,
		Args:    argsPreview,
		Phase:   PhaseEnd,
		Result:  resultSummary,
		IsError: result.IsError,
	}}

	// 回灌对话历史
	conv.AddAssistantWithToolCalls([]llm.ToolCall{*toolCall})
	conv.AddToolResult(llm.ToolResult{
		ToolUseID: toolCall.ID,
		Content:   result.Content,
		IsError:   result.IsError,
	})
}

// secondRound 执行第二轮请求（不带工具）
func (a *Agent) secondRound(ctx context.Context, conv *conversation.Conversation, events chan<- Event) {
	// 第二轮：续答（不带工具）
	stream, errs := a.provider.Stream(ctx, conv.Messages(), nil)

	var textBuilder strings.Builder
	for {
		select {
		case event, ok := <-stream:
			if !ok {
				// stream 关闭
				if textBuilder.Len() > 0 {
					conv.AddAssistant(textBuilder.String())
				} else {
					// 空最终答复，用占位提示
					conv.AddAssistant("[工具执行完成，无额外回复]")
					events <- Event{Text: "[工具执行完成，无额外回复]"}
				}
				events <- Event{Done: true}
				return
			}

			if event.Text != "" {
				textBuilder.WriteString(event.Text)
				events <- Event{Text: event.Text}
			}

			if event.Done {
				if textBuilder.Len() > 0 {
					conv.AddAssistant(textBuilder.String())
				} else {
					// 空最终答复，用占位提示
					conv.AddAssistant("[工具执行完成，无额外回复]")
					events <- Event{Text: "[工具执行完成，无额外回复]"}
				}
				events <- Event{Done: true}
				return
			}

		case err, ok := <-errs:
			if !ok {
				continue
			}
			events <- Event{Err: err}
			return

		case <-ctx.Done():
			events <- Event{Err: ctx.Err()}
			return
		}
	}
}

// formatArgsPreview 格式化参数预览
func formatArgsPreview(args map[string]interface{}) string {
	if len(args) == 0 {
		return ""
	}

	var parts []string
	for k, v := range args {
		val := fmt.Sprintf("%v", v)
		if len(val) > 50 {
			val = val[:50] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s: %s", k, val))
	}

	result := strings.Join(parts, ", ")
	if len(result) > 100 {
		result = result[:100] + "..."
	}
	return result
}

// truncateResult 截断结果
func truncateResult(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

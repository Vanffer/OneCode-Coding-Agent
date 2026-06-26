package agent

import (
	"context"
	"fmt"
	"sync"

	"onecode/internal/llm"
	"onecode/internal/tools"
)

func (a *Agent) executeToolCalls(
	ctx context.Context,
	calls []llm.ToolCall,
	mode Mode,
	events chan<- Event,
) ([]llm.ToolResult, int) {
	results := make([]llm.ToolResult, len(calls))
	badTools := 0

	for i := 0; i < len(calls); {
		if ctx.Err() != nil {
			break
		}

		call := calls[i]
		safety, ok := a.registry.Safety(call.Name)
		if !ok {
			results[i] = a.badToolResult(ctx, call, fmt.Sprintf("工具不存在: %s", call.Name), events)
			badTools++
			i++
			continue
		}
		if mode == ModePlan && safety != tools.SafetyReadOnly {
			results[i] = a.badToolResult(ctx, call, fmt.Sprintf("当前模式禁用工具: %s", call.Name), events)
			badTools++
			i++
			continue
		}

		if safety == tools.SafetyReadOnly {
			end := i + 1
			for end < len(calls) {
				nextSafety, nextOK := a.registry.Safety(calls[end].Name)
				if !nextOK || nextSafety != tools.SafetyReadOnly {
					break
				}
				end++
			}
			a.executeReadOnlyBatch(ctx, calls[i:end], results[i:end], events)
			i = end
			continue
		}

		results[i] = a.executeOneTool(ctx, call, events)
		i++
	}

	for i, result := range results {
		if result.ToolUseID == "" && calls[i].ID != "" {
			results[i] = llm.ToolResult{
				ToolUseID: calls[i].ID,
				Content:   "工具执行已取消",
				IsError:   true,
			}
		}
	}

	return results, badTools
}

func (a *Agent) executeReadOnlyBatch(
	ctx context.Context,
	calls []llm.ToolCall,
	results []llm.ToolResult,
	events chan<- Event,
) {
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = a.executeOneTool(ctx, calls[i], events)
		}(i)
	}
	wg.Wait()
}

func (a *Agent) executeOneTool(ctx context.Context, call llm.ToolCall, events chan<- Event) llm.ToolResult {
	argsPreview := formatArgsPreview(call.Input)
	sendEvent(ctx, events, Event{
		Type: EventToolStart,
		Tool: &ToolEvent{
			ID:   call.ID,
			Name: call.Name,
			Args: argsPreview,
		},
	})

	result := a.registry.Execute(ctx, call.Name, call.Input)
	toolResult := llm.ToolResult{
		ToolUseID: call.ID,
		Content:   result.Content,
		IsError:   result.IsError,
	}

	sendEvent(ctx, events, Event{
		Type: EventToolResult,
		Tool: &ToolEvent{
			ID:      call.ID,
			Name:    call.Name,
			Args:    argsPreview,
			Result:  truncateResult(result.Content, 200),
			IsError: result.IsError,
		},
	})

	return toolResult
}

func (a *Agent) badToolResult(ctx context.Context, call llm.ToolCall, message string, events chan<- Event) llm.ToolResult {
	argsPreview := formatArgsPreview(call.Input)
	sendEvent(ctx, events, Event{
		Type: EventToolStart,
		Tool: &ToolEvent{
			ID:   call.ID,
			Name: call.Name,
			Args: argsPreview,
		},
	})

	sendEvent(ctx, events, Event{
		Type: EventToolResult,
		Tool: &ToolEvent{
			ID:      call.ID,
			Name:    call.Name,
			Args:    argsPreview,
			Result:  truncateResult(message, 200),
			IsError: true,
		},
	})

	return llm.ToolResult{
		ToolUseID: call.ID,
		Content:   message,
		IsError:   true,
	}
}

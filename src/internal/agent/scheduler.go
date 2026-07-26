package agent

import (
	"context"
	"fmt"
	"sync"

	"onecode/internal/llm"
	"onecode/internal/permission"
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
			a.executeReadOnlyBatch(ctx, calls[i:end], results[i:end], mode, events, i, len(calls))
			i = end
			continue
		}

		category, _ := a.registry.Category(call.Name)
		results[i] = a.executeOneTool(ctx, call, safety, category, mode, events, i+1, len(calls))
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
	mode Mode,
	events chan<- Event,
	batchStart int,
	batchTotal int,
) {
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			category, _ := a.registry.Category(calls[i].Name)
			results[i] = a.executeOneTool(ctx, calls[i], tools.SafetyReadOnly, category, mode, events, batchStart+i+1, batchTotal)
		}(i)
	}
	wg.Wait()
}

func (a *Agent) executeOneTool(ctx context.Context, call llm.ToolCall, safety tools.Safety, category tools.ToolCategory, mode Mode, events chan<- Event, batchIndex, batchTotal int) llm.ToolResult {
	argsPreview := formatArgsPreview(call.Input)
	sendEvent(ctx, events, Event{
		Type: EventToolStart,
		Tool: &ToolEvent{
			ID:   call.ID,
			Name: call.Name,
			Args: argsPreview,
		},
	})

	if a.permissionManager != nil {
		decision := a.permissionManager.Resolve(ctx, permission.Request{
			ID:         call.ID,
			Tool:       call.Name,
			Args:       call.Input,
			Safety:     safety,
			Category:   category,
			BatchIndex: batchIndex,
			BatchTotal: batchTotal,
		})
		if decision.Action != permission.ActionAllow {
			message := fmt.Sprintf("权限拒绝: %s", decision.Reason)
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
	}

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

type eventConfirmer struct {
	mu        sync.Mutex
	events    chan<- Event
	responses <-chan permission.ConfirmationResponse
}

func (c *eventConfirmer) Confirm(ctx context.Context, req permission.ConfirmationRequest) (permission.ConfirmationResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !sendEvent(ctx, c.events, Event{
		Type:       EventPermissionRequest,
		Permission: &PermissionEvent{Request: req},
	}) {
		return permission.ConfirmationResponse{}, ctx.Err()
	}

	for {
		select {
		case response := <-c.responses:
			if response.RequestID == "" || response.RequestID == req.ID {
				if response.RequestID == "" {
					response.RequestID = req.ID
				}
				return response, nil
			}
		case <-ctx.Done():
			return permission.ConfirmationResponse{}, ctx.Err()
		}
	}
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

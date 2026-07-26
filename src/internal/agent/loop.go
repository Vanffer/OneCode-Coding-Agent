package agent

import (
	"context"
	"errors"

	"onecode/internal/conversation"
	"onecode/internal/llm"
	"onecode/internal/prompt"
	"onecode/internal/tools"
)

func (a *Agent) runLoop(ctx context.Context, conv *conversation.Conversation, opts RunOptions, events chan<- Event) {
	consecutiveBadTools := 0

	for iteration := 1; iteration <= opts.MaxIterations; iteration++ {
		if ctx.Err() != nil {
			a.finishCancelled(ctx, events, iteration-1)
			return
		}

		preflight, err := conv.Preflight(ctx, providerCompressor{
			provider: a.provider,
		}, a.conversationContextOptions())
		if !sendContextStatuses(ctx, events, preflight.Statuses) {
			a.finishCancelled(ctx, events, iteration-1)
			return
		}
		if err != nil {
			sendEvent(ctx, events, Event{Type: EventError, Err: err})
			a.finish(ctx, events, StopStreamError, iteration)
			return
		}
		if len(preflight.BoundedToolResults) > 0 || preflight.Compacted {
			if !sendConversationSnapshot(ctx, events, conv.Messages()) {
				a.finishCancelled(ctx, events, iteration-1)
				return
			}
		}

		sendEvent(ctx, events, Event{
			Type: EventProgress,
			Progress: &ProgressEvent{
				Iteration: iteration,
				Status:    ProgressRequestingModel,
				Message:   "正在请求模型",
			},
		})

		stream, errs := a.provider.Stream(ctx, conv.Messages(), a.toolDefinitionsForMode(opts.Mode), llm.StreamOptions{
			Prompt: a.promptPayload(ctx, opts, iteration),
		})
		response, err := a.collectModelResponse(ctx, stream, errs, events, iteration)
		if err != nil {
			if ctx.Err() != nil {
				a.finishCancelled(ctx, events, iteration)
				return
			}
			if isContextTooLong(err) {
				response, err = a.emergencyCompactAndRetry(ctx, conv, opts, events, iteration)
				if err == nil {
					goto handleResponse
				}
				if ctx.Err() != nil {
					a.finishCancelled(ctx, events, iteration)
					return
				}
			}
			sendEvent(ctx, events, Event{Type: EventError, Err: err})
			a.finish(ctx, events, StopStreamError, iteration)
			return
		}

	handleResponse:
		if len(response.ToolCalls) == 0 {
			if response.Text != "" {
				conv.AddAssistant(response.Text)
				if !sendConversationAppend(ctx, events, llm.Message{Role: "assistant", Content: response.Text}) {
					a.finishCancelled(ctx, events, iteration)
					return
				}
			}
			if !sendUsageAnchorStatus(ctx, events, conv, response.Usage) {
				a.finishCancelled(ctx, events, iteration)
				return
			}
			sendEvent(ctx, events, Event{
				Type: EventProgress,
				Progress: &ProgressEvent{
					Iteration: iteration,
					Status:    ProgressCompleted,
					Message:   stopReasonMessage(StopModelDone),
				},
			})
			a.finish(ctx, events, StopModelDone, iteration)
			return
		}

		conv.AddAssistantWithToolCalls(response.Text, response.ToolCalls)
		if !sendConversationAppend(ctx, events, llm.Message{Role: "assistant", Content: response.Text, ToolCalls: response.ToolCalls}) {
			a.finishCancelled(ctx, events, iteration)
			return
		}
		if !sendUsageAnchorStatus(ctx, events, conv, response.Usage) {
			a.finishCancelled(ctx, events, iteration)
			return
		}
		sendEvent(ctx, events, Event{
			Type: EventProgress,
			Progress: &ProgressEvent{
				Iteration: iteration,
				Status:    ProgressExecutingTools,
				Message:   "正在执行工具",
			},
		})

		results, badToolCount := a.executeToolCalls(ctx, response.ToolCalls, opts.Mode, events)
		for _, result := range results {
			conv.AddToolResult(result)
			if !sendConversationAppend(ctx, events, llm.Message{Role: "tool", ToolResult: &result}) {
				a.finishCancelled(ctx, events, iteration)
				return
			}
		}

		if ctx.Err() != nil {
			a.finishCancelled(ctx, events, iteration)
			return
		}

		postflight, err := conv.PostToolResults(ctx, providerCompressor{
			provider: a.provider,
		}, a.conversationContextOptions())
		if !sendContextStatuses(ctx, events, postflight.Statuses) {
			a.finishCancelled(ctx, events, iteration)
			return
		}
		if err != nil {
			sendEvent(ctx, events, Event{Type: EventError, Err: err})
			a.finish(ctx, events, StopStreamError, iteration)
			return
		}
		if len(postflight.BoundedToolResults) > 0 || postflight.Compacted {
			if !sendConversationSnapshot(ctx, events, conv.Messages()) {
				a.finishCancelled(ctx, events, iteration)
				return
			}
		}

		if badToolCount > 0 && badToolCount == len(response.ToolCalls) {
			consecutiveBadTools += badToolCount
		} else {
			consecutiveBadTools = 0
		}
		if consecutiveBadTools >= opts.MaxConsecutiveBadTools {
			sendEvent(ctx, events, Event{Type: EventText, Text: stopReasonMessage(StopBadToolLimit)})
			a.finish(ctx, events, StopBadToolLimit, iteration)
			return
		}

		if iteration == opts.MaxIterations {
			sendEvent(ctx, events, Event{Type: EventText, Text: stopReasonMessage(StopMaxIterations)})
			a.finish(ctx, events, StopMaxIterations, iteration)
			return
		}

		sendEvent(ctx, events, Event{
			Type: EventProgress,
			Progress: &ProgressEvent{
				Iteration: iteration,
				Status:    ProgressContinuing,
				Message:   "工具结果已回写，继续下一轮",
			},
		})
	}
}

func (a *Agent) promptPayload(ctx context.Context, opts RunOptions, iteration int) prompt.Payload {
	return a.promptRuntime.BuildPayload(buildRequestContext(ctx, opts, iteration))
}

func (a *Agent) toolDefinitionsForMode(mode Mode) []llm.ToolDefinition {
	if mode == ModePlan {
		return a.registry.ToToolDefinitionsBySafety(map[tools.Safety]bool{
			tools.SafetyReadOnly: true,
		})
	}
	return a.registry.ToToolDefinitions()
}

func (a *Agent) finishCancelled(_ context.Context, events chan<- Event, iteration int) {
	sendEvent(context.Background(), events, Event{Type: EventCancelled})
	a.finish(context.Background(), events, StopCancelled, iteration)
}

func (a *Agent) finish(ctx context.Context, events chan<- Event, reason StopReason, iteration int) {
	sendEvent(ctx, events, Event{
		Type: EventDone,
		Done: &DoneEvent{
			Reason:     reason,
			Iterations: iteration,
		},
	})
}

func sendUsageAnchorStatus(ctx context.Context, events chan<- Event, conv *conversation.Conversation, usage llm.Usage) bool {
	if !usage.Available {
		return true
	}
	conv.UpdateUsage(usage)
	state := conv.ContextState()
	return sendContextStatuses(ctx, events, []conversation.ContextStatus{{
		Kind:    conversation.ContextStatusUsageUpdated,
		Message: "上下文用量已用模型返回值校准",
		Usage:   state.Usage,
	}})
}

func sendEvent(ctx context.Context, events chan<- Event, event Event) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func sendConversationAppend(ctx context.Context, events chan<- Event, message llm.Message) bool {
	cloned := cloneAgentMessage(message)
	return sendEvent(ctx, events, Event{
		Type: EventConversation,
		Conversation: &ConversationEvent{
			Kind:    ConversationAppend,
			Message: &cloned,
		},
	})
}

func sendConversationSnapshot(ctx context.Context, events chan<- Event, messages []llm.Message) bool {
	return sendEvent(ctx, events, Event{
		Type: EventConversation,
		Conversation: &ConversationEvent{
			Kind:     ConversationSnapshot,
			Messages: cloneAgentMessages(messages),
		},
	})
}

func cloneAgentMessages(messages []llm.Message) []llm.Message {
	cloned := make([]llm.Message, len(messages))
	for i, message := range messages {
		cloned[i] = cloneAgentMessage(message)
	}
	return cloned
}

func cloneAgentMessage(message llm.Message) llm.Message {
	cloned := message
	cloned.ToolCalls = make([]llm.ToolCall, len(message.ToolCalls))
	for i, call := range message.ToolCalls {
		cloned.ToolCalls[i] = call
		cloned.ToolCalls[i].Input = cloneAgentMap(call.Input)
	}
	if message.ToolResult != nil {
		result := *message.ToolResult
		cloned.ToolResult = &result
	}
	return cloned
}

func cloneAgentMap(value map[string]interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(value))
	for key, item := range value {
		cloned[key] = cloneAgentValue(item)
	}
	return cloned
}

func cloneAgentValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return cloneAgentMap(typed)
	case []interface{}:
		items := make([]interface{}, len(typed))
		for i, item := range typed {
			items[i] = cloneAgentValue(item)
		}
		return items
	default:
		return value
	}
}

func sendContextStatuses(ctx context.Context, events chan<- Event, statuses []conversation.ContextStatus) bool {
	for _, status := range statuses {
		if !sendEvent(ctx, events, Event{
			Type: EventContext,
			Context: &ContextEvent{
				Kind:    contextEventKind(status.Kind),
				Message: status.Message,
				Usage:   status.Usage,
			},
		}) {
			return false
		}
	}
	return true
}

func contextEventKind(kind conversation.ContextStatusKind) ContextEventKind {
	switch kind {
	case conversation.ContextStatusToolResultStored:
		return ContextToolResultStored
	case conversation.ContextStatusCompactStarted:
		return ContextCompactStarted
	case conversation.ContextStatusCompactCompleted:
		return ContextCompactCompleted
	case conversation.ContextStatusCompactFailed:
		return ContextCompactFailed
	case conversation.ContextStatusCompactFuseTripped:
		return ContextCompactFuseTripped
	default:
		return ContextUsageUpdated
	}
}

func isContextTooLong(err error) bool {
	var contextErr *llm.ContextTooLongError
	return errors.As(err, &contextErr)
}

func (a *Agent) emergencyCompactAndRetry(
	ctx context.Context,
	conv *conversation.Conversation,
	opts RunOptions,
	events chan<- Event,
	iteration int,
) (ModelResponse, error) {
	compact, err := conv.Compact(ctx, providerCompressor{
		provider: a.provider,
	}, conversation.CompactModeEmergency, a.conversationContextOptions())
	if !sendContextStatuses(ctx, events, compact.Statuses) {
		return ModelResponse{}, ctx.Err()
	}
	if err != nil {
		return ModelResponse{}, err
	}
	if !sendConversationSnapshot(ctx, events, conv.Messages()) {
		return ModelResponse{}, ctx.Err()
	}
	if !sendEvent(ctx, events, Event{
		Type: EventContext,
		Context: &ContextEvent{
			Kind:    ContextEmergencyRetry,
			Message: "上下文已紧急压缩，正在重试请求",
			Usage:   compact.Usage,
		},
	}) {
		return ModelResponse{}, ctx.Err()
	}

	stream, errs := a.provider.Stream(ctx, conv.Messages(), a.toolDefinitionsForMode(opts.Mode), llm.StreamOptions{
		Prompt: a.promptPayload(ctx, opts, iteration),
	})
	return a.collectModelResponse(ctx, stream, errs, events, iteration)
}

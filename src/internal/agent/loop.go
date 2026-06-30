package agent

import (
	"context"
	"os"
	"runtime"
	"time"

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

		sendEvent(ctx, events, Event{
			Type: EventProgress,
			Progress: &ProgressEvent{
				Iteration: iteration,
				Status:    ProgressRequestingModel,
				Message:   "正在请求模型",
			},
		})

		stream, errs := a.provider.Stream(ctx, conv.Messages(), a.toolDefinitionsForMode(opts.Mode), llm.StreamOptions{
			Prompt: a.promptPayload(opts, iteration),
		})
		response, err := a.collectModelResponse(ctx, stream, errs, events, iteration)
		if err != nil {
			if ctx.Err() != nil {
				a.finishCancelled(ctx, events, iteration)
				return
			}
			sendEvent(ctx, events, Event{Type: EventError, Err: err})
			a.finish(ctx, events, StopStreamError, iteration)
			return
		}

		if len(response.ToolCalls) == 0 {
			if response.Text != "" {
				conv.AddAssistant(response.Text)
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
		}

		if ctx.Err() != nil {
			a.finishCancelled(ctx, events, iteration)
			return
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

func (a *Agent) promptPayload(opts RunOptions, iteration int) prompt.Payload {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return a.promptRuntime.BuildPayload(prompt.RequestContext{
		Mode:             opts.Mode.String(),
		Iteration:        iteration,
		CWD:              cwd,
		OS:               runtime.GOOS,
		Now:              time.Now(),
		ReminderInterval: opts.ReminderInterval,
	})
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

func sendEvent(ctx context.Context, events chan<- Event, event Event) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

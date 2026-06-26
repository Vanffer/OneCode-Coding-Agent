package agent

import (
	"context"

	"onecode/internal/llm"
)

// ModelResponse 是一轮模型流式响应的完整收集结果。
type ModelResponse struct {
	Text         string
	ToolCalls    []llm.ToolCall
	Usage        llm.Usage
	FinishReason llm.FinishReason
}

func (a *Agent) collectModelResponse(
	ctx context.Context,
	stream <-chan llm.StreamEvent,
	errs <-chan error,
	events chan<- Event,
	iteration int,
) (ModelResponse, error) {
	var response ModelResponse

	sendEvent(ctx, events, Event{
		Type: EventProgress,
		Progress: &ProgressEvent{
			Iteration: iteration,
			Status:    ProgressCollectingStream,
			Message:   "正在接收模型响应",
		},
	})

	for stream != nil || errs != nil {
		select {
		case <-ctx.Done():
			return response, ctx.Err()

		case event, ok := <-stream:
			if !ok {
				stream = nil
				if errs == nil {
					return response, nil
				}
				continue
			}

			if event.Text != "" {
				response.Text += event.Text
				if !sendEvent(ctx, events, Event{Type: EventText, Text: event.Text}) {
					return response, ctx.Err()
				}
			}

			if event.ToolCall != nil {
				response.ToolCalls = append(response.ToolCalls, *event.ToolCall)
			}

			if event.Usage != nil {
				response.Usage = mergeUsage(response.Usage, *event.Usage)
				if !sendEvent(ctx, events, Event{
					Type: EventUsage,
					Usage: &UsageEvent{
						InputTokens:  response.Usage.InputTokens,
						OutputTokens: response.Usage.OutputTokens,
						TotalTokens:  response.Usage.TotalTokens,
						Available:    response.Usage.Available,
					},
				}) {
					return response, ctx.Err()
				}
			}

			if event.Done {
				response.FinishReason = event.FinishReason
				return response, nil
			}

		case err, ok := <-errs:
			if !ok {
				errs = nil
				if stream == nil {
					return response, nil
				}
				continue
			}
			return response, err
		}
	}

	return response, nil
}

func mergeUsage(current, next llm.Usage) llm.Usage {
	if !next.Available {
		if current.Available {
			return current
		}
		return next
	}
	if next.TotalTokens == 0 {
		next.TotalTokens = next.InputTokens + next.OutputTokens
	}
	return next
}

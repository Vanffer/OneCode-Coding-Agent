package agent

import (
	"context"
	"fmt"

	"onecode/internal/conversation"
	"onecode/internal/llm"
	"onecode/internal/prompt"
)

const compactStablePrompt = "You are OneCode's context compaction worker. Summarize conversation history only. Do not call tools."

type providerCompressor struct {
	provider llm.Provider
}

func (c providerCompressor) Summarize(ctx context.Context, input conversation.CompactInput) (conversation.CompactOutput, error) {
	if c.provider == nil {
		return conversation.CompactOutput{}, fmt.Errorf("压缩 provider 未配置")
	}

	builder := conversation.SummaryBuilder{}
	msgs := []llm.Message{{
		Role:    "user",
		Content: builder.Prompt(input),
	}}
	stream, errs := c.provider.Stream(ctx, msgs, nil, llm.StreamOptions{
		Prompt: prompt.Payload{StableSystem: compactStablePrompt},
	})

	var text string
	for stream != nil || errs != nil {
		select {
		case <-ctx.Done():
			return conversation.CompactOutput{}, ctx.Err()
		case event, ok := <-stream:
			if !ok {
				stream = nil
				continue
			}
			if event.ToolCall != nil {
				return conversation.CompactOutput{}, fmt.Errorf("压缩请求不允许工具调用: %s", event.ToolCall.Name)
			}
			text += event.Text
			if event.Done {
				stream = nil
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			return conversation.CompactOutput{}, err
		}
	}

	summary, err := builder.ParseOutput(text)
	if err != nil {
		return conversation.CompactOutput{}, err
	}
	return conversation.CompactOutput{
		Summary: summary,
	}, nil
}

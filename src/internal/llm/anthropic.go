package llm

import (
	"context"
	"io"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"onecode/internal/config"
	"onecode/internal/prompt"
)

// anthropicProvider 实现 Anthropic Claude 的 Provider 接口
type anthropicProvider struct {
	client *anthropic.Client
	cfg    config.ProviderConfig
}

// newAnthropicProvider 创建 Anthropic Provider
func newAnthropicProvider(cfg config.ProviderConfig) (Provider, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}

	client := anthropic.NewClient(opts...)
	return &anthropicProvider{
		client: &client,
		cfg:    cfg,
	}, nil
}

// Name 返回 Provider 名称
func (p *anthropicProvider) Name() string {
	return p.cfg.Name
}

// Model 返回模型名称
func (p *anthropicProvider) Model() string {
	return p.cfg.Model
}

// Stream 发起流式对话
func (p *anthropicProvider) Stream(ctx context.Context, msgs []Message) <-chan StreamEvent {
	ch := make(chan StreamEvent, 1)

	go func() {
		defer close(ch)

		// 转换消息格式
		messages := make([]anthropic.MessageParam, len(msgs))
		for i, msg := range msgs {
			if msg.Role == "user" {
				messages[i] = anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content))
			} else {
				messages[i] = anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content))
			}
		}

		// 构建请求参数
		params := anthropic.MessageNewParams{
			Model:     p.cfg.Model,
			MaxTokens: 4096,
			System: []anthropic.TextBlockParam{
				{Text: prompt.SystemPrompt},
			},
			Messages: messages,
		}

		// 如果启用 thinking
		if p.cfg.Thinking {
			params.Thinking = anthropic.ThinkingConfigParamUnion{
				OfEnabled: &anthropic.ThinkingConfigEnabledParam{},
			}
		}

		// 创建流式请求
		stream := p.client.Messages.NewStreaming(ctx, params)

		// 迭代流式响应
		for stream.Next() {
			event := stream.Current()

			// 处理内容块增量
			switch variant := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				switch delta := variant.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					ch <- StreamEvent{Text: delta.Text}
				case anthropic.ThinkingDelta:
					// 思考增量丢弃
					continue
				}
			}
		}

		// 检查错误
		if err := stream.Err(); err != nil && err != io.EOF {
			ch <- StreamEvent{Err: err}
			return
		}

		// 正常结束
		ch <- StreamEvent{Done: true}
	}()

	return ch
}

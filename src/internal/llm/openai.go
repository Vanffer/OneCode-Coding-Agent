package llm

import (
	"context"
	"io"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"onecode/internal/config"
	"onecode/internal/prompt"
)

// openaiProvider 实现 OpenAI 的 Provider 接口
type openaiProvider struct {
	client *openai.Client
	cfg    config.ProviderConfig
}

// newOpenAIProvider 创建 OpenAI Provider
func newOpenAIProvider(cfg config.ProviderConfig) (Provider, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}

	client := openai.NewClient(opts...)
	return &openaiProvider{
		client: &client,
		cfg:    cfg,
	}, nil
}

// Name 返回 Provider 名称
func (p *openaiProvider) Name() string {
	return p.cfg.Name
}

// Model 返回模型名称
func (p *openaiProvider) Model() string {
	return p.cfg.Model
}

// Stream 发起流式对话
func (p *openaiProvider) Stream(ctx context.Context, msgs []Message) <-chan StreamEvent {
	ch := make(chan StreamEvent, 1)

	go func() {
		defer close(ch)

		// 转换消息格式，首条插入 system message
		messages := []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(prompt.SystemPrompt),
		}

		for _, msg := range msgs {
			if msg.Role == "user" {
				messages = append(messages, openai.UserMessage(msg.Content))
			} else {
				messages = append(messages, openai.AssistantMessage(msg.Content))
			}
		}

		// 构建请求参数
		params := openai.ChatCompletionNewParams{
			Model:    p.cfg.Model,
			Messages: messages,
		}

		// 创建流式请求
		stream := p.client.Chat.Completions.NewStreaming(ctx, params)

		// 迭代流式响应
		for stream.Next() {
			event := stream.Current()

			// 获取文本增量
			if len(event.Choices) > 0 && event.Choices[0].Delta.Content != "" {
				ch <- StreamEvent{Text: event.Choices[0].Delta.Content}
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

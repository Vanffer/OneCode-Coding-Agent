package llm

import (
	"context"
	"io"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"onecode/internal/config"
	"onecode/internal/prompt"
)

// anthropicStreamIdleTimeout 流式空闲超时
const anthropicStreamIdleTimeout = 5 * time.Minute

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
		defer stream.Close()

		// 空闲超时计时器
		idle := time.NewTimer(anthropicStreamIdleTimeout)
		defer idle.Stop()

		// 用独立 goroutine 读取 SSE，以便检测连接静默断开
		type sseResult struct {
			hasNext bool
			err     error
		}
		nextCh := make(chan sseResult, 1)
		readNext := func() {
			next := stream.Next()
			nextCh <- sseResult{hasNext: next}
		}

		go readNext()
		for {
			var res sseResult
			select {
			case <-ctx.Done():
				ch <- StreamEvent{Err: &NetworkError{Message: "上下文已取消"}}
				return
			case <-idle.C:
				ch <- StreamEvent{Err: &NetworkError{Message: "流式连接超时，无数据传输超过 5 分钟"}}
				return
			case res = <-nextCh:
			}

			// 重置空闲计时器
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(anthropicStreamIdleTimeout)

			if !res.hasNext {
				break
			}

			event := stream.Current()
			switch variant := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				switch delta := variant.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					ch <- StreamEvent{Text: delta.Text}
				case anthropic.ThinkingDelta:
					// 思考增量丢弃（basic-chat 阶段不暴露）
					continue
				}
			}

			go readNext()
		}

		// 检查错误
		if err := stream.Err(); err != nil && err != io.EOF {
			ch <- StreamEvent{Err: classifyAnthropicError(err)}
			return
		}

		// 正常结束
		ch <- StreamEvent{Done: true}
	}()

	return ch
}

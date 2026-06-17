package llm

import (
	"context"
	"io"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"onecode/internal/config"
	"onecode/internal/prompt"
)

// openaiStreamIdleTimeout 流式空闲超时
const openaiStreamIdleTimeout = 5 * time.Minute

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
		defer stream.Close()

		// 空闲超时计时器
		idle := time.NewTimer(openaiStreamIdleTimeout)
		defer idle.Stop()

		// 用独立 goroutine 读取 SSE，以便检测连接静默断开
		type sseResult struct {
			hasNext bool
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
			idle.Reset(openaiStreamIdleTimeout)

			if !res.hasNext {
				break
			}

			event := stream.Current()
			if len(event.Choices) > 0 && event.Choices[0].Delta.Content != "" {
				ch <- StreamEvent{Text: event.Choices[0].Delta.Content}
			}

			go readNext()
		}

		// 检查错误
		if err := stream.Err(); err != nil && err != io.EOF {
			ch <- StreamEvent{Err: classifyOpenAIError(err)}
			return
		}

		// 正常结束
		ch <- StreamEvent{Done: true}
	}()

	return ch
}

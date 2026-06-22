package llm

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
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
func (p *anthropicProvider) Stream(ctx context.Context, msgs []Message, tools []ToolDefinition) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent, 1)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		// 转换消息格式
		messages := make([]anthropic.MessageParam, 0, len(msgs))
		hasToolUse := false
		for _, msg := range msgs {
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				// assistant 消息带工具调用
				hasToolUse = true
				blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.ToolCalls)+1)
				if msg.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
				}
				for _, tc := range msg.ToolCalls {
					blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, tc.Input, tc.Name))
				}
				messages = append(messages, anthropic.NewAssistantMessage(blocks...))
			} else if msg.Role == "tool" && msg.ToolResult != nil {
				// 工具结果：作为 user 消息的 tool_result 块
				resultBlock := anthropic.NewToolResultBlock(msg.ToolResult.ToolUseID, msg.ToolResult.Content, msg.ToolResult.IsError)
				messages = append(messages, anthropic.NewUserMessage(resultBlock))
			} else if msg.Role == "user" {
				messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content)))
			} else if msg.Role == "assistant" {
				messages = append(messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content)))
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

		// 注入工具定义
		if len(tools) > 0 {
			anthropicTools := make([]anthropic.ToolUnionParam, len(tools))
			for i, t := range tools {
				toolParam := anthropic.ToolParam{
					Name: t.Name,
					Description: param.Opt[string]{
						Value: t.Description,
					},
					InputSchema: anthropic.ToolInputSchemaParam{
						Properties: t.Schema["properties"],
					},
				}
				// 设置 required 字段
				if required, ok := t.Schema["required"]; ok {
					if reqSlice, ok := required.([]string); ok {
						toolParam.InputSchema.Required = reqSlice
					}
				}
				anthropicTools[i] = anthropic.ToolUnionParam{
					OfTool: &toolParam,
				}
			}
			params.Tools = anthropicTools

			// 历史含工具交互时关闭 thinking
			if hasToolUse {
				params.Thinking = anthropic.ThinkingConfigParamUnion{}
			}
		}

		// 如果启用 thinking（无工具时）
		if p.cfg.Thinking && len(tools) == 0 {
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
		}
		nextCh := make(chan sseResult, 1)
		readNext := func() {
			next := stream.Next()
			nextCh <- sseResult{hasNext: next}
		}

		// 工具调用状态
		var toolUseID string
		var toolUseName string
		var toolUseJSON strings.Builder
		inToolUse := false

		go readNext()
		for {
			var res sseResult
			select {
			case <-ctx.Done():
				errs <- &NetworkError{Message: "上下文已取消"}
				return
			case <-idle.C:
				errs <- &NetworkError{Message: "流式连接超时，无数据传输超过 5 分钟"}
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
			case anthropic.ContentBlockStartEvent:
				// 检查是否是 tool_use 块
				if variant.ContentBlock.Type == "tool_use" {
					inToolUse = true
					toolUseID = variant.ContentBlock.ID
					toolUseName = variant.ContentBlock.Name
					toolUseJSON.Reset()
				}

			case anthropic.ContentBlockDeltaEvent:
				switch delta := variant.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					select {
					case events <- StreamEvent{Text: delta.Text}:
						// 写入成功
					case <-ctx.Done():
						// 用户取消，立即退出
						return
					}
				case anthropic.InputJSONDelta:
					if inToolUse {
						toolUseJSON.WriteString(delta.PartialJSON)
					}
				case anthropic.ThinkingDelta:
					// 思考增量丢弃
					continue
				}

			case anthropic.ContentBlockStopEvent:
				if inToolUse {
					// 解析完整的 JSON 参数
					var input map[string]interface{}
					jsonStr := toolUseJSON.String()
					if jsonStr == "" {
						jsonStr = "{}"
					}
					if err := json.Unmarshal([]byte(jsonStr), &input); err != nil {
						errs <- &LLMError{Message: "工具参数解析失败: " + err.Error()}
						return
					}

					// 吐出工具调用事件
					select {
					case events <- StreamEvent{ToolCall: &ToolCall{
						ID:    toolUseID,
						Name:  toolUseName,
						Input: input,
					}}:
						// 写入成功
					case <-ctx.Done():
						return
					}

					// 重置状态
					inToolUse = false
					toolUseID = ""
					toolUseName = ""
					toolUseJSON.Reset()
				}
			}

			go readNext()
		}

		// 检查错误
		if err := stream.Err(); err != nil && err != io.EOF {
			select {
			case errs <- classifyAnthropicError(err):
				// 写入成功
			case <-ctx.Done():
				// 用户取消，立即退出
				return
			}
			return
		}

		// 正常结束
		select {
		case events <- StreamEvent{Done: true}:
			// 写入成功
		case <-ctx.Done():
			// 用户取消，立即退出
			return
		}
	}()

	return events, errs
}

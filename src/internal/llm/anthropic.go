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
)

// anthropicStreamIdleTimeout 流式空闲超时
const anthropicStreamIdleTimeout = 5 * time.Minute

// defaultAnthropicMaxOutputTokens is an internal output budget. It is higher
// than the old 4096 cap so coding tasks can return plans, diffs, and
// verification details without hitting max_tokens too early.
const defaultAnthropicMaxOutputTokens int64 = 8192

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
func (p *anthropicProvider) Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, opts StreamOptions) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent, 1)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		// 转换消息格式。运行时 system-reminder 走消息通道，不进入顶层 System，
		// 这样稳定 system prompt 可以保持缓存友好。
		runtimeMsgs := messagesWithReminders(msgs, opts.Prompt.Reminders)
		messages, hasToolUse := buildAnthropicMessages(runtimeMsgs)

		// 构建请求参数
		systemBlocks := buildAnthropicSystemBlocks(opts.Prompt.StableSystem)
		maxTokens := defaultAnthropicMaxOutputTokens

		params := anthropic.MessageNewParams{
			Model:     p.cfg.Model,
			MaxTokens: maxTokens,
			System:    systemBlocks,
			Messages:  messages,
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
			if len(anthropicTools) > 0 {
				anthropicTools[len(anthropicTools)-1].OfTool.CacheControl = anthropic.NewCacheControlEphemeralParam()
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
				OfEnabled: &anthropic.ThinkingConfigEnabledParam{
					BudgetTokens: anthropicThinkingBudgetTokens(maxTokens),
				},
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
		finishReason := FinishUnknown

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
			case anthropic.MessageDeltaEvent:
				finishReason = mapAnthropicFinishReason(variant.Delta.StopReason)
				if variant.Usage.InputTokens > 0 || variant.Usage.OutputTokens > 0 ||
					variant.Usage.CacheCreationInputTokens > 0 || variant.Usage.CacheReadInputTokens > 0 {
					inputTokens := variant.Usage.InputTokens
					cacheCreationTokens := variant.Usage.CacheCreationInputTokens
					cacheReadTokens := variant.Usage.CacheReadInputTokens
					totalTokens := inputTokens + cacheCreationTokens + cacheReadTokens + variant.Usage.OutputTokens
					select {
					case events <- StreamEvent{Usage: &Usage{
						InputTokens:  int(inputTokens),
						OutputTokens: int(variant.Usage.OutputTokens),
						TotalTokens:  int(totalTokens),
						Available:    true,
						Cache: CacheUsage{
							Available:           true,
							CreationInputTokens: int(cacheCreationTokens),
							ReadInputTokens:     int(cacheReadTokens),
						},
					}}:
					case <-ctx.Done():
						return
					}
				}

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
		case events <- StreamEvent{Done: true, FinishReason: finishReason}:
			// 写入成功
		case <-ctx.Done():
			// 用户取消，立即退出
			return
		}
	}()

	return events, errs
}

func buildAnthropicSystemBlocks(stableSystem string) []anthropic.TextBlockParam {
	if stableSystem == "" {
		return nil
	}
	return []anthropic.TextBlockParam{{
		Text:         stableSystem,
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}}
}

func buildAnthropicMessages(msgs []Message) ([]anthropic.MessageParam, bool) {
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
			messages = appendAnthropicUserBlocks(messages, resultBlock)
		} else if msg.Role == "user" {
			messages = appendAnthropicUserBlocks(messages, anthropic.NewTextBlock(msg.Content))
		} else if msg.Role == "assistant" {
			messages = append(messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content)))
		}
	}
	return messages, hasToolUse
}

func appendAnthropicUserBlocks(messages []anthropic.MessageParam, blocks ...anthropic.ContentBlockParamUnion) []anthropic.MessageParam {
	if len(messages) > 0 && messages[len(messages)-1].Role == anthropic.MessageParamRoleUser {
		messages[len(messages)-1].Content = append(messages[len(messages)-1].Content, blocks...)
		return messages
	}
	return append(messages, anthropic.NewUserMessage(blocks...))
}

func anthropicThinkingBudgetTokens(maxTokens int64) int64 {
	if maxTokens <= 1 {
		return 1
	}
	if maxTokens <= 2048 {
		return maxTokens - 1
	}
	return maxTokens / 2
}

func mapAnthropicFinishReason(reason anthropic.StopReason) FinishReason {
	switch reason {
	case anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence:
		return FinishStop
	case anthropic.StopReasonToolUse:
		return FinishToolCalls
	case anthropic.StopReasonMaxTokens:
		return FinishLength
	default:
		return FinishUnknown
	}
}

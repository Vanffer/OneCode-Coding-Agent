package llm

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"onecode/internal/config"
	"onecode/internal/prompt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
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
func (p *openaiProvider) Stream(ctx context.Context, msgs []Message, tools []ToolDefinition) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent, 1)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		// 转换消息格式，首条插入 system message
		messages := []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(prompt.SystemPrompt),
		}

		for _, msg := range msgs {
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				// assistant 消息带工具调用
				assistantMsg := openai.ChatCompletionAssistantMessageParam{
					Content: openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: param.Opt[string]{
							Value: msg.Content,
						},
					},
				}
				for _, tc := range msg.ToolCalls {
					inputJSON, _ := json.Marshal(tc.Input)
					assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: tc.ID,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tc.Name,
								Arguments: string(inputJSON),
							},
						},
					})
				}
				messages = append(messages, openai.ChatCompletionMessageParamUnion{
					OfAssistant: &assistantMsg,
				})
			} else if msg.Role == "tool" && msg.ToolResult != nil {
				// 工具结果
				toolMsg := openai.ChatCompletionToolMessageParam{
					ToolCallID: msg.ToolResult.ToolUseID,
					Content: openai.ChatCompletionToolMessageParamContentUnion{
						OfString: param.Opt[string]{
							Value: msg.ToolResult.Content,
						},
					},
				}
				messages = append(messages, openai.ChatCompletionMessageParamUnion{
					OfTool: &toolMsg,
				})
			} else if msg.Role == "user" {
				messages = append(messages, openai.UserMessage(msg.Content))
			} else if msg.Role == "assistant" {
				messages = append(messages, openai.AssistantMessage(msg.Content))
			}
		}

		// 构建请求参数
		params := openai.ChatCompletionNewParams{
			Model:    p.cfg.Model,
			Messages: messages,
		}

		// 注入工具定义
		if len(tools) > 0 {
			openaiTools := make([]openai.ChatCompletionToolUnionParam, len(tools))
			for i, t := range tools {
				// 空参数归一
				schema := t.Schema
				if schema == nil {
					schema = map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					}
				}

				openaiTools[i] = openai.ChatCompletionToolUnionParam{
					OfFunction: &openai.ChatCompletionFunctionToolParam{
						Function: shared.FunctionDefinitionParam{
							Name: t.Name,
							Description: param.Opt[string]{
								Value: t.Description,
							},
							Parameters: shared.FunctionParameters(schema),
						},
					},
				}
			}
			params.Tools = openaiTools
		}

		// 创建流式请求
		stream := p.client.Chat.Completions.NewStreaming(ctx, params)
		defer stream.Close()

		// 空闲超时计时器：5 分钟没有数据传输则超时
		// 收到数据时需重置，触发后 idle.C 会有信号
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

		// 工具调用状态
		var toolCallID string
		var toolCallName string
		var toolCallArgs strings.Builder
		inToolCall := false

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
			// Stop() 返回 false 表示 Timer 已触发，idle.C 里有旧信号
			// 必须先读掉旧信号，否则下次 select 会误判为超时
			if !idle.Stop() {
				select {
				case <-idle.C: // 清空旧信号
				default:
				}
			}
			idle.Reset(openaiStreamIdleTimeout) // 安全重置，重新开始 5 分钟倒计时

			if !res.hasNext {
				break
			}

			event := stream.Current()
			if len(event.Choices) == 0 {
				go readNext()
				continue
			}

			choice := event.Choices[0]

			// 处理文本增量
			if choice.Delta.Content != "" {
				select {
				case events <- StreamEvent{Text: choice.Delta.Content}:
					// 写入成功
				case <-ctx.Done():
					// 用户取消，立即退出
					return
				}
			}

			// 处理工具调用增量
			if len(choice.Delta.ToolCalls) > 0 {
				tc := choice.Delta.ToolCalls[0]
				if tc.ID != "" {
					// 新的工具调用开始
					if inToolCall {
						// 先吐出之前的工具调用
						inputJSON := toolCallArgs.String()
						if inputJSON == "" {
							inputJSON = "{}"
						}
						var input map[string]interface{}
						if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
							errs <- &LLMError{Message: "工具参数解析失败: " + err.Error()}
							return
						}
						select {
						case events <- StreamEvent{ToolCall: &ToolCall{
							ID:    toolCallID,
							Name:  toolCallName,
							Input: input,
						}}:
						case <-ctx.Done():
							return
						}
					}
					inToolCall = true
					toolCallID = tc.ID
					toolCallName = tc.Function.Name
					toolCallArgs.Reset()
				}
				if tc.Function.Arguments != "" {
					toolCallArgs.WriteString(tc.Function.Arguments)
				}
			}

			// 检查是否结束
			if choice.FinishReason == "tool_calls" || choice.FinishReason == "stop" {
				if inToolCall {
					// 吐出最后一个工具调用
					inputJSON := toolCallArgs.String()
					if inputJSON == "" {
						inputJSON = "{}"
					}
					var input map[string]interface{}
					if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
						errs <- &LLMError{Message: "工具参数解析失败: " + err.Error()}
						return
					}
					select {
					case events <- StreamEvent{ToolCall: &ToolCall{
						ID:    toolCallID,
						Name:  toolCallName,
						Input: input,
					}}:
					case <-ctx.Done():
						return
					}
					inToolCall = false
				}
			}

			go readNext()
		}

		// 检查错误
		if err := stream.Err(); err != nil && err != io.EOF {
			select {
			case errs <- classifyOpenAIError(err):
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

package llm

import "context"

// ToolDefinition 工具定义，发给 LLM API 让模型知道有哪些工具可用。
// 协议无关，由 Registry.ToToolDefinitions() 生成。
type ToolDefinition struct {
	Name        string                 // 工具名称，如 "read_file"
	Description string                 // 工具描述，告诉模型何时该用这个工具
	Schema      map[string]interface{} // 参数的 JSON Schema，模型据此构造参数
}

// ToolCall 模型在流式响应中请求的一次工具调用。
// 由 llm 适配器从流式片段拼接完成后，通过 StreamEvent 吐出。
type ToolCall struct {
	ID    string                 // 唯一标识，用于关联对应的 ToolResult
	Name  string                 // 工具名称
	Input map[string]interface{} // JSON 参数，传给 Tool.Execute
}

// ToolResult 工具执行结果，回灌给 LLM 让模型据此生成最终回复。
type ToolResult struct {
	ToolUseID string // 对应的 ToolCall.ID，API 要求一一关联
	Content   string // 结果文本（成功时是输出，失败时是错误详情）
	IsError   bool   // 是否为错误，模型据此决定是否重试
}

// Message 表示对话中的一条消息。
// 扩展后支持三种角色：
//   - "user"：用户输入，Content 有值
//   - "assistant"：模型回复，Content 有值（纯文本）或 ToolCalls 有值（工具调用）
//   - "tool"：工具结果，ToolResult 有值
type Message struct {
	Role       string      // "user" | "assistant" | "tool"
	Content    string      // 纯文本内容（向后兼容）
	ToolCalls  []ToolCall  // assistant 消息携带的工具调用
	ToolResult *ToolResult // tool 消息携带的工具结果
}

// StreamEvent 流式响应中的一个事件。
// 文本增量和工具调用互斥——同一事件只会有一个字段非零。
type StreamEvent struct {
	Text     string    // 文本增量（模型的纯文本输出片段）
	ToolCall *ToolCall // 工具调用（流式拼接完成后一次性吐出，不是碎片）
	Done     bool      // 本轮流式结束
}

// Provider 定义 LLM Provider 的统一接口。
// Stream 新增 tools 参数：传入工具定义列表，模型据此决定是否调用工具。
type Provider interface {
	// Name 返回 Provider 名称，用于状态栏左侧显示
	Name() string
	// Model 返回模型名称，用于状态栏右侧显示
	Model() string
	// Stream 发起一轮流式对话
	// 内部注入内置 system prompt 与 thinking 配置；思考增量内部丢弃
	// tools 为工具定义列表，传 nil 表示不使用工具
	// events 吐出文本增量、工具调用和结束信号；errs 吐出错误（与 events 互斥）
	// ctx 取消即终止；两个 channel 都由实现方关闭
	Stream(ctx context.Context, msgs []Message, tools []ToolDefinition) (<-chan StreamEvent, <-chan error)
}

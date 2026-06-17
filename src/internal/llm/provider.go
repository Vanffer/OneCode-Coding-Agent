package llm

import "context"

// Message 表示对话中的一条消息
type Message struct {
	Role    string // "user" | "assistant"
	Content string
}

// StreamEvent 表示流式输出中的一个事件
type StreamEvent struct {
	Text string // 文本增量
	Done bool   // 本轮正常结束
}

// Provider 定义 LLM Provider 的统一接口
type Provider interface {
	// Name 返回 Provider 名称，用于状态栏左侧显示
	Name() string
	// Model 返回模型名称，用于状态栏右侧显示
	Model() string
	// Stream 发起一轮流式对话
	// 内部注入内置 system prompt 与 thinking 配置；思考增量内部丢弃
	// events 吐出文本增量和结束信号；errs 吐出错误（与 events 互斥）
	// ctx 取消即终止；两个 channel 都由实现方关闭
	Stream(ctx context.Context, msgs []Message) (<-chan StreamEvent, <-chan error)
}

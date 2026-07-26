package conversation

import "onecode/internal/llm"

// Conversation 管理单会话的多轮对话历史
type Conversation struct {
	messages []llm.Message
	context  *ContextState
}

// New 创建一个新的会话
func New(opts ...Option) *Conversation {
	c := &Conversation{
		messages: make([]llm.Message, 0),
	}
	c.context = newContextState(ContextOptions{})
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	if c.context == nil {
		c.context = newContextState(ContextOptions{})
	}
	return c
}

// AddUser 添加一条用户消息
func (c *Conversation) AddUser(text string) {
	c.messages = append(c.messages, llm.Message{
		Role:    "user",
		Content: text,
	})
}

// AddAssistant 添加一条助手消息
func (c *Conversation) AddAssistant(text string) {
	c.messages = append(c.messages, llm.Message{
		Role:    "assistant",
		Content: text,
	})
}

// AddAssistantWithToolCalls 添加带工具调用的助手消息。
// content 保存模型在工具调用前输出的可见文本，toolCalls 保存同一轮的全部工具调用。
func (c *Conversation) AddAssistantWithToolCalls(content string, toolCalls []llm.ToolCall) {
	c.messages = append(c.messages, llm.Message{
		Content:   content,
		Role:      "assistant",
		ToolCalls: toolCalls,
	})
}

// AddToolResult 添加工具结果消息
func (c *Conversation) AddToolResult(result llm.ToolResult) {
	c.messages = append(c.messages, llm.Message{
		Role:       "tool",
		ToolResult: &result,
	})
}

// Messages 返回完整的消息历史
func (c *Conversation) Messages() []llm.Message {
	return c.messages
}

// MessageCount 返回消息数量
func (c *Conversation) MessageCount() int {
	return len(c.messages)
}

// Restore replaces the current history with a defensive copy loaded from a
// session. Project context configuration is preserved while runtime usage,
// compaction failures, and the previous session's file index are reset.
func (c *Conversation) Restore(messages []llm.Message) {
	c.messages = cloneConversationMessages(messages)
	if c.context != nil {
		c.context.Usage = UsageEstimate{}
		c.context.Fuse = CompactFuse{}
		c.context.Files = &FileIndex{}
	}
}

// ContextState 返回当前上下文管理状态的快照。
func (c *Conversation) ContextState() ContextState {
	if c.context == nil {
		c.context = newContextState(ContextOptions{})
	}
	return *c.context
}

func cloneConversationMessages(messages []llm.Message) []llm.Message {
	cloned := make([]llm.Message, len(messages))
	for i, message := range messages {
		cloned[i] = message
		cloned[i].ToolCalls = make([]llm.ToolCall, len(message.ToolCalls))
		for j, call := range message.ToolCalls {
			cloned[i].ToolCalls[j] = call
			cloned[i].ToolCalls[j].Input = cloneConversationMap(call.Input)
		}
		if message.ToolResult != nil {
			result := *message.ToolResult
			cloned[i].ToolResult = &result
		}
	}
	return cloned
}

func cloneConversationMap(value map[string]interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(value))
	for key, item := range value {
		cloned[key] = cloneConversationValue(item)
	}
	return cloned
}

func cloneConversationValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return cloneConversationMap(typed)
	case []interface{}:
		cloned := make([]interface{}, len(typed))
		for i, item := range typed {
			cloned[i] = cloneConversationValue(item)
		}
		return cloned
	default:
		return value
	}
}

// Clear 清空对话历史
func (c *Conversation) Clear() {
	c.messages = make([]llm.Message, 0)
	if c.context != nil {
		c.context.Usage = UsageEstimate{}
		c.context.Fuse = CompactFuse{}
	}
}

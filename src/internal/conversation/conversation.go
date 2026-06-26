package conversation

import "onecode/internal/llm"

// Conversation 管理单会话的多轮对话历史
type Conversation struct {
	messages []llm.Message
}

// New 创建一个新的会话
func New() *Conversation {
	return &Conversation{
		messages: make([]llm.Message, 0),
	}
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

// Clear 清空对话历史
func (c *Conversation) Clear() {
	c.messages = make([]llm.Message, 0)
}

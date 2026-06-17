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

// Messages 返回完整的消息历史
func (c *Conversation) Messages() []llm.Message {
	return c.messages
}

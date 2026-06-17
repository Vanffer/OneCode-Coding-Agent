package llm

import (
	"fmt"

	"onecode/internal/config"
)

// New 根据配置创建对应的 Provider
func New(cfg config.ProviderConfig) (Provider, error) {
	switch cfg.Protocol {
	case "anthropic":
		return newAnthropicProvider(cfg)
	case "openai":
		return newOpenAIProvider(cfg)
	default:
		return nil, fmt.Errorf("不支持的协议: %s", cfg.Protocol)
	}
}

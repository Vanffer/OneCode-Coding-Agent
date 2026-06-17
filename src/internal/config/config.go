package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ProviderConfig 定义单个 LLM Provider 的配置
type ProviderConfig struct {
	Name     string `yaml:"name"`     // 状态栏左侧显示
	Protocol string `yaml:"protocol"` // "anthropic" | "openai"
	BaseURL  string `yaml:"base_url"` // 空则用 SDK 默认端点
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`    // 状态栏右侧显示
	Thinking bool   `yaml:"thinking"` // 仅 anthropic 生效
}

// Config 应用配置
type Config struct {
	Providers []ProviderConfig `yaml:"providers"`
}

// Load 从指定路径加载并校验配置文件
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取配置文件 %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("配置文件格式错误: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validate 校验配置
func validate(cfg *Config) error {
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("配置错误: providers 列表为空，至少需要一个 provider")
	}

	for i, p := range cfg.Providers {
		if p.Name == "" {
			return fmt.Errorf("配置错误: providers[%d].name 为空", i)
		}
		if p.Protocol == "" {
			return fmt.Errorf("配置错误: providers[%d] (%s).protocol 为空", i, p.Name)
		}
		if p.Protocol != "anthropic" && p.Protocol != "openai" {
			return fmt.Errorf("配置错误: providers[%d] (%s).protocol 无效，必须是 anthropic 或 openai", i, p.Name)
		}
		if p.APIKey == "" {
			return fmt.Errorf("配置错误: providers[%d] (%s).api_key 为空", i, p.Name)
		}
		if p.Model == "" {
			return fmt.Errorf("配置错误: providers[%d] (%s).model 为空", i, p.Name)
		}
	}

	return nil
}

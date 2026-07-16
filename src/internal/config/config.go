package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// ProviderConfig 定义单个 LLM Provider 的配置
type ProviderConfig struct {
	Name          string `yaml:"name"`     // 状态栏左侧显示
	Protocol      string `yaml:"protocol"` // "anthropic" | "openai"
	BaseURL       string `yaml:"base_url"` // 空则用 SDK 默认端点
	APIKey        string `yaml:"api_key"`
	Model         string `yaml:"model"`          // 状态栏右侧显示
	Thinking      bool   `yaml:"thinking"`       // 仅 anthropic 生效
	ContextWindow int    `yaml:"context_window"` // 可选：上下文窗口 token 上限
}

// Config 应用配置
type Config struct {
	Providers  []ProviderConfig     `yaml:"providers"`
	MCPServers map[string]MCPConfig `yaml:"mcp_servers"`
}

// MCPConfig 定义一个 MCP Server 的配置。
type MCPConfig struct {
	Type     string                   `yaml:"type"`
	Command  string                   `yaml:"command"`
	Args     []string                 `yaml:"args"`
	Env      map[string]string        `yaml:"env"`
	URL      string                   `yaml:"url"`
	Headers  map[string]string        `yaml:"headers"`
	ReadOnly bool                     `yaml:"read_only"`
	Tools    map[string]MCPToolConfig `yaml:"tools"`
}

// MCPToolConfig 定义单个 MCP 工具的本地覆盖配置。
type MCPToolConfig struct {
	ReadOnly *bool `yaml:"read_only"`
}

// Load 从指定路径加载并校验配置文件
func Load(path string) (*Config, error) {
	cfg, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	if err := validate(&cfg, true); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadMerged 加载用户级和项目级配置，并按项目级覆盖用户级合并 MCP Server。
func LoadMerged(userPath, projectPath string) (*Config, error) {
	projectCfg, err := loadFile(projectPath)
	if err != nil {
		return nil, err
	}
	if err := validate(&projectCfg, true); err != nil {
		return nil, err
	}

	merged := Config{
		Providers:  projectCfg.Providers,
		MCPServers: map[string]MCPConfig{},
	}

	if userPath != "" {
		userCfg, err := loadFile(userPath)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		} else {
			if err := validate(&userCfg, false); err != nil {
				return nil, err
			}
			for name, server := range userCfg.MCPServers {
				merged.MCPServers[name] = server
			}
		}
	}

	for name, server := range projectCfg.MCPServers {
		merged.MCPServers[name] = server
	}

	return &merged, nil
}

func loadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("无法读取配置文件 %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("配置文件格式错误: %w", err)
	}
	return cfg, nil
}

// validate 校验配置
func validate(cfg *Config, requireProviders bool) error {
	if err := validateProviders(cfg.Providers, requireProviders); err != nil {
		return err
	}
	if err := validateMCPServers(cfg.MCPServers); err != nil {
		return err
	}
	return expandMCPServers(cfg.MCPServers)
}

func validateProviders(providers []ProviderConfig, required bool) error {
	if len(providers) == 0 {
		if required {
			return fmt.Errorf("配置错误: providers 列表为空，至少需要一个 provider")
		}
		return nil
	}

	for i, p := range providers {
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
		if p.ContextWindow < 0 {
			return fmt.Errorf("配置错误: providers[%d] (%s).context_window 必须为正数", i, p.Name)
		}
	}

	return nil
}

func validateMCPServers(servers map[string]MCPConfig) error {
	for name, server := range servers {
		if name == "" {
			return fmt.Errorf("配置错误: mcp_servers 存在空 server 名称")
		}
		switch server.Type {
		case "stdio":
			if server.Command == "" {
				return fmt.Errorf("配置错误: mcp_servers.%s.command 为空", name)
			}
		case "http":
			if server.URL == "" {
				return fmt.Errorf("配置错误: mcp_servers.%s.url 为空", name)
			}
		default:
			return fmt.Errorf("配置错误: mcp_servers.%s.type 无效，必须是 stdio 或 http", name)
		}
	}
	return nil
}

func expandMCPServers(servers map[string]MCPConfig) error {
	for name, server := range servers {
		if err := expandStringMap(server.Env, fmt.Sprintf("mcp_servers.%s.env", name)); err != nil {
			return err
		}
		if err := expandStringMap(server.Headers, fmt.Sprintf("mcp_servers.%s.headers", name)); err != nil {
			return err
		}
		servers[name] = server
	}
	return nil
}

func expandStringMap(values map[string]string, field string) error {
	for key, value := range values {
		expanded, err := expandEnvRefs(value, fmt.Sprintf("%s.%s", field, key))
		if err != nil {
			return err
		}
		values[key] = expanded
	}
	return nil
}

var envRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandEnvRefs(value, field string) (string, error) {
	var missing string
	expanded := envRefPattern.ReplaceAllStringFunc(value, func(match string) string {
		if missing != "" {
			return ""
		}
		groups := envRefPattern.FindStringSubmatch(match)
		name := groups[1]
		envValue, ok := os.LookupEnv(name)
		if !ok {
			missing = name
			return ""
		}
		return envValue
	})
	if missing != "" {
		return "", fmt.Errorf("配置错误: %s 引用的环境变量 %s 未设置", field, missing)
	}
	return expanded, nil
}

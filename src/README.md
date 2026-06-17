# OneCode

一个用 Go 语言构建的命令行 AI 助手（Coding Agent），类似 Claude Code。

## 功能特性

- 全屏 TUI 交互界面
- 支持 Anthropic Claude 和 OpenAI 两种 API 后端
- SSE 流式输出，逐字显示回复
- Markdown 渲染美化
- 多轮对话上下文记忆
- 响应计时显示
- 错误处理与恢复

## 安装与构建

```bash
# 克隆项目
git clone <repository-url>
cd basic-chat

# 构建
go build -o onecode ./cmd/onecode/

# 或直接运行
go run ./cmd/onecode/
```

## 配置

在项目根目录创建 `.onecode/config.yaml` 文件：

```yaml
providers:
  # Anthropic Claude 官方 API
  - name: Claude
    protocol: anthropic
    api_key: YOUR_ANTHROPIC_API_KEY
    model: claude-3-sonnet-20240229
    base_url: ""  # 留空使用官方 API
    thinking: false

  # OpenAI 官方 API
  - name: GPT-4
    protocol: openai
    api_key: YOUR_OPENAI_API_KEY
    model: gpt-4
    base_url: ""  # 留空使用官方 API

  # 第三方供应商示例（如 OpenRouter、OneAPI 等）
  - name: Claude-Via-ThirdParty
    protocol: anthropic
    api_key: YOUR_THIRD_PARTY_API_KEY
    model: claude-3-sonnet-20240229
    base_url: https://api.example.com/v1  # 第三方 API 端点
    thinking: false

  - name: GPT4-Via-ThirdParty
    protocol: openai
    api_key: YOUR_THIRD_PARTY_API_KEY
    model: gpt-4
    base_url: https://api.example.com/v1  # 第三方 API 端点
```

### 配置字段说明

- `name`: Provider 名称，显示在状态栏左侧
- `protocol`: API 协议，支持 `anthropic` 或 `openai`
- `api_key`: API 密钥
- `model`: 模型名称，显示在状态栏右侧
- `base_url`: 自定义 API 端点（可选，留空使用官方 API，设置后可连接第三方供应商）
- `thinking`: 是否启用 Claude 的 extended thinking（仅 anthropic 生效）

### 支持的第三方供应商

只要第三方供应商兼容 Anthropic 或 OpenAI 的 API 协议，就可以通过设置 `base_url` 来使用：

- **OpenRouter**: `https://openrouter.ai/api/v1`
- **OneAPI**: 根据你的部署地址设置
- **其他兼容 API**: 设置对应的端点地址

## 使用方法

1. 配置好 `.onecode/config.yaml`
2. 运行 `./onecode`
3. 如果配置了多个 Provider，使用方向键选择后按 Enter 确认
4. 在输入框中输入问题，按 Enter 发送
5. 查看流式回复
6. 输入 `/exit` 或按 `Ctrl+C` 退出

## 快捷键

- `Enter`: 发送消息
- `Alt+Enter`: 换行
- `Ctrl+C`: 退出
- `/exit`: 退出

## 技术栈

- Go
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - TUI 框架
- [Bubbles](https://github.com/charmbracelet/bubbles) - TUI 组件库
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) - 样式库
- [Glamour](https://github.com/charmbracelet/glamour) - Markdown 渲染
- [Anthropic SDK Go](https://github.com/anthropics/anthropic-sdk-go) - Anthropic API
- [OpenAI SDK Go](https://github.com/openai/openai-go) - OpenAI API

## 项目结构

```
basic-chat/
├── cmd/onecode/          # 主入口
├── internal/
│   ├── config/           # 配置管理
│   ├── llm/              # LLM Provider 抽象层
│   ├── conversation/     # 对话历史管理
│   ├── prompt/           # 内置提示词
│   └── tui/              # TUI 界面
├── .onecode/             # 配置文件目录
└── README.md
```

## 许可证

MIT

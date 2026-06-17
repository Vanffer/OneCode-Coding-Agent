# OneCode Coding Agent - 基础对话功能 Plan

## 架构概览

本项目采用六层架构，自上而下各司其职：

### 1. 入口层 cmd/onecode
加载配置、打印 banner、启动 TUI。程序唯一入口，负责组装各层依赖并启动主循环。

### 2. 配置层 config
读取并校验 config.yaml，输出 providers 列表。

### 3. LLM 协议层 llm
定义协议无关的 Provider 接口与统一消息/流式事件类型。anthropic、openai 两个适配器各自封装官方 SDK，统一吐出文本增量（思考增量内部丢弃）。

### 4. 会话层 conversation
进程内维护单会话多轮历史（user/assistant 交替），提供完整上下文供 Provider 消费。

### 5. 提示词/资源 prompt
内置 system prompt 与 ASCII 柴犬 banner 文本。

### 6. 终端层 tui
基于 bubbletea 的 Model/Update/View，含状态机（选择/空闲/流式）、输入框、对话区、spinner+计时、Provider 选择列表。以"读一个再续"的 Cmd 把 llm 流式事件接入 Update。

## 核心数据结构

### config.ProviderConfig
```go
type ProviderConfig struct {
    Name     string `yaml:"name"`     // 状态栏左侧显示
    Protocol string `yaml:"protocol"` // "anthropic" | "openai"
    BaseURL  string `yaml:"base_url"` // 空则用 SDK 默认端点
    APIKey   string `yaml:"api_key"`
    Model    string `yaml:"model"`    // 状态栏右侧显示
    Thinking bool   `yaml:"thinking"` // 仅 anthropic 生效
}
```

### config.Config
```go
type Config struct {
    Providers []ProviderConfig `yaml:"providers"`
}
```

### llm.Message
```go
type Message struct {
    Role    string // "user" | "assistant"
    Content string
}
```

### llm.StreamEvent
```go
type StreamEvent struct {
    Text string // 文本增量
    Done bool   // 本轮正常结束
    Err  error  // 出错（与 Done 互斥）
}
```

### llm.Provider（接口）
```go
type Provider interface {
    Name() string  // -> 状态栏左
    Model() string // -> 状态栏右
    // 发起一轮流式对话；内部注入内置 system prompt 与 thinking 配置；思考增量内部丢弃；
    // 通过 channel 吐出文本增量/结束/错误；ctx 取消即终止。
    Stream(ctx context.Context, msgs []Message) <-chan StreamEvent
}
```

### llm.New（工厂函数）
```go
// 按 protocol 构造适配器
func New(cfg config.ProviderConfig) (Provider, error)
```

### conversation.Conversation
```go
type Conversation struct {
    messages []llm.Message
}

func (c *Conversation) AddUser(text string)
func (c *Conversation) AddAssistant(text string)
func (c *Conversation) Messages() []llm.Message
```

### prompt
```go
const SystemPrompt = "..."          // 内置固定 system prompt
const DogBanner    = "..."          // ASCII 柴犬
func RenderBanner(version, cwd string) string
```

### tui.Model
```go
type sessionState int
const (
    stateSelecting sessionState = iota // 多 provider 时的选择界面
    stateIdle                          // 等待用户输入
    stateStreaming                     // 等待/接收模型流（spinner+计时）
)

type Model struct {
    state     sessionState
    textarea  textarea.Model
    spinner   spinner.Model
    list      list.Model            // 仅多 provider 时使用
    renderer  *glamour.TermRenderer
    providers []config.ProviderConfig
    provider  llm.Provider
    conv      *conversation.Conversation
    events    <-chan llm.StreamEvent // 当前流
    curReply  strings.Builder        // 本轮 assistant 增量缓冲（动态区显示，Done 后提交 scrollback）
    turnStart time.Time              // 计时起点
    width, height int
    // 注：完成的消息（用户输入 / 渲染后的助手回复 / 错误）通过 tea.Println 提交到终端
    //     scrollback，不在 Model 内保留；无 viewport。
}

type streamMsg llm.StreamEvent
func waitForEvent(ch <-chan llm.StreamEvent) tea.Cmd // 读一个事件 -> streamMsg
```

## 模块设计

### 模块 config
**职责：** 读取并校验 .onecode/config.yaml，产出 providers 列表。
**对外接口：** `Load(path) (*Config, error)`；`Config.Providers`。
**校验规则：** 列表非空；每项 name/protocol/api_key/model 非空；protocol ∈ {anthropic, openai}。任一不满足 → 返回可读错误（指明哪个 provider 的哪个字段）。
**依赖：** gopkg.in/yaml.v3、标准库 os。

### 模块 llm
**职责：** 定义协议无关的 Provider 接口与统一消息/事件类型；按 protocol 构造适配器。
**对外接口：** Provider 接口、Message、StreamEvent、`New(cfg) (Provider, error)`。
**依赖：** anthropics/anthropic-sdk-go、openai/openai-go/v3、本模块 prompt、config。

**子单元：**

#### anthropic 适配器
- 封装 anthropic-sdk-go
- 把 []Message 转为 SDK 的 MessageParam
- 注入 System=内置 prompt、按 cfg.Thinking 设 ThinkingConfig
- NewStreaming 迭代，取 TextDelta → StreamEvent.Text，遇 ThinkingDelta 丢弃
- 结束发 Done，错误发 Err
- base_url 非空时 option.WithBaseURL 覆盖

#### openai 适配器
- 封装 openai-go/v3
- 把 []Message 转为 ChatCompletionMessageParamUnion
- 首条插入 SystemMessage(内置 prompt)
- NewStreaming 迭代取 Choices[0].Delta.Content
- 结束/出错按 stream.Err() 发 Done/Err
- base_url 非空时 WithBaseURL 覆盖；thinking 忽略

**共同点：** 各适配器内部起 goroutine 迭代 SDK 流并向 channel 推 StreamEvent，ctx.Done() 时停止；channel 在结束/出错后关闭。

### 模块 conversation
**职责：** 进程内维护单会话多轮历史（user/assistant 交替）。
**对外接口：** AddUser、AddAssistant、Messages()。
**依赖：** llm（Message 类型）。

### 模块 prompt
**职责：** 提供内置 system prompt 与 ASCII 柴犬 banner 文本。
**对外接口：** SystemPrompt 常量、DogBanner 常量、`RenderBanner(version, cwd) string`。
**依赖：** 无。

### 模块 tui
**职责：** bubbletea 应用，承载选择/对话/流式/错误的全部交互与渲染。
**对外接口：** `New(providers []config.ProviderConfig) Model`；`Run() error`。
**依赖：** bubbletea/v2(tea.Println)、bubbles/v2(textarea/spinner/list)、lipgloss/v2、glamour/v2、本项目 llm、conversation、config、prompt。

**内部职责：**
- 启动时若 providers 多于一项 → stateSelecting（list 选择）；否则直接进 stateIdle 并构造 provider。
- stateIdle：textarea 接收输入；Enter 提交（Alt+Enter 换行）；/exit 或 Ctrl+C 退出。
- 提交：conv.AddUser → provider.Stream(ctx, conv.Messages()) → 存 events → 起 waitForEvent 与 spinner.Tick；记 turnStart；切 stateStreaming。
- 提交时 tea.Println 提交用户输入块到 scrollback。
- stateStreaming：每个 streamMsg 追加 curReply（底部动态区逐字显示）并续读；spinner+"Imagining…(Ns)"计时；Done → glamour 渲染整段定型 → tea.Println 提交到 scrollback、conv.AddAssistant、清缓冲、回 stateIdle；Err → tea.Println 提交错误块、回 stateIdle。
- 窗口尺寸变化：同步 textarea/renderer 宽度（N6）。

### 模块 cmd/onecode（入口）
**职责：** 装配与启动。
**流程：** `config.Load` → `prompt.RenderBanner` 打印 → `tui.New(providers)` → `tui.Run()`。
**失败处理：** 配置错误打印可读信息并非零退出（N4）。
**依赖：** config、tui、prompt。

## 模块交互

### 调用链（启动）
```
main → config.Load(".onecode/config.yaml")
     → 若 err：打印可读错误、非零退出
     → prompt.RenderBanner(version, cwd) 打印
     → tui.New(cfg.Providers) → tui.Run()
       → providers==1：内部 llm.New(cfg[0]) 构造 provider，进 stateIdle
       → providers>1 ：进 stateSelecting
```

### 时序（多 provider 选择）
```
stateSelecting:
  list 显示各 provider 的 name + model
  用户方向键移动、Enter 选定
  → llm.New(选定 cfg) 构造 provider
  → 状态栏更新为 provider.Name()/Model()
  → 进 stateIdle
```

### 时序（一轮对话，核心）
```
stateIdle:
  用户在 textarea 输入，Enter 提交
  → conv.AddUser(text)
  → events = provider.Stream(ctx, conv.Messages())
  → turnStart = now；curReply.Reset()
  → 返回 batch(waitForEvent(events), spinner.Tick)；切 stateStreaming

stateStreaming（循环）:
  spinner.TickMsg → 推进 spinner + 计算 elapsed → 再次 spinner.Tick
  streamMsg：
    - Text 非空 → curReply 追加（底部动态区逐字显示）→ waitForEvent 续读
    - Done       → glamour 渲染 curReply → tea.Println 提交 scrollback → conv.AddAssistant → 回 stateIdle
    - Err        → tea.Println 提交"可区分样式"错误块 → 回 stateIdle（不退出）
  期间 textarea 不接受提交（N1：界面仍响应，完成内容可用终端原生滚动回看）
```

### 时序（退出）
```
任意状态：输入 "/exit"（stateIdle 识别）或 Ctrl+C
  → cancel(ctx)（终止进行中的流）→ tea.Quit → bubbletea 还原终端（N7）
```

### 数据流图
```
config.yaml ──config.Load──> []config.ProviderConfig ──llm.New──> llm.Provider
用户输入 ──> conv.AddUser ──conv.Messages()──> provider.Stream
provider.Stream ──goroutine 迭代 SDK 流──> chan llm.StreamEvent
chan ──waitForEvent Cmd──> Update(streamMsg) ──> 底部动态区(纯文本流式)
Done ──glamour──> tea.Println 提交 scrollback & conv.AddAssistant
```

## 文件组织

```
onecode/
├── cmd/
│   └── onecode/
│       └── main.go              — 入口：config.Load → banner → tui.New → Run
├── internal/
│   ├── config/
│   │   └── config.go            — ProviderConfig、Config、Load、校验
│   ├── llm/
│   │   ├── provider.go          — Provider 接口、Message、StreamEvent 定义
│   │   ├── factory.go           — New 工厂函数
│   │   ├── anthropic.go         — Anthropic 适配器
│   │   └── openai.go            — OpenAI 适配器
│   ├── conversation/
│   │   └── conversation.go      — Conversation 结构体、AddUser/AddAssistant/Messages
│   ├── prompt/
│   │   ├── prompt.go            — SystemPrompt、DogBanner、RenderBanner
│   │   ├── system.txt           — 内置 system prompt（embed）
│   │   └── banner.txt           — ASCII 柴犬（embed）
│   └── tui/
│       ├── model.go             — Model 定义、状态机、New、Run
│       ├── update.go            — Update 逻辑、消息处理
│       ├── view.go              — View 渲染
│       ├── provider_select.go   — Provider 选择列表组件
│       ├── input.go             — textarea 封装
│       ├── conversation.go      — 对话区渲染（glamour）
│       ├── status.go            — 状态栏
│       └── timer.go             — spinner + 计时
├── .onecode/
│   └── config.yaml              — 配置文件（gitignore）
├── go.mod
├── go.sum
└── README.md
```

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 语言 | Go | 项目既定技术栈 |
| TUI 框架 | charm v2 线（bubbletea/bubbles/lipgloss） | Go 生态最成熟；MVU 架构天然适配异步流式状态驱动 |
| Markdown 渲染 | glamour/v2 | 生态一致；NewTermRenderer+WordWrap 实现宽度自适应 |
| LLM 通信 | 官方 SDK（anthropic-sdk-go / openai-go/v3） | SDK 内置 SSE 解析，省去手写 HTTP 流处理 |
| 协议抽象 | 统一 Provider 接口 + 两适配器 | 上层不感知协议差异；扩展新后端只需加适配器 |
| 流式接入 TUI | "读一个再续" waitForEvent Cmd + channel | 解耦 SDK 迭代与 TUI 更新；无需持有 Program 引用；界面不阻塞 |
| 流式渲染策略 | 流式纯文本 + Done 后 glamour 定型 | glamour 需完整 Markdown 块渲染；增量渲染会抖动 |
| 渲染模型 | inline + tea.Println 提交 scrollback | 完成消息写入终端原生滚动历史，退出后保留、可用终端原生滚轮回看；仅"输入框 + 正在流式的回复 + 状态栏"为动态重绘区 |
| thinking | 仅 anthropic 生效（ThinkingConfig）；openai 忽略 | OpenAI reasoning 不经 chat completions 返回正文；思考内容本就丢弃 |
| 计时 | turnStart + spinner tick 计算 elapsed | 自请求发出即计时，复用 spinner 动画驱动 |
| provider 选择 | 单份直进 / 多份 list 选择 | 减少不必要的交互步骤 |
| 历史 | 进程内 slice，单会话 | 简化实现，不持久化 |
| system prompt | 内置常量，适配器注入 | conversation 层保持纯 user/assistant，职责单一 |
| 配置 | .onecode/config.yaml + yaml.v3；密钥入 .gitignore | 项目级配置；密钥安全 |
| 错误处理 | 运行时错误经 StreamEvent.Err 显示，不退出 | 会话不中断 |

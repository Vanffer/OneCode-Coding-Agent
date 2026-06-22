# OneCode Coding Agent - 基础对话功能 设计实现总结

## 一、项目概览

### 1.1 目标

构建一个类似 Claude Code 的命令行 AI 助手，本阶段实现纯对话功能：
- 全屏 TUI 交互界面
- 支持 Anthropic Claude 和 OpenAI 两种 API 后端
- SSE 流式输出，逐字显示回复
- Markdown 渲染美化
- 多轮对话上下文记忆

### 1.2 目录结构

```
src/
├── cmd/onecode/              # 主入口
├── internal/
│   ├── config/               # 配置管理
│   ├── llm/                  # LLM Provider 抽象层
│   ├── conversation/         # 对话历史管理
│   ├── prompt/               # 内置提示词和 banner
│   └── tui/                  # TUI 界面
├── .onecode/config.yaml      # 配置文件
├── go.mod
└── README.md
```

---

## 二、核心技术选型

| 技术 | 选择 | 理由 |
|------|------|------|
| 语言 | Go | 项目既定技术栈 |
| TUI 框架 | charm v2 (bubbletea/bubbles/lipgloss) | Go 生态最成熟，MVU 架构适配异步流式 |
| Markdown 渲染 | glamour v2 | 生态一致，支持宽度自适应 |
| LLM 通信 | 官方 SDK (anthropic-sdk-go / openai-go/v3) | 内置 SSE 解析 |
| 配置 | YAML (gopkg.in/yaml.v3) | 简洁易读 |

---

## 三、架构设计：MVU 模式

### 3.1 核心概念

Bubble Tea 采用 **Model-Update-View (MVU)** 架构：

```
┌─────────────────────────────────────────────────────────────┐
│                        Bubble Tea 程序                        │
├─────────────────────────────────────────────────────────────┤
│  Model（状态）  ←──  Update（处理消息）  ←──  Msg（消息）   │
│       │                    │                                │
│       └────────────────────┴────→  View（渲染界面）         │
└─────────────────────────────────────────────────────────────┘
```

- **Model**: 保存所有状态（当前状态机、输入框内容、对话历史等）
- **Update**: 接收消息，更新 Model，返回命令（Cmd）
- **View**: 根据 Model 渲染界面
- **Msg**: 各种事件（按键、窗口大小变化、流式数据等）
- **Cmd**: 产生新消息的异步操作

### 3.2 事件循环机制

Bubble Tea **不是轮询**，而是**事件驱动**：

```go
// Bubble Tea 内部简化逻辑（伪代码）
func (p *Program) Run() {
    cmd := model.Init()
    go executeCmd(cmd)
    
    for {
        msg := <-p.msgs  // 阻塞等待消息
        model, cmd = model.Update(msg)
        go executeCmd(cmd)  // 在独立 goroutine 中执行
        renderView(model)
    }
}
```

**消息来源：**
1. 终端输入（键盘、鼠标、窗口大小变化）
2. Cmd 返回的 Msg（如 waitForEvent 返回的 streamMsg）
3. 外部发送（program.Send()）

### 3.3 命令链模式

`waitForEvent` 不是一个循环，而是每次都创建一个新的 Cmd，形成**命令链**：

```
第 1 轮: Update 返回 [waitForEvent(ch)] → 执行 → 收到 "你" → 交给 Update
第 2 轮: Update 返回 [waitForEvent(ch)] → 执行 → 收到 "好" → 交给 Update
第 3 轮: Update 返回 [waitForEvent(ch)] → 执行 → 收到 "！" → 交给 Update
第 4 轮: Update 返回 [waitForEvent(ch)] → 执行 → 收到 Done → 交给 Update
```

**为什么不用循环？**
- 遵循 MVU 架构，所有状态变更在 Update 中，无需加锁
- 每次 Update 后触发 View 重新渲染，保持响应式
- 消息顺序确定，便于调试

---

## 四、状态机设计

### 4.1 三种状态

```go
type sessionState int

const (
    stateSelecting sessionState = iota // 多 provider 时的选择界面
    stateIdle                          // 等待用户输入
    stateStreaming                     // 等待/接收模型流
)
```

### 4.2 状态转换

```
┌──────────────┐      用户选择      ┌──────────────┐
│ stateSelecting │ ──────────────────→ │  stateIdle   │
└──────────────┘                    └──────┬───────┘
                                           │ 用户按 Enter
                                           ▼
                                    ┌──────────────┐
                                    │ stateStreaming│
                                    └──────┬───────┘
                                           │ Done/Err
                                           ▼
                                    ┌──────────────┐
                                    │  stateIdle   │
                                    └──────────────┘
```

---

## 五、模块详解

### 5.1 配置模块 (internal/config)

**职责：** 读取并校验 YAML 配置文件，产出 providers 列表。

**核心结构：**
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

**校验规则：**
- 列表非空
- 每项 name/protocol/api_key/model 非空
- protocol ∈ {anthropic, openai}

### 5.2 LLM 抽象层 (internal/llm)

**职责：** 定义协议无关的 Provider 接口，按 protocol 构造适配器。

**核心接口：**
```go
type Provider interface {
    Name() string
    Model() string
    Stream(ctx context.Context, msgs []Message) (<-chan StreamEvent, <-chan error)
}

type Message struct {
    Role    string // "user" | "assistant"
    Content string
}

type StreamEvent struct {
    Text string // 文本增量
    Done bool   // 本轮正常结束
    Err  error  // 出错（与 Done 互斥）
}
```

**工厂函数：**
```go
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
```

### 5.3 Stream 设计详解

**核心设计：** 用多个 goroutine 和 channel 实现异步、可取消、有超时的流式读取。

**返回两个 channel：**
```go
func (p *openaiProvider) Stream(ctx context.Context, msgs []Message) (<-chan StreamEvent, <-chan error) {
    events := make(chan StreamEvent, 1)  // 流式事件
    errs := make(chan error, 1)          // 错误
    // ...
    return events, errs
}
```

**为什么用独立 goroutine 调用 stream.Next()？**

`select` 只能等待 channel 操作，不能等待普通函数调用。所以需要：
1. 在独立 goroutine 中调用 `stream.Next()`（阻塞）
2. 通过 channel 把结果传回来
3. 用 `select` 同时等待多个条件（数据、超时、取消）

```go
// 辅助结构：把 stream.Next() 的结果转换为 channel 操作
type sseResult struct { hasNext bool }
nextCh := make(chan sseResult, 1)
readNext := func() {
    next := stream.Next()
    nextCh <- sseResult{hasNext: next}
}

go readNext()
for {
    select {
    case <-ctx.Done():    // 用户取消
        return
    case <-idle.C:        // 超时
        return
    case res = <-nextCh:  // 有数据
        // 处理...
        go readNext()     // 启动下一次读取
    }
}
```

**超时计时器重置：**
```go
// Stop() 返回 false 表示 Timer 已触发，idle.C 里有旧信号
// 必须先读掉旧信号，否则下次 select 会误判为超时
if !idle.Stop() {
    select {
    case <-idle.C: // 清空旧信号
    default:
    }
}
idle.Reset(openaiStreamIdleTimeout) // 安全重置
```

**Channel 写入时检查取消：**
```go
select {
case events <- StreamEvent{Text: text}:
    // 写入成功
case <-ctx.Done():
    // 用户取消，立即退出
    return
}
```

### 5.4 Conversation 模块 (internal/conversation)

**职责：** 进程内维护单会话多轮历史。

```go
type Conversation struct {
    messages []llm.Message
}

func (c *Conversation) AddUser(text string)
func (c *Conversation) AddAssistant(text string)
func (c *Conversation) Messages() []llm.Message
```

### 5.5 Prompt 模块 (internal/prompt)

**职责：** 提供内置 system prompt 和 ASCII 柴犬 banner。

```go
const SystemPrompt = "..."           // 内置固定 system prompt
const DogBanner = "..."              // ASCII 柴犬 + ONE CODE CODING CLI
func RenderBanner(version, cwd string) string  // 渲染启动横幅（渐变色）
```

### 5.6 TUI 模块 (internal/tui)

**职责：** bubbletea 应用，承载选择/对话/流式/错误的全部交互与渲染。

**Model 结构：**
```go
type Model struct {
    state         sessionState
    textarea      textarea.Model      // 输入框
    spinner       spinner.Model       // 加载动画
    selectIndex   int                 // provider 选择索引
    renderer      *glamour.TermRenderer // Markdown 渲染器
    providers     []config.ProviderConfig
    provider      llm.Provider
    conv          *conversation.Conversation
    events        <-chan llm.StreamEvent
    errs          <-chan error
    curReply      *strings.Builder    // 流式回复缓冲
    pendingChars  int                 // 待渲染字符数
    pendingMarkdown string            // 待打印的 markdown
    turnStart     time.Time           // 计时起点
    width, height int
    err           error
}
```

**状态栏渲染：**
```
 Claude                      ⠋ 0.5s                        deepseek-v4-flash 
├───────┤├──────────────────┤├──────────────────────────┤├───────────────┤
  左侧        中间状态              填充空格                 右侧
```

---

## 六、完整对话流程

### 6.1 用户发送消息

```
用户在 textarea 输入 "你好"，按 Enter
        │
        ▼
Update: tea.KeyMsg
  ├─ m.conv.AddUser("你好")           // 添加到对话历史
  ├─ tea.Println("❯ 你好")           // 提交到终端 scrollback
  ├─ m.textarea.Reset()              // 清空输入框
  ├─ m.provider.Stream(ctx, msgs)    // 调用 API，返回 channel
  ├─ m.state = stateStreaming        // 切换到流式状态
  └─ return [waitForEvent, waitForErr, spinner.Tick, renderTick]
```

### 6.2 接收流式响应

```
OpenAI goroutine                    TUI (waitForEvent)
      │                                    │
      ▼                                    │
 stream.Next() → "你"                      │
      │                                    │
      ▼                                    │
 events <- StreamEvent{Text: "你"}         │
      │                                    │
      ▼                                    ▼
 (缓冲区有 1 个)                    读取事件: "你"
      │                                    │
      ▼                                    ▼
 stream.Next() → "好"              返回 streamMsg{Text: "你"}
      │                                    │
      ▼                                    ▼
 events <- StreamEvent{Text: "好"}  Update 处理
      │                             m.curReply += "你"
      │                             return [waitForEvent]
      ▼
 (继续循环...)
```

### 6.3 批量更新优化

**问题：** TUI 渲染速度跟不上 API 返回速度，导致最后瞬间蹦出一大段。

**解决方案：** 定时批量更新，每 50ms 重绘一次。

```go
// 收到文本增量时，累加计数，不立即触发重绘
if msg.Text != "" {
    m.curReply.WriteString(msg.Text)
    m.pendingChars++
}

// 定时器每 50ms 触发一次
case tickMsg:
    if m.pendingChars > 0 {
        m.pendingChars = 0
        // 触发重绘
    }
```

### 6.4 流式完成处理

**问题：** 最后几个字符可能没被 View 渲染，就被全量 markdown 覆盖。

**解决方案：** 延迟一帧（50ms）再打印 markdown。

```go
case streamMsg:
    if msg.Done {
        // 保存 markdown，不立即打印
        m.pendingMarkdown = rendered
        // 延迟 50ms
        return m, tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
            return doneMsg{}
        })
    }

case doneMsg:
    // 打印 markdown
    tea.Println(m.pendingMarkdown)
    m.state = stateIdle
```

---

## 七、关键设计决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 流式接入 TUI | "读一个再续" waitForEvent Cmd + channel | 解耦 SDK 迭代与 TUI 更新，界面不阻塞 |
| 流式渲染策略 | 流式纯文本 + Done 后 glamour 定型 | glamour 需完整块，增量渲染会抖动 |
| 渲染模型 | inline + tea.Println 提交 scrollback | 完成消息写入终端原生滚动历史，退出后保留 |
| thinking | 仅 anthropic 生效，openai 忽略 | OpenAI reasoning 不经 chat completions 返回正文 |
| 计时 | turnStart + spinner tick 计算 elapsed | 自请求即计时，复用 spinner 动画驱动 |
| provider 选择 | 单份直进 / 多份 list 选择 | 减少不必要的交互步骤 |
| 历史 | 进程内 slice，单会话 | 简化实现，不持久化 |
| system prompt | 内置常量，适配器注入 | conversation 层保持纯 user/assistant，职责单一 |
| 配置 | .onecode/config.yaml + yaml.v3 | 项目级配置 |
| 错误处理 | 运行时错误经 StreamEvent.Err 显示，不退出 | 会话不中断 |
| 批量更新 | 每 50ms 重绘一次 | 平衡流畅性和 CPU 占用 |
| 完成延迟 | 延迟 50ms 打印 markdown | 确保最后的流式文本被渲染 |

---

## 八、数据流图

```
config.yaml ──config.Load──> []config.ProviderConfig ──llm.New──> llm.Provider
用户输入 ──> conv.AddUser ──conv.Messages()──> provider.Stream
provider.Stream ──goroutine 迭代 SDK 流──> chan llm.StreamEvent
chan ──waitForEvent Cmd──> Update(streamMsg) ──> curReply 累加
                                                         │
                                                  tickMsg 每 50ms
                                                         │
                                                         ▼
                                                   View 渲染
                                                         │
Done ──延迟 50ms──> doneMsg ──glamour──> tea.Println 提交 scrollback
```

---

## 九、待改进项

1. **上下文压缩：** 历史增长不做摘要/截断，超长由用户自行控制
2. **会话持久化：** 历史不落盘、不支持重启恢复或续聊
3. **斜杠命令体系：** 除 /exit 外，不做可扩展的命令系统
4. **运行时切换 provider：** 多份配置仅在启动时选一次，会话中途不切换
5. **流式中断：** 不支持取消正在进行的回复
6. **自动重试 / 限流退避：** 出错仅提示，不自动重试
7. **用量统计：** 不显示 token 数与费用
8. **多模态：** 不支持图片等非文本输入

---

## 十、验收清单

详见 `checklist.md`

---

## 十一、相关文档

- `spec.md` - 需求规范
- `plan.md` - 技术设计
- `task.md` - 任务拆解
- `checklist.md` - 验收清单

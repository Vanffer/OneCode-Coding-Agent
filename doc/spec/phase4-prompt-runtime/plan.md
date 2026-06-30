# Structured System Prompt Runtime Plan

## 架构概览

Phase 4 采用“Prompt Runtime + Provider 映射”的方案：在 `internal/prompt` 中新增结构化系统提示装配层，由它负责稳定模块排序、可选模块追加、环境信息渲染、Plan Mode reminder 生成；Agent loop 每轮请求模型前向 Prompt Runtime 询问本轮需要的动态系统级补充消息；Provider 层负责把稳定系统提示、动态补充消息、工具定义和会话历史映射成具体模型 API 请求。

整体分层如下：

```text
cmd/onecode
  初始化 provider、registry、agent、tui

tui
  继续负责 /plan、/do、ESC、pending plan 和事件渲染
  不理解 prompt 模块、缓存策略或 provider 消息格式

agent
  继续负责 ReAct loop
  每轮根据 mode、iteration、cwd、时间等构造 prompt runtime 输入
  把 prompt runtime 输出交给 provider.Stream
  从 provider StreamEvent 接收 usage/cache usage 并转成 Agent usage 事件

prompt
  管理稳定系统提示模块
  管理可选模块插槽
  生成环境 reminder
  生成 Plan Mode reminder
  提供本轮 prompt payload，区分稳定系统内容和动态补充内容

llm
  扩展 provider 抽象，接收 prompt payload
  OpenAI/Anthropic 分别映射 system、developer/user/system reminder、工具定义和 cache control
  解析普通 usage 与 cache usage
  保持协议差异不泄漏到 agent/tui

tools
  保持工具执行语义不变
  补强工具描述中的关键使用规则
  继续通过 registry 输出工具定义

conversation
  继续只保存真实会话消息
  不保存环境 reminder、Plan Mode reminder 等运行时补充消息
```

Phase 4 的关键变化是：`prompt.SystemPrompt` 不再作为 provider 内部硬编码读取的唯一全局文本，而是变成由 Prompt Runtime 生成的稳定系统提示主体；运行时动态内容通过独立的 supplemental messages 传入 provider，不进入 conversation 历史，也不拼进稳定主体。

新的请求数据流如下：

```text
Agent loop 第 n 轮
  -> prompt.BuildRequestContext(mode, iteration, cwd, now, ...)
  -> prompt.Runtime.Build(ctx)
       -> StableSystem: 七个固定模块 + 可选模块
       -> Reminders: 环境信息 + 按轮次注入的模式提醒
  -> registry.ToToolDefinitionsBySafety(...)
  -> provider.Stream(ctx, messages, tools, promptPayload)
  -> provider 映射成具体 API 请求
  -> provider 解析 text/tool/usage/cache usage
  -> agent collector 转成 EventText/EventUsage
```

Prompt Runtime 的设计原则：

- 稳定内容保持顺序稳定和文本稳定，服务 prompt cache。
- 动态内容每轮独立生成，不污染稳定系统提示。
- Plan Mode 规则从全局提示中移除，只在 Plan Mode 的指定轮次注入。
- 工具使用关键规则同时存在于稳定系统提示和工具描述中。
- Provider 的缓存支持是“尽力映射”，不支持显式缓存时保持行为正确但 cache usage 标记不可用。

## 核心数据结构

### prompt.ModuleKind

```go
type ModuleKind string

const (
	ModuleIdentity      ModuleKind = "identity"
	ModuleConstraints   ModuleKind = "constraints"
	ModuleTaskModes     ModuleKind = "task_modes"
	ModuleActions       ModuleKind = "actions"
	ModuleToolUse       ModuleKind = "tool_use"
	ModuleTone          ModuleKind = "tone"
	ModuleOutput        ModuleKind = "output"

	ModuleCustom        ModuleKind = "custom"
	ModuleSkills        ModuleKind = "skills"
	ModuleMemory        ModuleKind = "memory"
)
```

`ModuleKind` 表示一个提示模块的职责。七个固定模块使用稳定顺序；后三个是本阶段预留的可选模块插槽。

### prompt.Module

```go
type Module struct {
	Kind     ModuleKind
	Title    string
	Content  string
	Optional bool
}
```

`Module` 是系统提示的最小装配单元。

- `Kind` 用于排序和测试。
- `Title` 用于渲染模块标题。
- `Content` 是模块正文。
- `Optional` 表示是否属于预留扩展模块。

### prompt.BuildOptions

```go
type BuildOptions struct {
	OptionalModules []Module
}
```

`BuildOptions` 控制稳定系统提示主体如何装配。本阶段只支持调用方显式传入可选模块，不做文件加载或记忆检索。

### prompt.Runtime

```go
type Runtime struct {
	stable string
}
```

`Runtime` 持有稳定系统提示主体。它可以在 provider 初始化或 agent 初始化阶段创建，避免每轮重复拼接固定模块。

### prompt.RequestContext

```go
type RequestContext struct {
	Mode              string
	Iteration         int
	CWD               string
	OS                string
	Now               time.Time
	ReminderInterval  int
}
```

`RequestContext` 表示一次模型请求的动态上下文。

- `Mode` 表示当前运行模式，例如 `execute` 或 `plan`。
- `Iteration` 表示当前 Agent loop 轮次，从 1 开始。
- `CWD` 表示当前工作目录。
- `OS` 表示当前操作系统。
- `Now` 表示当前时间，由调用方传入，便于测试。
- `ReminderInterval` 控制 Plan Mode 完整 reminder 的重复频率，默认 5。

### prompt.Payload

```go
type Payload struct {
	StableSystem string
	Reminders    []Reminder
}
```

`Payload` 是 Prompt Runtime 给 Provider 的请求级提示载荷。

- `StableSystem` 是稳定系统提示主体，适合缓存。
- `Reminders` 是本轮动态系统级补充消息，不写入 conversation。

### prompt.Reminder

```go
type Reminder struct {
	Kind    ReminderKind
	Content string
}
```

`Reminder` 表示一条动态系统级补充消息，例如环境信息或 Plan Mode 约束。

### prompt.ReminderKind

```go
type ReminderKind string

const (
	ReminderEnvironment ReminderKind = "environment"
	ReminderPlanMode    ReminderKind = "plan_mode"
)
```

`ReminderKind` 用于 Provider 映射和测试断言。

### llm.StreamOptions

```go
type StreamOptions struct {
	Prompt prompt.Payload
}
```

`StreamOptions` 用于扩展 Provider 请求选项。相比继续给 `Provider.Stream` 增加多个参数，使用 options 可以减少未来扩展时的接口震荡。

### llm.CacheUsage

```go
type CacheUsage struct {
	Available           bool
	CreationInputTokens int
	ReadInputTokens     int
}
```

`CacheUsage` 表示 prompt cache 相关 token 用量。

- `Available=false` 表示 provider 未提供缓存信息。
- `CreationInputTokens` 表示本次创建缓存写入的 token。
- `ReadInputTokens` 表示本次从缓存读取命中的 token。

### llm.Usage

```go
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	Available    bool
	Cache        CacheUsage
}
```

`Usage` 在 Phase 3 基础上增加 `Cache` 字段。普通 token 与 cache token 分开表达，避免 Provider 不支持缓存时产生歧义。

### agent.RunOptions 新增字段

```go
type RunOptions struct {
	Mode                   Mode
	MaxIterations          int
	MaxConsecutiveBadTools int
	ReminderInterval       int
}
```

`ReminderInterval` 控制 Plan Mode 完整 reminder 的重复频率。零值使用默认值 5。

## 核心接口

### prompt.NewRuntime

```go
func NewRuntime(opts BuildOptions) (*Runtime, error)
```

创建 Prompt Runtime，装配并缓存稳定系统提示主体。它会校验固定模块顺序、可选模块内容和空模块问题。

### prompt.DefaultModules

```go
func DefaultModules() []Module
```

返回七个固定模块，顺序已经固定。测试可以直接验证顺序。

### prompt.BuildStableSystem

```go
func BuildStableSystem(opts BuildOptions) (string, error)
```

构造稳定系统提示主体。`Runtime` 内部使用它，测试也可以直接调用。

### prompt.BuildPayload

```go
func (r *Runtime) BuildPayload(ctx RequestContext) Payload
```

根据本轮动态上下文生成完整 prompt payload。`StableSystem` 直接复用 runtime 中的稳定主体，`Reminders` 根据环境和模式生成。

### prompt.ShouldInjectFullPlanReminder

```go
func ShouldInjectFullPlanReminder(iteration int, interval int) bool
```

判断当前 Plan Mode 轮次是否应该注入完整 reminder。规则为第 1 轮注入完整内容，之后每隔 interval 轮重复一次。

默认 `interval=5` 时，完整 reminder 出现在第 1、6、11、16 轮。

### llm.Provider

```go
type Provider interface {
	Name() string
	Model() string
	Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, opts StreamOptions) (<-chan StreamEvent, <-chan error)
}
```

`Provider.Stream` 增加 `StreamOptions`，由 Agent 传入 Prompt Runtime 生成的 payload。Provider 内部不再直接读取 `prompt.SystemPrompt` 全局变量。

### llm.StreamEvent

```go
type StreamEvent struct {
	Text         string
	ToolCall     *ToolCall
	Usage        *Usage
	Done         bool
	FinishReason FinishReason
}
```

`StreamEvent` 结构保持字段名不变，但 `Usage` 内部增加 cache usage，Agent collector 和 TUI 不需要理解 provider 原始 usage 字段。

## 模块设计

### prompt

**职责：**

`prompt` 从“嵌入一个全局 system.txt”升级为 Prompt Runtime。它负责生成稳定系统提示和动态系统级补充消息，但不理解具体 provider API。

主要职责：

- 定义七个固定模块和三个可选模块插槽。
- 按固定优先级拼接稳定系统提示。
- 确保稳定提示不包含工作目录、日期、轮次、当前模式等动态信息。
- 渲染环境信息 reminder。
- 渲染 Plan Mode reminder。
- 判断哪些轮次需要完整 Plan Mode reminder。
- 提供 Prompt Payload 给 Agent 和 Provider 使用。

**对外接口：**

```go
func DefaultModules() []Module

func BuildStableSystem(opts BuildOptions) (string, error)

func NewRuntime(opts BuildOptions) (*Runtime, error)

func (r *Runtime) StableSystem() string

func (r *Runtime) BuildPayload(ctx RequestContext) Payload

func ShouldInjectFullPlanReminder(iteration int, interval int) bool
```

**内部拆分：**

```text
runtime.go
  Runtime、BuildOptions、Payload、RequestContext

modules.go
  Module、ModuleKind、DefaultModules、BuildStableSystem

reminder.go
  Reminder、ReminderKind、environment reminder、plan reminder、轮次判断
```

**关键规则：**

- 固定模块顺序只能由 `DefaultModules` 决定。
- 可选模块统一追加到固定模块之后。
- 每个模块渲染为标题加正文，模块之间空一行。
- `BuildStableSystem` 对空模块报错，避免稳定提示意外缺块。
- `BuildPayload` 每轮都返回环境 reminder。
- `BuildPayload` 只在 `Mode=plan` 时返回 Plan Mode reminder。
- Plan Mode 完整 reminder 按第 1 轮和间隔轮次注入，其余轮次注入短提醒或不注入。建议本阶段使用“短提醒”，可观测性更强。

### agent

**职责：**

`agent` 继续负责 ReAct loop，但在每轮请求 provider 前构造 prompt payload。

主要职责：

- 在 `Agent` 中持有 `*prompt.Runtime`。
- 每轮 loop 开始时，根据 `RunOptions`、当前 iteration、cwd、runtime.GOOS 和当前时间构造 `prompt.RequestContext`。
- 调用 `promptRuntime.BuildPayload`。
- 将 prompt payload 放入 `llm.StreamOptions` 传给 provider。
- 将 provider 返回的 cache usage 继续通过 `EventUsage` 传给 TUI。

**对外接口变化：**

```go
func New(p llm.Provider, r *tools.Registry, pr *prompt.Runtime) *Agent
```

如果为了减少调用方变更，也可以提供兼容构造：

```go
func New(p llm.Provider, r *tools.Registry) *Agent
```

内部使用 `prompt.NewRuntime(prompt.BuildOptions{})` 创建默认 runtime。

推荐选择兼容构造，避免 TUI 和测试一次性大改。

**RunOptions 变化：**

```go
ReminderInterval int
```

默认值为 5。

**关键规则：**

- Agent 不把 reminder 写入 conversation。
- Agent 不直接拼 system prompt 文本。
- Agent 不关心 provider 如何表达 reminder。
- Execute Mode 不注入完整 Plan Mode 规则。

### llm

**职责：**

`llm` 负责接收 prompt payload，并把它映射到具体 provider 请求。

主要职责：

- 扩展 `Provider.Stream`，接收 `StreamOptions`。
- OpenAI provider 不再直接读取全局 `prompt.SystemPrompt`。
- Anthropic provider 不再直接读取全局 `prompt.SystemPrompt`。
- Provider 根据 `Prompt.StableSystem` 和 `Prompt.Reminders` 构造请求。
- 尽可能给稳定系统提示和工具定义配置缓存友好参数。
- 解析 usage 中的 cache 字段，填充 `llm.Usage.Cache`。

**OpenAI 映射策略：**

- 稳定系统提示作为 system 或 developer 消息放在消息列表最前。
- reminder 作为后续 system/developer 风格消息，内容用 `<system-reminder>` 标签包裹。
- 会话历史按原逻辑追加。
- 工具定义按原逻辑追加。
- 如果当前 SDK/API 不支持显式 cache control，则保持消息顺序稳定，cache usage 标记为不可用。

**Anthropic 映射策略：**

- 稳定系统提示作为第一段 system block。
- 动态 reminder 作为后续 system block，内容带 `<system-reminder>` 标签。
- 对稳定系统 block 和工具定义尽量设置 cache control。
- 继续按原逻辑映射 user/assistant/tool_result。
- 从 usage 中解析 `cache_creation_input_tokens` 和 `cache_read_input_tokens`。

**关键规则：**

- Provider 不自行决定是否注入 Plan Mode；只映射传入的 payload。
- Provider 不把 reminder 写进 conversation。
- Provider 不能在 cache 字段不可用时伪造命中。
- Provider 适配失败不应影响没有缓存能力的基本请求。

### tools

**职责：**

`tools` 的执行语义不变，但工具描述需要强化关键规则，使模型在工具选择阶段也能看到约束。

主要职责：

- 更新 `read_file` 描述，强调用于查看真实文件内容和编辑前检查上下文。
- 更新 `edit_file` 描述，强调编辑前应先读取文件，`old_string` 必须精确匹配。
- 更新 `write_file` 描述，强调创建或覆盖文件的副作用。
- 更新 `glob` 描述，强调用于路径查找。
- 更新 `grep` 描述，强调用于内容搜索。
- 更新 `bash` 描述，强调命令有副作用，应优先用专用工具完成文件读写搜索。

**关键规则：**

- 不改变工具参数 schema。
- 不改变工具返回格式。
- 不改变工具执行逻辑。
- 只改描述和必要测试。

### tui

**职责：**

`tui` 继续只消费 Agent 事件，不参与 prompt 组装。

主要职责：

- 显示 token usage 时，如果 cache usage 可用，可以在状态栏或工具区展示缓存创建和命中 token。
- 如果 cache usage 不可用，不显示或显示为 unavailable。
- 保持 `/plan`、`/do`、ESC 行为不变。

**关键规则：**

- TUI 不读取 prompt 模块。
- TUI 不解析 provider 原始 usage。
- TUI 不保存 reminder 状态。

### conversation

**职责：**

`conversation` 保持真实会话历史，不保存运行时系统补充消息。

主要职责：

- 继续保存 user、assistant、tool messages。
- 不新增 system reminder 到 message history。
- 测试确认 reminder 注入不会污染 conversation。

**关键规则：**

- `Messages()` 返回的历史不包含环境信息 reminder。
- `Messages()` 返回的历史不包含 Plan Mode reminder。

## 模块交互

### 初始化流程

程序启动时创建 Prompt Runtime：

```text
cmd/onecode
  -> prompt.NewRuntime(prompt.BuildOptions{})
  -> llm.NewProvider(...)
  -> tools.NewRegistry(...)
  -> agent.New(provider, registry, promptRuntime)
  -> tui.New(...)
```

如果 `agent.New` 保持兼容签名，也可以由 Agent 内部创建默认 Prompt Runtime：

```text
agent.New(provider, registry)
  -> prompt.NewRuntime(prompt.BuildOptions{})
```

推荐入口层显式创建 runtime，原因是后续项目指令、Skill、Memory 接入时，入口层可以统一组装可选模块。

### 稳定系统提示构造

```text
prompt.NewRuntime
  -> prompt.DefaultModules()
       1. identity
       2. constraints
       3. task_modes
       4. actions
       5. tool_use
       6. tone
       7. output
  -> append optional modules
       8. custom
       9. skills
       10. memory
  -> BuildStableSystem
  -> Runtime{stable: "..."}
```

稳定主体只构造一次。只要固定模块文本、可选模块文本和顺序不变，稳定提示就保持缓存友好。

稳定主体示意：

```text
# Identity
...

# System Constraints
...

# Task Modes
...

# Action Execution
...

# Tool Use
...

# Tone
...

# Text Output
...
```

### 每轮模型请求流程

```text
agent.runLoop iteration n
  -> prompt.RequestContext{
       Mode: opts.Mode.String(),
       Iteration: n,
       CWD: current working directory,
       OS: runtime.GOOS,
       Now: clock.Now(),
       ReminderInterval: opts.ReminderInterval,
     }
  -> promptRuntime.BuildPayload(requestCtx)
       StableSystem: stable prompt body
       Reminders:
         - environment reminder
         - optional plan mode reminder
  -> provider.Stream(ctx, conv.Messages(), toolDefs, llm.StreamOptions{
       Prompt: payload,
     })
```

Agent 只传递 payload，不关心 provider 如何组装最终 API 请求。

### Environment Reminder 数据流

环境信息每轮注入，但不进入 stable system，也不进入 conversation：

```text
prompt.BuildPayload
  -> buildEnvironmentReminder(ctx)
  -> Reminder{
       Kind: ReminderEnvironment,
       Content:
         <system-reminder>
         Environment:
         - Working directory: ...
         - OS: ...
         - Date: ...
         - Mode: ...
         </system-reminder>
     }
```

Provider 收到后将其映射成系统级消息或系统块。Conversation 中仍然只有用户、助手和工具消息。

### Plan Mode Reminder 数据流

Plan Mode reminder 只在 Plan Mode 注入：

```text
if ctx.Mode == "plan" {
    if ShouldInjectFullPlanReminder(ctx.Iteration, ctx.ReminderInterval) {
        add full plan reminder
    } else {
        add compact plan reminder
    }
}
```

默认 `ReminderInterval=5` 时：

```text
iteration 1  -> full reminder
iteration 2  -> compact reminder
iteration 3  -> compact reminder
iteration 4  -> compact reminder
iteration 5  -> compact reminder
iteration 6  -> full reminder
iteration 11 -> full reminder
```

完整 reminder 说明：

```text
- 当前处于 Plan Mode
- 只能使用 read_file、glob、grep 等只读工具
- 不能写文件、编辑文件、运行有副作用命令
- 目标是分析并输出可执行计划
- 等待用户 /do 后才执行修改
```

精简 reminder 说明：

```text
- Reminder: still in Plan Mode; read-only inspection only.
```

Execute Mode 不注入完整 Plan Mode 规则，也不注入精简 Plan Mode reminder。

### Provider 请求映射流程

Provider 统一接收：

```text
StableSystem
Reminders[]
Messages[]
Tools[]
```

OpenAI 请求映射：

```text
messages:
  1. system/developer: StableSystem
  2. system/developer: <system-reminder>Environment...</system-reminder>
  3. system/developer: <system-reminder>Plan Mode...</system-reminder> 只在 Plan Mode
  4. conversation messages...
tools:
  tool definitions
```

Anthropic 请求映射：

```text
system:
  1. text block: StableSystem
  2. text block: <system-reminder>Environment...</system-reminder>
  3. text block: <system-reminder>Plan Mode...</system-reminder> 只在 Plan Mode
messages:
  conversation messages...
tools:
  tool definitions
```

缓存策略：

```text
StableSystem: cache candidate
Tool definitions: cache candidate where supported
Reminders: dynamic, no cache control
Conversation messages: dynamic, no cache control
```

### Usage 与 Cache Usage 数据流

Provider 从 API 事件中解析 usage：

```text
Provider raw stream usage
  -> llm.Usage{
       InputTokens,
       OutputTokens,
       TotalTokens,
       Available,
       Cache: llm.CacheUsage{
         Available,
         CreationInputTokens,
         ReadInputTokens,
       },
     }
  -> llm.StreamEvent{Usage: &usage}
  -> agent.collectModelResponse
  -> agent.EventUsage
  -> TUI status/render
```

如果 provider 不提供 cache 字段：

```text
Cache.Available = false
CreationInputTokens = 0
ReadInputTokens = 0
```

这里的 0 只是结构体零值，语义由 `Available=false` 决定，UI 不应把它展示为“命中 0”。

### 工具描述强化数据流

工具定义仍由 Registry 生成：

```text
tools.Registry.ToToolDefinitions
  -> Tool.Description()
  -> llm.ToolDefinition.Description
  -> provider tools
```

Phase 4 只修改各工具的描述文本，让模型在工具选择阶段再次看到关键规则。工具 schema、执行逻辑和返回格式保持不变。

### Conversation 不污染保证

运行时补充消息只走 provider options，不写入 conversation：

```text
conv.Messages()
  -> user / assistant / tool only

prompt.Payload.Reminders
  -> provider request only
```

测试可以通过以下方式验证：

```text
1. 创建 conversation，加入用户消息。
2. 运行一轮 Agent，mock provider 记录收到的 prompt payload。
3. 检查 provider 收到 reminders。
4. 检查 conv.Messages() 中没有 reminder 内容。
```

## 文件组织

```text
src/
├── cmd/
│   └── onecode/
│       └── main.go
└── internal/
    ├── prompt/
    │   ├── prompt.go              — 保留 banner 渲染；移除单一 SystemPrompt 依赖
    │   ├── runtime.go             — Runtime、BuildOptions、Payload、RequestContext
    │   ├── modules.go             — Module、ModuleKind、DefaultModules、BuildStableSystem
    │   ├── reminder.go            — Reminder、ReminderKind、环境 reminder、Plan Mode reminder
    │   ├── runtime_test.go        — runtime payload、动态信息隔离测试
    │   ├── modules_test.go        — 模块顺序、格式、可选模块测试
    │   └── reminder_test.go       — Plan Mode 注入频率测试
    ├── llm/
    │   ├── provider.go            — StreamOptions、CacheUsage、Usage 扩展、Provider.Stream 签名
    │   ├── openai.go              — 使用 prompt payload 构造请求，映射 cache usage 不可用/可用字段
    │   ├── anthropic.go           — system block/reminder/cache usage 映射
    │   └── usage_test.go          — usage/cache usage 映射测试，如需要可拆 provider-specific tests
    ├── agent/
    │   ├── agent.go               — Agent 持有 Prompt Runtime，默认选项含 ReminderInterval
    │   ├── loop.go                — 每轮构造 prompt payload 并传入 StreamOptions
    │   ├── collector.go           — 继续转发 usage，支持 cache usage
    │   └── loop_test.go           — 验证 reminder 不污染 conversation、Plan/Execute 注入差异
    ├── tools/
    │   ├── read_file.go           — 强化工具描述
    │   ├── write_file.go          — 强化工具描述
    │   ├── edit_file.go           — 强化工具描述
    │   ├── bash.go                — 强化工具描述
    │   ├── glob.go                — 强化工具描述
    │   ├── grep.go                — 强化工具描述
    │   └── registry_test.go       — 如需要补充工具描述断言
    └── tui/
        └── model.go               — usage 展示兼容 cache usage 可用/不可用
doc/
└── spec/
    └── phase4-prompt-runtime/
        ├── spec.md
        ├── plan.md
        ├── task.md
        ├── checklist.md
        └── manual-scenarios.md    — 人工对比场景
```

`system.txt` 有两种处理方式：

- 推荐：保留文件但不再作为主系统提示来源，只作为迁移参考或删除后改用 Go 模块文本。
- 实现时优先选择 Go 常量模块文本，因为模块顺序和内容更容易被单元测试直接覆盖。

如果保留 `system.txt`，也不应继续在 provider 中直接引用全局 `prompt.SystemPrompt`。

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| Prompt 架构 | 新增 `prompt.Runtime`，稳定主体和动态 reminder 分离 | 满足缓存友好、动态注入和后续扩展需求 |
| 固定模块数量 | 七个固定模块按用户指定优先级排序 | 与需求一致，且比单个大 prompt 更可维护 |
| 可选模块 | 只实现显式传入和追加位置，不做加载 | 符合本阶段不做项目指令、Skill、Memory 的边界 |
| 动态注入形式 | 使用 `prompt.Reminder`，内容带 `<system-reminder>` 标签 | Provider 可灵活映射，模型也能识别为系统级补充约束 |
| Reminder 持久化 | 不写入 conversation | 避免污染历史和后续 `/do`、普通执行模式 |
| Plan Mode 提醒策略 | 第 1 轮完整，之后每 5 轮完整，其余轮次短提醒 | 兼顾约束保持和上下文体量控制 |
| Provider 接口 | `Provider.Stream` 增加 `llm.StreamOptions` | 后续扩展 prompt/cache/metadata 时不继续膨胀参数 |
| 稳定提示缓存 | Provider 尽力设置 cache-friendly 映射，不把缓存作为功能正确性的前提 | 不同 API 能力不同，保持跨 provider 可用 |
| Cache usage | 扩展 `llm.Usage` 增加 `CacheUsage` | Agent/TUI 不需要理解 provider 原始字段 |
| OpenAI cache | 本阶段以稳定消息顺序和不可用 cache usage 为主，若 SDK 支持则再映射显式字段 | OpenAI SDK/API 形态可能变化，避免过度绑定 |
| Anthropic cache | 对 stable system 和工具定义尽力设置 cache control，并解析 cache creation/read tokens | Anthropic 流式 usage 已暴露相关字段，适合先落地 |
| 工具规则强化 | 同时写入系统提示和工具描述 | 模型选择工具时能在 tool description 层再次看到关键约束 |
| 测试策略 | Prompt Runtime 单测 + Agent mock provider 单测 + Usage 映射测试 | 核心行为不依赖真实远程 LLM |
| TUI 改动 | 只展示统一 Usage，cache 字段不可用时不强行显示 | 保持 UI 简洁，不让 provider 差异污染交互层 |

## Plan 覆盖检查

| Spec 需求 | 设计归属 |
|-----------|----------|
| F1 结构化系统提示模块 | `prompt/modules.go`、`BuildStableSystem` |
| F2 可选提示模块 | `BuildOptions.OptionalModules` |
| F3 稳定/动态分离 | `Runtime.stable`、`BuildPayload`、`Reminder` |
| F4 系统级补充消息 | `prompt.Reminder`、Provider 映射 |
| F5 环境信息注入 | `RequestContext`、environment reminder |
| F6 Plan Mode 轮次提醒 | `ShouldInjectFullPlanReminder`、plan reminder |
| F7 工具规则双重强化 | `DefaultModules` + 各工具 Description |
| F8 Provider 缓存映射 | `llm.StreamOptions`、OpenAI/Anthropic 请求构造 |
| F9 缓存用量解析 | `llm.CacheUsage`、provider usage mapping |
| F10 Prompt 组装可测试 | `prompt/*_test.go` |
| F11 人工对比场景 | `manual-scenarios.md` |

当前设计没有发现未覆盖的功能需求。依赖方向保持为：`tui -> agent -> prompt/llm/tools/conversation`，`llm -> prompt` 只使用 payload 类型，不反向依赖 agent；`conversation` 仍然不感知 prompt runtime。

# Agent Loop Plan

## 架构概览

Phase 3 采用“Agent Runtime 化”的方案：保留现有 `Provider`、`Conversation`、`Registry`、`Tool` 等基础抽象，把当前 `agent.Run` 从单轮闭环升级为可配置的多轮运行时。Agent 层成为 ReAct loop 的唯一编排者，负责循环、停止条件、工具批处理、模式限制、事件输出和会话写回；TUI 层只负责把用户输入转换成运行请求，并消费 Agent 事件渲染界面。

整体分层保持 Phase 2 的依赖方向，但职责会更清晰：

```text
cmd/onecode
  注册内置工具，创建 provider、registry、TUI model

tui
  解析 /plan、/do、/exit 等用户命令
  保存 pending plan 和当前运行 cancel 函数
  启动 Agent loop，消费统一事件流并渲染文本、工具、进度、错误、取消

agent
  执行 ReAct loop
  根据模式选择可用工具
  收集每轮完整模型响应
  执行工具批次并回写 Conversation
  判断完成、取消、上限、连续无效工具、流错误等停止条件

llm
  维持 provider 抽象
  适配 Anthropic/OpenAI 流式协议
  输出统一 StreamEvent，包括文本、工具调用、完成原因和可用的 token 用量

conversation
  保存内部统一消息历史
  支持 assistant 文本、assistant tool_calls、tool_result 和 plan/do 注入消息

tools
  继续提供 Tool 与 Registry
  补充工具安全分类与按模式导出工具定义的能力
  继续隔离工具超时、panic 和结构化错误
```

Agent loop 的主路径如下：

```text
用户输入
  -> TUI 判断普通任务 /plan /do
  -> 构造 Agent 运行选项
  -> Agent 进入第 1 次迭代
  -> Provider 流式返回文本和工具调用
  -> 流式收集器一边把文本事件发给 TUI，一边累积完整响应
  -> 如果没有工具调用：写入 assistant 文本，Done
  -> 如果有工具调用：写入 assistant tool_calls
  -> Tool Scheduler 按安全性分批执行工具
  -> 每个工具结果写入 Conversation
  -> Agent 判断停止条件
  -> 未停止则进入下一次迭代
```

Plan Mode 是同一套 Agent loop 的两种运行模式，而不是单独做一套流程：

```text
/plan
  -> TUI 把用户目标作为计划请求加入会话
  -> Agent 以 Plan 模式运行，只暴露 read_file、glob、grep
  -> 模型输出计划且无更多工具调用时结束
  -> TUI 保存这次 assistant 计划文本为 pending plan

/do
  -> TUI 检查 pending plan 是否存在
  -> 将“执行这份计划”的用户意图注入会话
  -> Agent 以 Execute 模式运行，暴露全部工具
  -> 完成、取消或失败后清理 pending plan，避免误执行旧计划
```

这套划分覆盖 spec 的核心需求：F1/F2 由 Agent loop 和 Conversation 写回承担；F3/F4 由工具调度器承担；F5/F6 由统一事件和流式收集器承担；F7/F8/F9 由 loop 状态机与 TUI 取消信号承担；F10/F11 由 TUI 的 pending plan 状态和 Agent mode 承担；F12/F13 则通过保持现有工具语义和收敛 provider 差异来满足。

## 核心数据结构

### agent.Mode

```go
type Mode int

const (
	ModeExecute Mode = iota
	ModePlan
)
```

`Mode` 表示 Agent 本次运行处于哪种工具开放策略：

- `ModeExecute`：普通执行模式，暴露全部已注册工具。
- `ModePlan`：计划模式，只暴露只读工具，并把禁用工具调用作为结构化错误回写。

### agent.RunOptions

```go
type RunOptions struct {
	Mode                    Mode
	MaxIterations           int
	MaxConsecutiveBadTools  int
}
```

`RunOptions` 是 TUI 启动 Agent loop 时传入的运行配置：

- `Mode` 控制工具可见范围和禁用工具处理。
- `MaxIterations` 控制 ReAct loop 最大迭代次数，作为兜底停止条件。
- `MaxConsecutiveBadTools` 控制连续未知工具或禁用工具的容忍次数。

如果调用方不传或传零值，Agent 使用默认值。建议默认迭代上限设为 20 左右，避免复杂任务过早中断，同时保留硬上限。

### agent.Event

```go
type Event struct {
	Type     EventType
	Text     string
	Tool    *ToolEvent
	Usage   *UsageEvent
	Progress *ProgressEvent
	Done     *DoneEvent
	Err      error
}
```

`Event` 是 Agent 和 TUI 之间唯一的异步通信载体。相比 Phase 2 的 `Text/Tool/Done/Err` 零值分派，Phase 3 增加显式 `Type`，减少事件解释歧义。

### agent.EventType

```go
type EventType int

const (
	EventText EventType = iota
	EventToolStart
	EventToolResult
	EventUsage
	EventProgress
	EventDone
	EventError
	EventCancelled
)
```

事件类型用于 TUI 分发渲染：

- `EventText`：模型文本增量。
- `EventToolStart`：工具开始执行。
- `EventToolResult`：工具执行结束。
- `EventUsage`：token 用量更新。
- `EventProgress`：阶段变化，如请求模型、执行工具、继续下一轮。
- `EventDone`：正常完成。
- `EventError`：错误停止。
- `EventCancelled`：用户取消。

### agent.ToolEvent

```go
type ToolEvent struct {
	ID      string
	Name    string
	Args    string
	Result  string
	IsError bool
}
```

`ToolEvent` 用于展示工具调用开始和结果。`ID` 对应 LLM tool call ID，用来关联开始和结束事件；`Args` 与 `Result` 都是给 UI 展示的摘要，不替代完整工具输入或完整工具结果。

### agent.UsageEvent

```go
type UsageEvent struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	Available    bool
}
```

`UsageEvent` 用于向 TUI 汇报 token 用量。`Available=false` 表示当前 provider 或当前流式响应无法提供可靠用量，TUI 可以显示为空、隐藏或显示 unavailable。

### agent.ProgressEvent

```go
type ProgressEvent struct {
	Iteration int
	Status    ProgressStatus
	Message   string
}
```

`ProgressEvent` 表示 Agent loop 当前阶段。`Iteration` 从 1 开始；`Message` 是简短可读说明，用于状态栏或日志行。

### agent.ProgressStatus

```go
type ProgressStatus int

const (
	ProgressRequestingModel ProgressStatus = iota
	ProgressCollectingStream
	ProgressExecutingTools
	ProgressContinuing
	ProgressCompleted
	ProgressCancelling
)
```

`ProgressStatus` 给 UI 一个稳定枚举，避免靠字符串判断状态。UI 可以根据枚举决定状态栏文本或样式。

### agent.DoneEvent

```go
type DoneEvent struct {
	Reason     StopReason
	Iterations int
}
```

`DoneEvent` 表示本次 Agent loop 已经结束。它携带停止原因和实际迭代次数，方便 UI 展示和测试断言。

### agent.StopReason

```go
type StopReason int

const (
	StopModelDone StopReason = iota
	StopMaxIterations
	StopCancelled
	StopBadToolLimit
	StopStreamError
	StopToolError
)
```

`StopReason` 统一表示 loop 为什么停止：

- `StopModelDone`：模型输出最终答复且没有工具调用。
- `StopMaxIterations`：达到迭代上限。
- `StopCancelled`：用户取消。
- `StopBadToolLimit`：连续未知工具或禁用工具达到限制。
- `StopStreamError`：模型流式请求出错。
- `StopToolError`：工具层出现不可继续的系统性错误。

### agent.ModelResponse

```go
type ModelResponse struct {
	Text       string
	ToolCalls []llm.ToolCall
	Usage      llm.Usage
}
```

`ModelResponse` 是每一轮流式收集器的完整结果：文本用于写入 assistant 消息，`ToolCalls` 用于后续工具执行，`Usage` 用于累计 token 信息。它不会直接暴露给 TUI，TUI 只看 Agent 事件。

### llm.StreamEvent

```go
type StreamEvent struct {
	Text       string
	ToolCall   *ToolCall
	Usage      *Usage
	Done       bool
	FinishReason FinishReason
}
```

Provider 层的流式事件需要在现有 `Text/ToolCall/Done` 基础上扩展：

- `Usage` 表示 provider 能提供的 token 用量片段或最终用量。
- `FinishReason` 表示本轮模型流结束原因，例如正常停止、工具调用、长度限制等。

### llm.Usage

```go
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	Available    bool
}
```

`Usage` 是 provider 无关的 token 用量结构。OpenAI 和 Anthropic 的字段名不同，但在适配层统一成这组字段。

### llm.FinishReason

```go
type FinishReason int

const (
	FinishUnknown FinishReason = iota
	FinishStop
	FinishToolCalls
	FinishLength
	FinishError
)
```

`FinishReason` 用于 Agent 判断本轮流式结束的大致语义。Agent 最终仍以是否收集到工具调用作为是否执行工具的直接依据。

### tools.Safety

```go
type Safety int

const (
	SafetyReadOnly Safety = iota
	SafetySideEffect
)
```

`Safety` 是内置工具的固定安全分类：

- `SafetyReadOnly`：`read_file`、`glob`、`grep`。
- `SafetySideEffect`：`write_file`、`edit_file`、`bash`。

本阶段不做命令内容级别的动态风险判断，`bash` 始终按有副作用工具处理。

### tools.ToolInfo

```go
type ToolInfo struct {
	Tool   Tool
	Safety Safety
}
```

`ToolInfo` 让 Registry 能同时保存工具实例和安全分类。这样 Agent 可以根据当前模式导出可用工具定义，也可以在执行阶段判断某个工具是否被禁用。

### tui.PendingPlan

```go
type PendingPlan struct {
	Content   string
	CreatedAt time.Time
	Consumed  bool
}
```

`PendingPlan` 存在 TUI 会话状态里，不持久化到磁盘。`/plan` 正常完成后写入；`/do` 启动执行后标记为 consumed，并在执行完成、取消或失败后清理。

### tui.Model 新增状态字段

```go
type Model struct {
	// existing fields...
	cancelCurrent context.CancelFunc
	pendingPlan   *PendingPlan
	currentMode   agent.Mode
}
```

TUI 需要保存当前任务的取消函数，以便 ESC 触发取消；保存 pending plan，以便 `/do` 找到最近一次计划；保存当前 mode，用于状态栏或事件渲染。

## 模块设计

### agent

**职责：**

`agent` 是 Phase 3 的核心编排层，负责把一次用户任务推进成完整 ReAct loop。它不理解 TUI 细节，也不直接依赖 OpenAI 或 Anthropic SDK；它只依赖内部的 `llm.Provider`、`conversation.Conversation` 和 `tools.Registry`。

主要职责：

- 根据 `RunOptions` 决定本次运行模式和停止上限。
- 每次迭代调用 provider 发起流式请求。
- 使用流式收集器同时推送文本事件和累积完整响应。
- 把 assistant 文本或 assistant tool calls 写入 conversation。
- 调用工具调度器执行本轮全部工具调用。
- 将每个 tool result 写入 conversation。
- 判断模型完成、达到上限、取消、连续坏工具、流错误等停止条件。
- 对外只暴露统一事件流。

**对外接口：**

```go
type Agent struct {
	provider llm.Provider
	registry *tools.Registry
}

func New(p llm.Provider, r *tools.Registry) *Agent

func (a *Agent) Run(
	ctx context.Context,
	conv *conversation.Conversation,
	opts RunOptions,
) <-chan Event
```

`Run` 替代当前不带 options 的单轮版本。调用方通过 `ctx` 取消当前任务，通过 `RunOptions` 控制 Plan/Execute 模式和停止上限。

**内部组件：**

```go
func (a *Agent) runLoop(ctx context.Context, conv *conversation.Conversation, opts RunOptions, events chan<- Event)

func (a *Agent) collectModelResponse(ctx context.Context, stream <-chan llm.StreamEvent, errs <-chan error, events chan<- Event, iteration int) (ModelResponse, error)

func (a *Agent) executeToolCalls(ctx context.Context, calls []llm.ToolCall, mode Mode, events chan<- Event) ([]llm.ToolResult, int)
```

- `runLoop` 只处理 ReAct 状态机。
- `collectModelResponse` 是“双路”流式收集器：一边转发文本/用量事件，一边累积完整文本和工具调用。
- `executeToolCalls` 处理工具过滤、分批、并发只读、串行副作用，并返回工具结果和坏工具数量。

**依赖：**

```text
agent -> llm
agent -> tools
agent -> conversation
agent -> context/sync/strings/errors
```

### llm

**职责：**

`llm` 继续提供 provider 无关抽象，并把 Anthropic/OpenAI 的协议差异收敛成统一流式事件。Phase 3 中它不负责 ReAct loop，也不执行工具；它只负责把远端流式响应准确翻译成内部事件。

主要职责：

- 将内部 `Message` 转成各 provider 的请求消息。
- 将内部 `ToolDefinition` 转成 provider 的工具定义格式。
- 解析流式文本增量。
- 解析一个响应里的多个工具调用。
- 在 provider 支持时提取 token usage。
- 输出统一 `StreamEvent` 和错误。

**对外接口：**

```go
type Provider interface {
	Name() string
	Model() string
	Stream(ctx context.Context, msgs []Message, tools []ToolDefinition) (<-chan StreamEvent, <-chan error)
}
```

`Provider` 方法形态保持兼容，减少上层改动；扩展点放在 `StreamEvent` 字段里。

**关键实现要求：**

- OpenAI 流式响应里可能用 index 表示多个 tool call 的并行增量，适配器需要按 index 分别累积 ID、Name、Arguments。
- Anthropic 流式响应里每个 `tool_use` 是独立 content block，适配器需要在 block stop 时吐出完整 `ToolCall`。
- 两个 provider 都应该允许同一轮吐出多个 `ToolCall` 事件，Agent 会收集全部。
- usage 如果流中拿不到，必须返回 `Available=false`，不能伪造数字。

**依赖：**

```text
llm -> config
llm -> prompt
llm -> provider SDK
```

### tools

**职责：**

`tools` 继续负责内置工具定义、注册、查找和执行。Phase 3 需要在 Registry 中补充工具安全分类，使 Agent 可以按模式导出工具定义，并按安全性调度执行。

主要职责：

- 保存工具实例和安全分类。
- 根据模式返回可见工具定义。
- 判断工具是否存在、是否允许在当前模式中使用。
- 执行工具并保持超时、panic 捕获、结构化错误语义。

**对外接口：**

```go
func (r *Registry) Register(t Tool)

func (r *Registry) RegisterWithSafety(t Tool, safety Safety)

func (r *Registry) Get(name string) (Tool, bool)

func (r *Registry) Safety(name string) (Safety, bool)

func (r *Registry) ToToolDefinitions() []llm.ToolDefinition

func (r *Registry) ToToolDefinitionsBySafety(allowed map[Safety]bool) []llm.ToolDefinition

func (r *Registry) Execute(ctx context.Context, name string, args map[string]interface{}) Result
```

`Register` 保持向后兼容，内部可以默认按工具名推断安全分类；`RegisterWithSafety` 给测试或未来扩展显式指定分类。

**安全分类策略：**

```text
SafetyReadOnly:
  read_file
  glob
  grep

SafetySideEffect:
  write_file
  edit_file
  bash
```

**依赖：**

```text
tools -> llm
tools -> context/time/fmt
```

### conversation

**职责：**

`conversation` 继续保存内部统一消息历史。Phase 3 不需要把 conversation 变成复杂状态机，但需要支持更完整地写入一次 assistant 响应：同一条 assistant 消息可能同时有文本和多个工具调用。

主要职责：

- 保存用户消息、助手文本、助手工具调用、工具结果。
- 返回完整历史给 provider。
- 支持 Plan Mode 注入执行计划相关用户消息。

**对外接口：**

```go
func (c *Conversation) AddUser(text string)

func (c *Conversation) AddAssistant(text string)

func (c *Conversation) AddAssistantWithToolCalls(content string, toolCalls []llm.ToolCall)

func (c *Conversation) AddToolResult(result llm.ToolResult)

func (c *Conversation) Messages() []llm.Message
```

当前 `AddAssistantWithToolCalls` 只有 tool calls，没有 content。Phase 3 建议扩展为同时接收 `content`，这样模型在工具调用前输出的文字不会丢失。

**依赖：**

```text
conversation -> llm
```

### tui

**职责：**

`tui` 负责用户交互和可视化状态，不再参与 Agent 内部编排。Phase 3 中它需要解析 `/plan`、`/do`、`/exit`，保存 pending plan，启动带取消能力的 Agent loop，并消费更丰富的 Agent 事件。

主要职责：

- 普通输入：作为用户任务加入 conversation，启动 Execute 模式。
- `/plan`：把用户计划请求加入 conversation，启动 Plan 模式，只读工具。
- `/do`：检查 pending plan，注入执行计划请求，启动 Execute 模式。
- ESC：调用当前任务 cancel 函数，等待 Agent 发出取消事件或完成事件。
- 渲染文本、工具开始、工具结果、用量、进度、完成、错误、取消。
- 在 `/plan` 正常完成后保存 assistant 计划文本为 pending plan。
- 在 `/do` 完成、取消或失败后清理 pending plan。

**对外接口：**

TUI 仍然以 Bubble Tea `Model` 为主，不对其他包暴露新接口。内部新增帮助函数：

```go
func (m Model) startAgentRun(input string, mode agent.Mode, opts agent.RunOptions) (Model, tea.Cmd)

func (m Model) handleSlashCommand(input string) (Model, tea.Cmd, bool)

func (m Model) cancelAgentRun() (Model, tea.Cmd)
```

**依赖：**

```text
tui -> agent
tui -> conversation
tui -> llm
tui -> tools
tui -> bubbletea/bubbles/glamour
```

### prompt

**职责：**

`prompt` 目前提供固定 system prompt。Phase 3 至少需要让模型理解 ReAct loop 和 Plan Mode 的行为规则，但不做复杂 prompt builder。

主要职责：

- 更新系统提示，说明模型可以连续使用工具直到任务完成。
- 说明 Plan Mode 下只能使用只读工具并输出计划。
- 说明 Execute 模式下应基于工具结果继续推进任务，完成后总结。
- 说明遇到工具错误时应读错误并调整策略。

**对外接口：**

保持现有 `prompt.SystemPrompt` 使用方式，不新增公开接口。

**依赖：**

```text
prompt -> embed
```

### cmd/onecode

**职责：**

入口层继续负责初始化配置、provider、registry 和 TUI。Phase 3 中只需要在注册工具时确保安全分类生效。

**对外接口：**

无新增公开接口。

**依赖：**

```text
cmd/onecode -> config
cmd/onecode -> llm
cmd/onecode -> tools
cmd/onecode -> tui
```

## 模块交互

### 普通执行流程

普通用户输入走 Execute 模式，完整调用链如下：

```text
tui.Update
  -> 读取输入框内容
  -> conv.AddUser(input)
  -> context.WithCancel(context.Background())
  -> 保存 cancelCurrent
  -> agent.Run(ctx, conv, RunOptions{Mode: ModeExecute})
  -> stateStreaming
  -> waitForAgentEvent

agent.Run
  -> normalize RunOptions 默认值
  -> runLoop

runLoop iteration 1
  -> events <- ProgressRequestingModel
  -> registry.ToToolDefinitionsBySafety(all)
  -> provider.Stream(ctx, conv.Messages(), toolDefs)
  -> collectModelResponse

collectModelResponse
  -> 收到 llm.StreamEvent.Text
       -> 累积到 ModelResponse.Text
       -> events <- EventText
  -> 收到 llm.StreamEvent.ToolCall
       -> append 到 ModelResponse.ToolCalls
  -> 收到 llm.StreamEvent.Usage
       -> 合并 usage
       -> events <- EventUsage
  -> 收到 Done
       -> 返回完整 ModelResponse

runLoop
  -> 如果没有 ToolCalls
       -> conv.AddAssistant(response.Text)
       -> events <- EventDone{Reason: StopModelDone}
       -> 结束
  -> 如果存在 ToolCalls
       -> conv.AddAssistantWithToolCalls(response.Text, response.ToolCalls)
       -> executeToolCalls
       -> conv.AddToolResult(each result)
       -> 判断停止条件
       -> 下一轮 iteration
```

这个流程中，TUI 永远不直接调用 provider 或 registry。它只处理 `agent.Event`，因此 Agent 内部如何循环不会影响界面结构。

### 多工具调用执行流程

当一轮模型响应包含多个工具调用时，Agent 会先完整收集，再统一执行：

```text
ModelResponse.ToolCalls = [A, B, C, ...]
  -> classify calls
  -> read-only batch
  -> side-effect batch
  -> write all tool results to Conversation
  -> next model iteration
```

执行规则：

1. Agent 按工具名查 Registry。
2. 找不到工具：生成结构化错误结果，计入 bad tool。
3. 找到但当前模式禁用：生成结构化错误结果，计入 bad tool。
4. 允许执行的只读工具进入只读批次。
5. 允许执行的有副作用工具进入副作用批次。
6. 只读批次内部并发执行。
7. 副作用批次按模型返回顺序串行执行。
8. 混合批次时，建议先执行模型返回顺序中连续的只读段，再遇到副作用工具就串行执行；不要把后面的只读工具提前越过副作用工具，以保持顺序可预测。

示例：

```text
[read_file A, grep B, edit_file C, read_file D]

执行顺序：
  batch 1 并发: read_file A + grep B
  batch 2 串行: edit_file C
  batch 3 并发: read_file D
```

这样既能利用只读并发，又不会重排有副作用操作前后的观察关系。

### Plan Mode 流程

`/plan` 不直接执行用户原始输入，而是把它转成计划请求：

```text
用户输入: /plan 帮我优化 grep

tui.handleSlashCommand
  -> 提取目标 "帮我优化 grep"
  -> conv.AddUser("Plan mode: ... 目标是 ...")
  -> startAgentRun(mode=ModePlan)

agent.Run ModePlan
  -> registry.ToToolDefinitionsBySafety(read-only)
  -> 模型可调用 read_file/glob/grep
  -> write_file/edit_file/bash 不出现在工具定义里
  -> 如果模型仍请求禁用工具，回写结构化工具错误
  -> 模型输出最终计划文本

tui receives Done
  -> pendingPlan = PendingPlan{Content: assistantText}
  -> stateIdle
```

Plan Mode 的 conversation 里会保留计划过程和最终计划，因此 `/do` 时模型能看到计划上下文。

### Do Mode 流程

`/do` 只在存在 pending plan 时生效：

```text
用户输入: /do

tui.handleSlashCommand
  -> if pendingPlan == nil:
       -> 打印提示 "没有待执行计划"
       -> 不启动 Agent
  -> else:
       -> conv.AddUser("Execute the pending plan:\n\n" + pendingPlan.Content)
       -> pendingPlan.Consumed = true
       -> startAgentRun(mode=ModeExecute)

agent.Run ModeExecute
  -> 暴露全部工具
  -> 按 ReAct loop 执行计划

tui receives Done/Error/Cancelled
  -> clear pendingPlan
  -> stateIdle
```

这里清理 pending plan 的原则是：只要 `/do` 已经开始消费该计划，无论执行成功、失败还是取消，都不再保留它，避免后续误执行旧计划。

### ESC 取消流程

取消由 TUI 发起，通过 context 传入 Agent、Provider 和 Tool：

```text
用户按 ESC
  -> tui.Update stateStreaming
  -> cancelCurrent()
  -> events 继续监听
  -> 状态栏显示 Cancelling

agent.runLoop
  -> ctx.Done()
  -> events <- EventCancelled / Done{Reason: StopCancelled}
  -> close events

provider.Stream
  -> 看到 ctx.Done()
  -> 停止读取流
  -> 关闭 channel

registry.Execute / tool.Execute
  -> 使用带 cancel 的 ctx
  -> 支持 ctx 的工具尽快退出
```

如果取消发生在工具执行中，Agent 不保证外部命令瞬间终止，但必须保证工具返回后不会再进入下一轮模型请求。

### 连续未知或禁用工具流程

坏工具包括两类：

- Registry 中不存在的工具。
- 当前模式下禁用的工具。

处理流程：

```text
模型请求 bad_tool
  -> Agent 生成 ToolResult{IsError: true, Content: "..."}
  -> conv.AddAssistantWithToolCalls(...)
  -> conv.AddToolResult(...)
  -> consecutiveBadTools++
  -> 如果未达到上限，继续下一轮，让模型纠正
  -> 如果达到上限，EventDone{Reason: StopBadToolLimit}
```

只要某一轮出现至少一个成功允许的工具，`consecutiveBadTools` 重置为 0。这样可以容忍模型偶发犯错，但避免一直请求不存在或禁用工具。

### 流式错误流程

Provider 流式请求出错时，Agent 不继续执行工具，也不进入下一轮：

```text
provider errs <- err
  -> collectModelResponse 返回错误
  -> 如果已经有部分文本，TUI 已经实时显示
  -> events <- EventError
  -> events <- EventDone{Reason: StopStreamError}
  -> runLoop 结束
```

conversation 写入策略：

- 如果流错误发生前已经收到部分文本但没有形成完整响应，不强行写入 assistant 历史，避免把半截回复变成后续上下文事实。
- 如果 provider 已经明确 Done 后才出现统计或关闭错误，则按正常完成处理。

### 迭代上限流程

每次 provider 请求算一次 iteration。执行完某轮工具并准备进入下一轮前检查上限：

```text
iteration == MaxIterations
  -> 如果本轮没有工具调用，按 StopModelDone 正常结束
  -> 如果本轮执行了工具且还需要继续，请求停止
  -> events <- EventDone{Reason: StopMaxIterations}
```

达到上限时，Agent 应给用户一条可读说明，说明已经停止在保护上限处，而不是静默结束。

### Token 用量流

Provider 能拿到 usage 时：

```text
llm.StreamEvent{Usage: ...}
  -> collectModelResponse 合并到 ModelResponse.Usage
  -> agent.Event{Type: EventUsage, Usage: ...}
  -> TUI 更新状态栏或输出摘要
```

Provider 拿不到 usage 时：

```text
llm.Usage{Available: false}
  -> TUI 不显示数字，或显示 usage unavailable
```

Agent 不做价格计算，也不把 unavailable 当成 0。

## 文件组织

Phase 3 会在现有目录基础上拆分 Agent 内部实现，避免继续把 loop、工具执行、事件类型、工具摘要都挤在 `agent.go` 一个文件里。

```text
src/
├── cmd/
│   └── onecode/
│       └── main.go
└── internal/
    ├── agent/
    │   ├── agent.go          — Agent 结构体、New、Run 入口和默认选项
    │   ├── events.go         — Event、EventType、ToolEvent、UsageEvent、ProgressEvent、DoneEvent
    │   ├── loop.go           — ReAct loop 主状态机、停止条件判断
    │   ├── collector.go      — 流式收集器：实时转发文本，同时累积 ModelResponse
    │   ├── scheduler.go      — 多工具调用分批、只读并发、有副作用串行、坏工具统计
    │   └── format.go         — 参数摘要、结果摘要、停止原因文本等展示辅助
    ├── conversation/
    │   └── conversation.go   — 扩展 assistant tool_calls 写入，支持 content + tool calls
    ├── llm/
    │   ├── provider.go       — 扩展 StreamEvent、Usage、FinishReason
    │   ├── openai.go         — 支持多个 tool call 增量收集、usage 映射
    │   └── anthropic.go      — 支持多个 tool_use block、usage 映射
    ├── prompt/
    │   └── system.txt        — 更新 ReAct loop、Plan Mode、工具错误处理指令
    ├── tools/
    │   ├── tool.go           — 新增 Safety 类型或相关说明
    │   ├── registry.go       — ToolInfo、安全分类、按安全范围导出工具定义
    │   ├── read_file.go      — 工具语义保持不变
    │   ├── write_file.go     — 工具语义保持不变
    │   ├── edit_file.go      — 工具语义保持不变
    │   ├── bash.go           — 工具语义保持不变，安全分类为 side effect
    │   ├── glob.go           — 工具语义保持不变
    │   ├── grep.go           — 工具语义保持不变
    │   └── searchutil/       — 保持 Phase 2 抽出的搜索工具公共实现
    └── tui/
        ├── model.go          — slash 命令、pending plan、cancel、事件消费
        └── styles.go         — 如需区分进度/取消/错误，可补充样式
```

测试文件建议按模块补充：

```text
src/
└── internal/
    ├── agent/
    │   ├── loop_test.go       — 多轮循环、迭代上限、坏工具停止、取消
    │   ├── collector_test.go  — 双路收集、文本增量、多个 tool call、usage
    │   └── scheduler_test.go  — 只读并发、副作用串行、混合批次顺序
    ├── tools/
    │   └── registry_test.go   — 安全分类、按模式导出工具定义
    └── conversation/
        └── conversation_test.go — assistant content + tool calls 写入
```

本阶段不新增独立的 `mode` 包或 `runtime` 包，原因是这些概念目前只服务 Agent loop。先放在 `internal/agent` 内部更符合当前规模；如果后续权限系统、任务队列、上下文压缩都接入，再考虑抽更上层的运行时包。

文件职责对应 spec：

- `agent/loop.go` 覆盖 F1、F2、F7、F8、F9。
- `agent/collector.go` 覆盖 F5、F6。
- `agent/scheduler.go` 覆盖 F3、F4。
- `tools/registry.go` 覆盖 F4、F10、F12。
- `tui/model.go` 覆盖 F5、F8、F10、F11。
- `llm/openai.go` 与 `llm/anthropic.go` 覆盖 F13。

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| Agent 架构 | 在 `internal/agent` 内实现 Agent Runtime，不新增顶层 runtime 包 | 当前 loop、collector、scheduler 都只服务 Agent；放在 agent 包内边界清晰，避免过早抽象 |
| Provider 接口 | 保持 `Provider.Stream(ctx, msgs, tools)` 方法签名，扩展 `llm.StreamEvent` 字段 | 减少现有调用方改动，把 usage、finish reason、多 tool call 作为流事件能力增强 |
| 流式收集 | Agent 层实现 collector，Provider 只负责协议翻译 | Provider 不应该理解 ReAct loop；collector 同时满足实时 UI 和完整响应判断 |
| 多工具调用 | 同一轮响应先完整收集全部 tool calls，再执行工具 | 保证 assistant tool_calls 和 tool_results 一一对应，也便于统一分批和停止判断 |
| 工具并发策略 | 只读工具并发，有副作用工具串行；混合批次不跨越副作用工具重排 | 提升只读搜索/读取效率，同时避免写入和命令执行导致不可预测状态 |
| 工具安全分类 | Registry 保存固定 `Safety` 分类，`bash` 永远视为有副作用 | 本阶段不做权限系统和动态风险判断，固定分类简单、可测试、符合 spec 边界 |
| Plan Mode | `/plan` 和 `/do` 由 TUI 解析，Agent 只接收 Mode 和 RunOptions | Slash 命令是交互层概念；Agent 保持通用，只理解运行模式和工具范围 |
| Pending Plan | 只保存在 TUI 内存状态，不落盘；`/do` 开始消费后完成/失败/取消都清理 | 符合“不做持久化计划”；避免旧计划被误执行 |
| ESC 取消 | TUI 保存 `context.CancelFunc`，通过 ctx 传递给 Agent、Provider、Tool | Go 原生取消链路清晰；不需要额外全局状态 |
| 迭代上限 | 默认较宽松，建议 20；支持 RunOptions 覆盖 | 复杂编码任务需要多步，过小会误伤；仍需要硬上限防止无限循环 |
| 坏工具处理 | 未知工具和禁用工具先作为 tool_result 错误回写，连续达到上限再停止 | 给模型自我修正机会，同时防止重复无效调用 |
| 流式错误写历史 | 未完整 Done 的半截响应不写入 conversation | 避免把不完整输出变成后续上下文事实；UI 仍保留用户已看到的部分 |
| Conversation 扩展 | `AddAssistantWithToolCalls` 同时接收 content 和多个 tool calls | 保留模型在工具调用前输出的文字，符合 OpenAI/Anthropic 都可能出现文本+工具块的现实 |
| Token 用量 | 增加统一 `Usage`，不可用时显式 `Available=false` | 避免把拿不到 usage 误解为 0；为后续统计保留扩展点 |
| 测试策略 | 用 mock provider 和 fake tools 测 Agent loop，不依赖真实 API | 核心编排逻辑可稳定、快速验证；真实 provider 只做少量 smoke 验证 |

## Plan 覆盖检查

| Spec 需求 | 设计归属 |
|-----------|----------|
| F1 多轮 ReAct 循环 | `agent/loop.go`、`RunOptions`、`StopReason` |
| F2 工具结果回写 | `conversation` 扩展、`agent.runLoop` |
| F3 多工具调用处理 | `ModelResponse.ToolCalls`、`agent/scheduler.go` |
| F4 按安全性分批执行工具 | `tools.Safety`、`ToolInfo`、scheduler 分批策略 |
| F5 统一异步事件流 | `agent/events.go` |
| F6 流式收集器双路输出 | `agent/collector.go` |
| F7 停止条件 | `StopReason`、`RunOptions`、`agent/loop.go` |
| F8 ESC 取消当前任务 | TUI `cancelCurrent`、ctx 取消链路 |
| F9 未知工具与禁用工具处理 | scheduler bad tool 统计、结构化 tool result |
| F10 Plan Mode 两段式 | TUI slash 命令、`agent.Mode`、Registry 按安全分类导出 |
| F11 Plan Mode 用户体验 | `PendingPlan`、`/do` 注入执行计划 |
| F12 与现有工具兼容 | `Register` 兼容、工具语义不变 |
| F13 跨 Provider 行为一致 | `llm.StreamEvent` 扩展、OpenAI/Anthropic 适配 |

当前设计没有发现未覆盖的 spec 功能需求。依赖方向仍保持单向：`tui -> agent -> llm/tools/conversation`，`llm` 不依赖 `agent` 或 `tools` 执行逻辑，`tools` 不依赖 TUI。

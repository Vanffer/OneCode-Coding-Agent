# 上下文管理能力 Plan

## 架构概览

本阶段把 `conversation` 包从“简单消息列表容器”扩展为“对话上下文管理模块”。上下文管理的核心对象就是对话历史，因此工具结果轻量处理、历史压缩、近期原文保留、近期文件索引、Token 估算和项目内上下文产物存储都归到 `conversation` 包内。

Agent Loop 不直接实现压缩策略，只在关键时机调用 `conversation` 提供的上下文管理能力，并把结果转换成 Agent Event 发给 TUI。TUI 不理解压缩算法，只负责展示上下文状态、压缩状态，并提供用户命令和可视化配置入口。

整体结构分为四层：

### 1. Conversation Context Management

`conversation` 包是上下文管理的核心层，负责维护和变换对话历史：

- 保存原有多轮消息列表。
- 在请求模型前执行工具结果轻量处理。
- 根据上下文窗口预算判断是否需要重量压缩。
- 调用专用压缩请求生成结构化摘要。
- 用摘要和近期原文消息替换旧历史。
- 维护近期访问或编辑过的重要文件索引。
- 维护 Token 使用量估算和最近一次真实 usage 锚点。
- 将大工具结果保存到当前项目的 OneCode 管理目录。
- 读写当前项目本地的上下文窗口覆盖配置。

建议文件组织：

```text
src/internal/conversation/
├── conversation.go      # 原有 Conversation，继续管理消息列表和基础追加操作
├── context.go           # 上下文管理总入口：preflight、manual compact、emergency compact
├── bounder.go           # 工具结果轻量处理
├── estimator.go         # Token 估算和 usage 锚点
├── window.go            # 上下文窗口解析和来源标记
├── store.go             # 项目内上下文产物存储
├── compactor.go         # 重量压缩流程
├── summary.go           # 摘要提示词、摘要解析、边界消息
└── files.go             # 近期文件索引
```

`conversation` 包不依赖 TUI，也不直接发送 UI 事件。它只返回结构化结果，例如当前上下文用量、是否发生轻量处理、是否触发压缩、压缩是否成功、保存了哪些工具结果、是否进入自动压缩熔断状态。

### 2. Agent Integration

`agent` 包负责把上下文管理接入现有 ReAct Loop：

- 每次正常请求模型前，调用 `conversation` 的 preflight。
- preflight 先做工具结果轻量处理，再根据窗口预算判断是否需要自动重量压缩。
- preflight 返回的状态变化转换为 Agent Event，发给 TUI。
- 收到模型服务返回的上下文过长错误时，调用紧急压缩；成功后重试原请求一次。
- 每次模型返回真实 usage 后，把 usage 锚点回写给 `conversation`。
- `/compact` 触发的手动压缩通过 Agent 调用 `conversation`，并同样转换成事件。

现有接入点是 `loop.go` 中每次 `provider.Stream` 之前，以及 `collector.go` 收到 usage 事件之后。

### 3. LLM Compression Request

重量压缩需要再次调用模型，但这次调用和正常 Agent 请求不同：

- 不传工具定义，确保模型不能调用工具。
- 使用专门的压缩提示词。
- 压缩输入受更严格预算约束，避免压缩请求自身也超限。
- 要求模型输出草稿区和正式摘要区。
- 系统只解析并保留正式摘要区，草稿区丢弃。
- 压缩后的对话历史包含摘要边界消息、近期文件索引和近期原文消息。

为了避免 `conversation` 直接依赖具体 provider，它只依赖一个很小的压缩接口；`agent` 在创建上下文管理能力时，把当前 provider 适配成这个接口。

### 4. TUI Context Surface

`tui` 包负责用户可见部分：

- 状态栏常驻展示上下文使用量，例如 `Context ~42k / 200k · 21%`。
- 当窗口大小来自推断或默认值时，用 `~` 或类似标记提示不是精确配置值。
- 任意方式触发压缩时，状态栏或提示行展示压缩开始、完成、失败、紧急重试、自动压缩熔断等状态。
- 新增 `/compact` 手动压缩命令。
- 新增上下文窗口设置入口，用于可视化查看和调整当前项目的窗口大小，并保存到项目本地配置。

TUI 不直接改写对话历史，只通过 Agent 暴露的命令或事件驱动上下文管理。

## 核心数据结构与接口

### Conversation

`Conversation` 继续是对话历史的主对象，但会从简单消息容器升级为带上下文状态的会话对象。

```go
type Conversation struct {
    messages []llm.Message

    context *ContextState
}
```

职责：

- 保存完整的当前 LLM 可见消息列表。
- 追加用户消息、助手消息、工具调用消息和工具结果消息。
- 在上下文压缩后替换消息历史。
- 暴露当前上下文状态，用于 Agent 和 TUI 展示。
- 记录最近一次真实 usage，用于 Token 估算锚点。

### ContextState

`ContextState` 保存上下文管理运行时状态。

```go
type ContextState struct {
    ProjectRoot string
    Window      WindowInfo
    Usage       UsageEstimate
    Fuse        CompactFuse
    Store       *ProjectStore
    Files       *FileIndex
}
```

字段含义：

- `ProjectRoot`: 当前项目根目录。
- `Window`: 当前上下文窗口大小及来源。
- `Usage`: 当前上下文使用量估算。
- `Fuse`: 自动压缩熔断状态。
- `Store`: 项目内上下文产物存储。
- `Files`: 近期文件索引。

这个状态属于当前 `Conversation`，不做跨会话共享。

### WindowInfo

`WindowInfo` 描述上下文窗口大小和来源。

```go
type WindowInfo struct {
    Limit  int
    Source WindowSource
}
```

```go
type WindowSource int

const (
    WindowSourceLocal WindowSource = iota
    WindowSourceProvider
    WindowSourceInferred
    WindowSourceDefault
)
```

含义：

- `Local`: 用户在当前项目本地配置里覆盖。
- `Provider`: provider 配置里显式声明。
- `Inferred`: 系统根据 provider/model 推断。
- `Default`: 无法识别时使用保守默认值。

TUI 根据 `Source` 决定是否显示估算标记。

### UsageEstimate

`UsageEstimate` 描述当前上下文使用量。

```go
type UsageEstimate struct {
    Used       int
    Limit      int
    Percent    int
    Estimated  bool
    Anchor     UsageAnchor
    UpdatedAt  time.Time
}
```

```go
type UsageAnchor struct {
    MessageCount int
    Usage        llm.Usage
}
```

设计要点：

- `Used` 是当前估算的上下文 token 用量。
- `Limit` 来自 `WindowInfo.Limit`。
- `Estimated=true` 表示当前结果包含近似估算。
- `Anchor` 记录最近一次模型返回的真实 usage，以及当时对话消息数量。
- 后续新增消息只做增量估算。

### CompactFuse

`CompactFuse` 控制自动压缩失败熔断。

```go
type CompactFuse struct {
    ConsecutiveFailures int
    Tripped             bool
}
```

规则：

- 自动压缩失败一次，`ConsecutiveFailures` 加一。
- 自动压缩成功后清零。
- 连续失败达到 3 次后，`Tripped=true`。
- 自动压缩熔断后，不再自动压缩。
- 手动压缩和紧急压缩仍可以尝试，但失败要返回清晰状态。

### ProjectStore

`ProjectStore` 管理当前项目内上下文产物。

```go
type ProjectStore struct {
    ProjectRoot string
    ContextDir  string
}
```

职责：

- 创建 `.onecode/context/`。
- 创建 `.onecode/context/tool-results/`。
- 保存完整工具结果。
- 读写 `.onecode/context/local.yaml` 中的上下文窗口本地覆盖配置。
- 维护 `.onecode/context/.gitignore`，避免产物默认进入版本控制。

工具结果保存记录：

```go
type StoredToolResult struct {
    ToolUseID string
    Path      string
    Bytes     int
    Preview   string
}
```

### FileIndex

`FileIndex` 记录近期访问、编辑或由工具结果引用的重要文件。

```go
type FileIndex struct {
    Entries []FileIndexEntry
}
```

```go
type FileIndexEntry struct {
    Path       string
    Preview    string
    Reason     string
    LastSeenAt time.Time
}
```

来源：

- `read_file` 工具读取过的路径。
- `write_file` 或 `edit_file` 修改过的路径。
- 工具结果中明显引用到的项目内路径。
- 被保存的大工具结果路径。

FileIndex 只提供导航线索，不替代真实文件内容。压缩摘要中只放有限数量、有限长度的文件预览。

### ContextOptions

`ContextOptions` 是上下文管理配置。

```go
type ContextOptions struct {
    ProjectRoot              string
    ProviderName             string
    ModelName                string
    ProviderWindow           int
    ToolResultMaxTokens      int
    ToolResultBatchMaxTokens int
    RecentTokens             int
    RecentMinMessages        int
    SummaryReserveTokens     int
    AutoSafetyMarginTokens   int
    ManualSafetyMarginTokens int
    ForceSafetyMarginTokens  int
    MaxCompactFailures       int
}
```

默认值：

```text
ToolResultMaxTokens:      8k
ToolResultBatchMaxTokens: 16k
RecentTokens:             10k
RecentMinMessages:        5
SummaryReserveTokens:     20k
AutoSafetyMarginTokens:   13k
ManualSafetyMarginTokens: 3k
ForceSafetyMarginTokens:  3k
MaxCompactFailures:       3
```

这些默认值可以在 plan 阶段固定，后续如需暴露配置再扩展。

### PreflightResult

`PreflightResult` 是每次请求模型前的上下文检查结果。

```go
type PreflightResult struct {
    Usage              UsageEstimate
    BoundedToolResults []StoredToolResult
    Compacted          bool
    CompactMode        CompactMode
    Statuses           []ContextStatus
}
```

用于 Agent 转换成事件发送给 TUI。

### CompactMode

```go
type CompactMode int

const (
    CompactModeAuto CompactMode = iota
    CompactModeManual
    CompactModeEmergency
    CompactModeForce
)
```

含义：

- `Auto`: 正常请求前自动触发。
- `Manual`: 用户执行 `/compact`。
- `Emergency`: 收到上下文过长错误后的补救压缩。
- `Force`: 接近硬上限时的强制压缩。

### ContextStatus

`ContextStatus` 表示上下文管理过程中的状态变化。

```go
type ContextStatus struct {
    Kind    ContextStatusKind
    Message string
    Usage   UsageEstimate
}
```

```go
type ContextStatusKind int

const (
    ContextStatusUsageUpdated ContextStatusKind = iota
    ContextStatusToolResultStored
    ContextStatusCompactStarted
    ContextStatusCompactCompleted
    ContextStatusCompactFailed
    ContextStatusCompactFuseTripped
    ContextStatusEmergencyRetry
)
```

Agent 把这些状态转成 `agent.Event`，TUI 只消费事件，不依赖 `conversation` 内部结构。

### Compressor

`Compressor` 是 `conversation` 使用的压缩接口，避免 `conversation` 直接依赖具体 provider。

```go
type Compressor interface {
    Summarize(ctx context.Context, input CompactInput) (CompactOutput, error)
}
```

### CompactInput

```go
type CompactInput struct {
    Messages      []llm.Message
    FileIndex     []FileIndexEntry
    StoredResults []StoredToolResult
    BudgetTokens  int
    Mode          CompactMode
}
```

### CompactOutput

```go
type CompactOutput struct {
    Summary string
    Usage   llm.Usage
}
```

Agent 会把当前 provider 包装成 `Compressor`。压缩请求内部不传工具定义，并使用专门 prompt。

## 模块设计

### conversation.Conversation

**职责：**

`Conversation` 是对话历史和上下文状态的聚合根。它继续提供现有的消息追加能力，同时新增上下文管理入口。

**对外接口：**

```go
func New(opts ...Option) *Conversation

func (c *Conversation) AddUser(text string)
func (c *Conversation) AddAssistant(text string)
func (c *Conversation) AddAssistantWithToolCalls(content string, toolCalls []llm.ToolCall)
func (c *Conversation) AddToolResult(result llm.ToolResult)

func (c *Conversation) Messages() []llm.Message
func (c *Conversation) MessageCount() int
func (c *Conversation) Clear()

func (c *Conversation) ContextState() ContextState
func (c *Conversation) UpdateUsage(usage llm.Usage)
func (c *Conversation) Preflight(ctx context.Context, compressor Compressor, opts ContextOptions) (PreflightResult, error)
func (c *Conversation) Compact(ctx context.Context, compressor Compressor, mode CompactMode, opts ContextOptions) (CompactResult, error)
func (c *Conversation) SetContextWindow(ctx context.Context, limit int) (WindowInfo, error)
```

**设计说明：**

- `AddToolResult` 只追加原始结果，不立刻存盘，避免工具执行路径承担上下文策略。
- `Preflight` 在请求模型前统一做轻量处理和必要压缩。
- `Compact` 给 `/compact` 和紧急压缩复用。
- `UpdateUsage` 在模型返回真实 usage 后调用，更新估算锚点。
- `Messages` 返回的是当前 LLM 可见历史；工具结果被轻量处理或历史被压缩后，这里看到的是处理后的消息列表。

### conversation.ToolResultBounder

**职责：**

处理过大的工具结果，满足 F1、F2、F3、F4、F19。

**核心接口：**

```go
type ToolResultBounder struct {
    Store *ProjectStore
}

func (b *ToolResultBounder) Bound(messages []llm.Message, opts BoundOptions) (BoundResult, error)
```

```go
type BoundOptions struct {
    SingleMaxTokens int
    BatchMaxTokens  int
}

type BoundResult struct {
    Messages []llm.Message
    Stored   []StoredToolResult
    Changed  bool
}
```

**处理流程：**

1. 遍历消息列表，找到 `Role == "tool"` 且包含 `ToolResult` 的消息。
2. 跳过已经被标记为“已存盘”的工具结果。
3. 单个工具结果超过单项阈值时，写入 `ProjectStore`。
4. 在消息中替换为预览文本、保存路径、重新读取提示。
5. 再按连续工具结果组或同一轮工具结果组计算合计大小。
6. 如果合计超过单轮阈值，按工具结果大小从大到小继续存盘。
7. 返回新的消息列表和存盘记录。

**设计说明：**

- 为避免重复存盘，替换后的工具结果文本包含稳定 marker。
- Bounder 不处理用户消息和 assistant 消息。
- Bounder 不调用模型。

### conversation.TokenEstimator

**职责：**

估算上下文使用量，满足 F16。

**核心接口：**

```go
type TokenEstimator struct{}

func (e TokenEstimator) Estimate(messages []llm.Message, window WindowInfo, anchor UsageAnchor) UsageEstimate
func (e TokenEstimator) EstimateText(text string) int
func (e TokenEstimator) EstimateMessage(msg llm.Message) int
```

**估算策略：**

- 没有 usage 锚点时，对所有消息做近似估算。
- 有 usage 锚点时，复用锚点的真实 total tokens。
- 对锚点之后新增的消息做近似估算。
- 工具调用参数、工具结果内容、assistant 内容都纳入估算。
- 每条消息保留固定结构开销，避免过度低估。

**默认近似：**

```text
估算 tokens = 字符数 / 4 + 消息固定开销
```

这里是保守近似，不追求 tokenizer 精确一致。

### conversation.WindowResolver

**职责：**

解析上下文窗口大小，满足 F14、F15。

**核心接口：**

```go
type WindowResolver struct {
    Store *ProjectStore
}

func (r WindowResolver) Resolve(ctx context.Context, opts ContextOptions) (WindowInfo, error)
func InferWindow(providerName, modelName string) (WindowInfo, bool)
```

**优先级：**

1. 当前项目本地覆盖值。
2. provider 配置显式值。
3. 根据 provider/model 推断。
4. 保守默认值。

**初始推断表：**

```text
claude-3.5 / claude-3-5 / claude-3-7 / claude-sonnet / claude-opus: 200k
gpt-4.1 / gpt-4o / o 系列: 128k
未知模型: 128k 默认
```

推断表只作为体验优化，不作为准确性承诺。TUI 应显示估算标记。

### conversation.ProjectStore

**职责：**

管理项目内上下文产物，满足 F2、F8、F15、N8。

**核心接口：**

```go
type ProjectStore struct {
    ProjectRoot string
    ContextDir  string
}

func NewProjectStore(projectRoot string) *ProjectStore

func (s *ProjectStore) Ensure(ctx context.Context) error
func (s *ProjectStore) StoreToolResult(ctx context.Context, result llm.ToolResult, preview string) (StoredToolResult, error)

func (s *ProjectStore) LoadLocalConfig(ctx context.Context) (LocalConfig, bool, error)
func (s *ProjectStore) SaveLocalConfig(ctx context.Context, cfg LocalConfig) error
```

```go
type LocalConfig struct {
    ContextWindow int `yaml:"context_window"`
}
```

**路径策略：**

```text
.onecode/context/
.onecode/context/.gitignore
.onecode/context/local.yaml
.onecode/context/tool-results/
```

**写入规则：**

- 所有写入路径必须在 `<project>/.onecode/context/` 下。
- `tool-results` 下文件名包含时间戳和 tool use id。
- 文件名需要清理非法字符。
- `.gitignore` 默认忽略 `tool-results/` 和 `local.yaml`。
- 保存失败时返回错误，不吞掉。

### conversation.FileIndex

**职责：**

记录近期重要文件，满足 F8 和 AC6。

**核心接口：**

```go
type FileIndex struct {
    Entries []FileIndexEntry
}

func (idx *FileIndex) ObserveToolCall(call llm.ToolCall, result llm.ToolResult)
func (idx *FileIndex) ObserveStoredToolResult(stored StoredToolResult)
func (idx *FileIndex) Recent(limit int) []FileIndexEntry
```

**观察规则：**

- `read_file` 的 `path` 进入索引，preview 从工具结果前若干行提取。
- `write_file`、`edit_file` 的 `path` 进入索引，reason 标记为 edited。
- 大工具结果存盘路径进入索引，reason 标记为 stored tool result。
- 对工具结果文本中的项目内路径做简单提取，作为弱信号。
- 同一路径重复出现时更新 preview、reason 和时间。

**限制：**

- 单条 preview 控制长度。
- 索引最多保留最近若干条。
- 不把完整大文件塞进摘要。

### conversation.Compactor

**职责：**

执行重量压缩，满足 F5、F6、F7、F8、F9、F10、F11、F12、F13。

**核心接口：**

```go
type Compactor struct {
    Estimator TokenEstimator
}

func (c Compactor) ShouldCompact(usage UsageEstimate, window WindowInfo, mode CompactMode, opts ContextOptions, fuse CompactFuse) bool
func (c Compactor) Compact(ctx context.Context, messages []llm.Message, state ContextState, compressor Compressor, mode CompactMode, opts ContextOptions) (CompactResult, error)
```

```go
type CompactResult struct {
    Messages []llm.Message
    Summary  string
    Usage    UsageEstimate
    Statuses []ContextStatus
}
```

**触发阈值：**

```text
自动压缩阈值 = window - SummaryReserveTokens - AutoSafetyMarginTokens
手动压缩阈值 = window - SummaryReserveTokens - ManualSafetyMarginTokens
强制压缩阈值 = window - ForceSafetyMarginTokens
```

**保留策略：**

- 从尾部按 token 预算保留近期原文。
- 至少保留 `RecentMinMessages` 条最近消息。
- 优先保留最近用户消息、最近 assistant 回复、最近工具调用和工具结果。
- 早期消息交给摘要覆盖。

**压缩输出：**

压缩后的消息列表结构：

```text
user: <context-summary-boundary>
      结构化摘要
      近期文件索引
      重新读取提醒
assistant/user/tool: 最近原文消息...
```

边界消息使用 user role，是为了让 provider 转换层不需要新增系统消息历史类型，同时明确这是后续请求可见的运行时上下文。

### conversation.SummaryBuilder

**职责：**

生成压缩请求内容、解析正式摘要、构造边界消息。

**核心接口：**

```go
type SummaryBuilder struct{}

func (b SummaryBuilder) BuildInput(messages []llm.Message, fileIndex []FileIndexEntry, stored []StoredToolResult, budget int, mode CompactMode) CompactInput
func (b SummaryBuilder) ParseOutput(raw string) (summary string, err error)
func (b SummaryBuilder) BoundaryMessage(summary string, fileIndex []FileIndexEntry) llm.Message
```

**摘要结构：**

```text
## Task Goal
## User Constraints
## Completed Work
## Key Decisions
## Important Files And Paths
## Open Items
## Risks
## Next Steps
```

**草稿与正式摘要格式：**

```text
<analysis_draft>
...
</analysis_draft>

<formal_summary>
...
</formal_summary>
```

只保留 `<formal_summary>` 内的内容。

### agent.ContextEvent Bridge

**职责：**

把 `conversation` 返回的状态变成 TUI 可消费的 Agent Event。

**核心扩展：**

```go
const (
    EventContext EventType = ...
)

type ContextEvent struct {
    Kind    ContextEventKind
    Message string
    Usage   conversation.UsageEstimate
}
```

Agent 在以下时机发送 context event：

- preflight 后 usage 更新。
- 工具结果被存盘。
- 压缩开始。
- 压缩完成。
- 压缩失败。
- 自动压缩熔断。
- 紧急压缩后准备重试。

### agent.ProviderCompressor

**职责：**

把当前 `llm.Provider` 适配成 `conversation.Compressor`。

**核心接口：**

```go
type providerCompressor struct {
    provider      llm.Provider
    promptRuntime *prompt.Runtime
}

func (c providerCompressor) Summarize(ctx context.Context, input conversation.CompactInput) (conversation.CompactOutput, error)
```

**请求规则：**

- 调用 `provider.Stream`。
- `tools` 参数传 `nil`。
- 使用压缩专用 prompt。
- 收集完整文本和 usage。
- 如果模型返回工具调用，视为压缩失败。
- 如果找不到正式摘要区，视为压缩失败。

### tui.Context Surface

**职责：**

展示上下文状态并提供用户入口。

**主要改动：**

- Model 增加 `contextUsage`、`contextStatus`、`contextWindow` 等字段。
- `statusBar` 增加上下文展示片段。
- 处理 `agent.EventContext`。
- 新增 `/compact` 命令。
- 新增 `/context` 或 `/context window` 命令进入窗口大小设置流程。

**交互建议：**

```text
/context
```

展示当前上下文窗口、来源、使用量和可操作提示。

```text
/context window
```

进入一个简单的数字输入流程，用户输入新的窗口大小，保存到项目本地配置。

为了控制本阶段范围，先用命令式 TUI 流程，不做复杂弹层。

## 模块交互

### 1. 正常 Agent 请求前的 preflight

```text
TUI 用户输入
  -> tui.startAgentRun
  -> conversation.AddUser

agent.Run
  -> agent.runLoop iteration N
  -> conversation.Preflight
       -> ProjectStore.Ensure
       -> WindowResolver.Resolve
       -> ToolResultBounder.Bound
       -> TokenEstimator.Estimate
       -> Compactor.ShouldCompact(auto)
       -> 如需要自动压缩:
            -> Compactor.Compact
                 -> SummaryBuilder.BuildInput
                 -> Compressor.Summarize
                      -> provider.Stream(messages, tools=nil, compactPrompt)
                 -> SummaryBuilder.ParseOutput
                 -> SummaryBuilder.BoundaryMessage
                 -> 替换 conversation.messages
            -> 更新 Fuse / Usage / FileIndex
       -> 返回 PreflightResult
  -> agent 把 PreflightResult.Statuses 转为 EventContext
  -> provider.Stream(conversation.Messages(), normalTools, normalPrompt)
```

这个流程保证每次正常请求前都先处理工具结果，再判断整体上下文是否需要压缩。

### 2. 工具结果回写后的状态更新

```text
provider 返回 tool calls
  -> agent.executeToolCalls
       -> registry.Execute
       -> 返回 []llm.ToolResult
  -> conversation.AddToolResult
       -> 追加原始工具结果
       -> FileIndex.ObserveToolCall
  -> 下一轮 runLoop 开始
       -> conversation.Preflight
       -> ToolResultBounder 决定是否存盘和替换
```

设计上，工具执行阶段仍然保留原始工具结果；轻量处理发生在下一次请求模型前。这样工具执行链路保持纯粹，所有上下文策略集中在 `conversation`。

### 3. usage 锚点更新

```text
provider.Stream 返回 usage event
  -> agent.collectModelResponse
  -> agent.EventUsage 发给 TUI
  -> response.Usage 回到 runLoop
  -> conversation.UpdateUsage(response.Usage)
  -> TokenEstimator 后续以该 usage 作为锚点
```

需要注意：当前 `collector` 已经能收集完整 usage。Plan 中会把 usage 回写放到每次模型响应完成后，而不是只在 TUI 事件里消费。

### 4. 自动压缩

```text
conversation.Preflight
  -> usage >= window - summaryReserve - autoSafetyMargin
  -> fuse 未熔断
  -> ContextStatusCompactStarted
  -> Compactor.Compact(auto)
  -> 成功:
       -> 旧消息替换为 summary boundary + recent messages
       -> fuse 清零
       -> ContextStatusCompactCompleted
  -> 失败:
       -> fuse failures + 1
       -> ContextStatusCompactFailed
       -> 如果 failures >= 3:
            -> fuse tripped
            -> ContextStatusCompactFuseTripped
```

自动压缩失败不会无限重试。熔断后，普通 preflight 只继续轻量处理和 usage 更新，不再自动重量压缩。

### 5. 强制压缩线

```text
conversation.Preflight
  -> usage >= window - forceSafetyMargin
  -> 不管 fuse 是否熔断
  -> Compactor.Compact(force)
```

强制压缩是 API 请求前的最后兜底。它优先级高于自动压缩熔断，用于避免因为自动压缩连续失败后上下文继续增长到硬上限。

如果强制压缩也失败，Agent 不再继续本轮正常模型请求，而是向 TUI 展示错误。这样比直接把必然超限的请求发给 provider 更可控。

### 6. 手动 `/compact`

```text
用户输入 /compact
  -> tui.handleSlashCommand
  -> agent.Compact 或 agent.RunCompact
  -> conversation.Compact(manual)
       -> 使用 manual safety margin
       -> 保留近期原文
       -> 生成结构化摘要和文件索引
       -> 替换 messages
  -> TUI 显示压缩完成或失败
```

手动压缩不需要等到超过自动阈值。它是用户主动整理上下文，因此压缩策略更激进，安全余量更小。

### 7. 紧急压缩与重试

```text
provider.Stream 正常请求
  -> 返回 llm.ContextTooLongError
  -> agent 检测错误类型
  -> ContextStatusCompactStarted(emergency)
  -> conversation.Compact(emergency)
       -> 使用更严格输入预算
       -> 不传工具
       -> 生成摘要
       -> 替换 messages
  -> 成功:
       -> ContextStatusEmergencyRetry
       -> 重试原 provider.Stream 一次
  -> 失败:
       -> EventError
  -> 重试仍 ContextTooLong:
       -> EventError
       -> 不再重试
```

紧急压缩的关键是：压缩请求不是原始请求。它会裁剪压缩输入，保留近期原文和关键索引，用更小的输入请求换取一次可提交的摘要请求。

### 8. 上下文窗口配置

```text
用户输入 /context
  -> TUI 展示当前 WindowInfo + UsageEstimate

用户输入 /context window
  -> TUI 进入窗口大小输入流程
  -> 用户输入数字
  -> conversation.SetContextWindow
       -> ProjectStore.SaveLocalConfig
       -> WindowResolver.Resolve
       -> TokenEstimator.Estimate
  -> TUI 状态栏立刻使用新窗口
```

保存位置是项目本地配置，不默认进入共享配置。

### 9. TUI 状态展示

```text
conversation 返回 ContextStatus
  -> agent.EventContext
  -> tui.Update
       -> 更新 contextUsage
       -> 更新 contextStatus
       -> 必要时 tea.Println 提示
  -> statusBar 渲染：
       provider | run status + context status | context usage | model
```

状态栏保持轻量常驻。压缩开始、完成、失败、熔断、紧急重试等事件可以额外打印一行短提示，避免用户错过。

## 文件组织

```text
src/
├── cmd/
│   └── onecode/
│       └── main.go
│           - 读取合并后的配置
│           - 把 provider 的上下文窗口配置传给 TUI/Agent
│
├── internal/
│   ├── config/
│   │   ├── config.go
│   │   │   - ProviderConfig 增加可选 context_window 字段
│   │   │   - Config 继续加载项目共享配置和用户级配置
│   │   └── config_test.go
│   │       - 覆盖 context_window 字段解析和校验
│   │
│   ├── conversation/
│   │   ├── conversation.go
│   │   │   - Conversation 增加 ContextState
│   │   │   - 保留现有 AddUser/AddAssistant/AddToolResult/Messages 等接口
│   │   │
│   │   ├── context.go
│   │   │   - Preflight
│   │   │   - Compact
│   │   │   - UpdateUsage
│   │   │   - SetContextWindow
│   │   │   - ContextState 查询
│   │   │
│   │   ├── bounder.go
│   │   │   - ToolResultBounder
│   │   │   - BoundOptions / BoundResult
│   │   │   - 工具结果存盘 marker
│   │   │
│   │   ├── estimator.go
│   │   │   - TokenEstimator
│   │   │   - UsageEstimate / UsageAnchor
│   │   │
│   │   ├── window.go
│   │   │   - WindowInfo / WindowSource
│   │   │   - WindowResolver
│   │   │   - 模型窗口推断表
│   │   │
│   │   ├── store.go
│   │   │   - ProjectStore
│   │   │   - StoredToolResult
│   │   │   - LocalConfig
│   │   │   - .onecode/context 目录管理
│   │   │
│   │   ├── files.go
│   │   │   - FileIndex
│   │   │   - FileIndexEntry
│   │   │   - 从工具调用和工具结果观察文件路径
│   │   │
│   │   ├── compactor.go
│   │   │   - Compactor
│   │   │   - CompactMode / CompactResult
│   │   │   - 自动、手动、紧急、强制压缩策略
│   │   │
│   │   ├── summary.go
│   │   │   - SummaryBuilder
│   │   │   - CompactInput / CompactOutput
│   │   │   - Compressor 接口
│   │   │   - 摘要 prompt、正式摘要解析、边界消息构造
│   │   │
│   │   ├── *_test.go
│   │   │   - 覆盖工具结果存盘、估算、窗口解析、摘要解析、压缩消息替换
│   │
│   ├── agent/
│   │   ├── agent.go
│   │   │   - Agent 增加上下文管理相关 Option
│   │   │   - 创建 providerCompressor
│   │   │
│   │   ├── loop.go
│   │   │   - provider.Stream 前调用 conversation.Preflight
│   │   │   - context-too-long 后触发 emergency compact 并重试一次
│   │   │   - 模型响应结束后回写 usage
│   │   │
│   │   ├── events.go
│   │   │   - 新增 EventContext
│   │   │   - 新增 ContextEvent
│   │   │
│   │   ├── compressor.go
│   │   │   - providerCompressor
│   │   │   - 压缩专用 provider.Stream 调用
│   │   │
│   │   └── *_test.go
│   │       - 覆盖 preflight 调用、usage 回写、紧急压缩重试、context event
│   │
│   ├── tui/
│   │   ├── model.go
│   │   │   - Model 增加 context usage/window/status 字段
│   │   │   - 处理 EventContext
│   │   │   - 新增 /compact
│   │   │   - 新增 /context 和 /context window
│   │   │   - statusBar 渲染上下文使用量
│   │   │
│   │   ├── styles.go
│   │   │   - 如需要，增加上下文状态样式
│   │   │
│   │   └── *_test.go
│   │       - 覆盖命令解析和状态栏文本生成
│   │
│   └── llm/
│       ├── provider.go
│       │   - 如需要，为 StreamOptions 增加压缩请求所需 prompt 信息
│       │
│       └── errors.go
│           - 复用现有 ContextTooLongError
│
├── README.md
│   - 说明 /compact、/context、上下文窗口配置和工具结果存储位置
│
└── doc/
    └── spec/
        └── phase7-context-management/
            ├── spec.md
            ├── plan.md
            ├── task.md
            └── checklist.md
```

### 项目运行产物目录

```text
<project>/.onecode/context/
├── .gitignore
├── local.yaml
└── tool-results/
    └── 20260713-153012-toolu_abc123.txt
```

`.onecode/context/.gitignore` 由 OneCode 自动维护，但只作用于 `.onecode/context/` 子目录。创建或更新时应保留用户已有内容，只补齐缺失规则：

```gitignore
local.yaml
tool-results/
```

OneCode 不自动修改项目根 `.gitignore`。

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 上下文管理归属 | 扩展 `conversation` 包 | 上下文管理的核心对象是对话历史；放在 `conversation` 中比新增独立顶层包更贴合语义，也避免 `context` 包名和 Go 标准库冲突。 |
| Agent 接入点 | 每次 `provider.Stream` 前执行 preflight | 能统一覆盖每一轮正常模型请求，先处理工具结果，再判断是否需要整体压缩。 |
| 工具结果处理时机 | 工具执行后先原样进入 conversation，下一轮请求前轻量处理 | 保持工具执行链路纯粹，不让 scheduler/registry 承担上下文策略。 |
| 大工具结果存储位置 | 当前项目 `.onecode/context/tool-results/` | 模型后续可以通过读文件工具重新读取，路径与项目绑定，用户也能检查。 |
| 运行产物忽略策略 | 自动维护 `.onecode/context/.gitignore`，不改根 `.gitignore` | 避免误提交大工具结果，同时不干扰用户已有项目级忽略规则。 |
| Token 估算 | 真实 usage 锚点 + 新增内容近似估算 | 满足本阶段不做精确 tokenizer 的要求，同时避免对完整历史反复粗估导致误差越来越大。 |
| 上下文窗口来源 | 本地覆盖 > provider 配置 > 模型推断 > 默认值 | 默认体验接近 Claude Code，不强迫用户配置；国产模型或代理模型可以手动修正。 |
| TUI 窗口配置保存 | `.onecode/context/local.yaml` | 属于当前项目本机偏好，不默认进入共享配置。 |
| 自动压缩阈值 | `window - 20k - 13k` | 预留摘要输出空间和两次检查之间的安全余量。 |
| 手动压缩阈值 | `window - 20k - 3k` | 用户主动触发，允许更激进地使用窗口空间。 |
| 强制压缩阈值 | `window - 3k` | 自动压缩熔断后仍需要 API 前兜底，避免继续发出明显超限请求。 |
| 近期原文保留 | 约 10k token 或至少 5 条最近消息 | 兼顾任务连续性和压缩效果，避免摘要完全替代最近上下文。 |
| 压缩请求工具策略 | 不传任何工具定义 | 满足“摘要请求禁止模型调用工具”的要求，也降低压缩过程副作用。 |
| 压缩摘要格式 | 草稿区 + 正式摘要区，只保留正式摘要 | 允许模型先整理信息，但不把草稿污染后续上下文。 |
| 压缩后边界消息角色 | 使用 `user` role | 当前 provider 历史模型只支持 user/assistant/tool，使用 user role 能减少适配器改动，并明确这是后续可见的上下文提示。 |
| 紧急压缩重试 | 最多一次 | 作为 context-too-long 后的补救机制，避免无限重试。 |
| 自动压缩熔断 | 连续失败 3 次后停止自动重量压缩 | 防止每轮都尝试失败压缩，导致死循环或浪费 API。 |
| TUI 展示策略 | 状态栏常驻 usage，压缩事件额外短提示 | 让用户持续感知上下文压力，同时避免复杂详情面板扩大范围。 |
| 可视化配置方式 | `/context` 查看，`/context window` 输入窗口大小 | 先用命令式 TUI 完成可视化配置，避免本阶段引入复杂弹层。 |

## Spec 覆盖检查

- F1-F4, F19: 由 ToolResultBounder + Preflight 覆盖。
- F5-F7: 由 Compactor + Recent 保留策略覆盖。
- F8: 由 FileIndex + SummaryBuilder 覆盖。
- F9-F10: 由 SummaryBuilder + providerCompressor 覆盖。
- F11: 由 `/compact` + conversation.Compact(manual) 覆盖。
- F12: 由 ContextTooLongError 检测 + emergency compact + retry once 覆盖。
- F13: 由 CompactFuse 覆盖。
- F14-F15: 由 WindowResolver + ProjectStore local config + TUI `/context window` 覆盖。
- F16-F17: 由 EventContext + TUI statusBar 覆盖。
- F18: 由 TokenEstimator + UpdateUsage 覆盖。

# 工具系统 Plan

## 架构概览

本次在已有的 llm、conversation、tui 三个包基础上，新建两个包、扩展四处代码：

**新建 `internal/tools`**

工具系统的独立层。定义 `Tool` 接口和 `Result` 结构，实现 6 个核心工具（read_file、write_file、edit_file、bash、glob、grep），以及一个 `Registry` 负责集中注册和按名查找。这一层不依赖任何 LLM 协议代码，也不感知流式解析逻辑——纯粹是「给我名字和参数，我返回结果」。

**新建 `internal/agent`**

单轮闭环的编排者。接收用户消息后，调用 LLM 流式响应，收集模型返回的工具调用，交给 Registry 执行，把结果塞回对话历史，再请求一轮 LLM 让模型生成最终回复。对外通过 channel 吐出事件流（文本增量、工具调用、工具结果、完成），TUI 从事件流中读取并渲染。agent 只依赖 llm、tools、conversation，不直接 import anthropic/openai SDK。

**扩展 `internal/llm`**

Message 从纯文本扩展为支持多种内容块：text、tool_use、tool_result。StreamEvent 增加工具调用相关事件。Provider 接口的 Stream 方法新增 tools 参数，让适配器能把工具定义注入请求。两个适配器各自实现流式 tool_use 解析——Anthropic 从 content_block_delta 拼 JSON，OpenAI 从 delta.tool_calls 拼 JSON。

**扩展 `internal/conversation`**

新增方法：追加带工具调用的 assistant 消息、追加工具结果消息。历史消息在发给 LLM 前需要正确序列化成各协议要求的格式。

**扩展 `internal/prompt`**

SystemPrompt 增加 Agent 角色说明和工具使用约定，告诉模型何时该调用工具、如何组织参数。

**扩展 `internal/tui`**

用户回车后不再直接调 provider.Stream，改为调 agent.Run 启动单轮闭环。事件泵从 agent 的事件流中读取，文本增量照旧累积渲染，工具调用事件触发工具行展示（`● 工具名(参数)`），工具结果事件展示摘要。流式完成后回到 idle。

**扩展 `cmd/onecode/main.go`**

启动时构造 Registry（注册 6 个工具），传入 tui.New。

**依赖方向（无环）：**

```
tools → （无外部依赖）
conversation → llm
agent → llm, tools, conversation
tui → agent, llm, conversation, prompt
llm → config, prompt
```

## 核心数据结构

### llm 包新增/修改

```go
// ToolDefinition 工具定义，发给 LLM API 让模型知道有哪些工具可用。
// 协议无关，由 Registry.ToToolDefinitions() 生成。
type ToolDefinition struct {
    Name        string                 // 工具名称，如 "read_file"
    Description string                 // 工具描述，告诉模型何时该用这个工具
    Schema      map[string]interface{} // 参数的 JSON Schema，模型据此构造参数
}

// ToolCall 模型在流式响应中请求的一次工具调用。
// 由 llm 适配器从流式片段拼接完成后，通过 StreamEvent 吐出。
type ToolCall struct {
    ID    string                 // 唯一标识，用于关联对应的 ToolResult
    Name  string                 // 工具名称
    Input map[string]interface{} // JSON 参数，传给 Tool.Execute
}

// ToolResult 工具执行结果，回灌给 LLM 让模型据此生成最终回复。
type ToolResult struct {
    ToolUseID string // 对应的 ToolCall.ID，API 要求一一关联
    Content   string // 结果文本（成功时是输出，失败时是错误详情）
    IsError   bool   // 是否为错误，模型据此决定是否重试
}
```

Message 扩展：

```go
// Message 表示对话中的一条消息。
// 扩展后支持三种角色：
//   - "user"：用户输入，Content 有值
//   - "assistant"：模型回复，Content 有值（纯文本）或 ToolCalls 有值（工具调用）
//   - "tool"：工具结果，ToolResult 有值
type Message struct {
    Role       string      // "user" | "assistant" | "tool"
    Content    string      // 纯文本内容（向后兼容）
    ToolCalls  []ToolCall  // assistant 消息携带的工具调用（模型可能同时调多个，但本阶段只处理第一个）
    ToolResult *ToolResult // tool 消息携带的工具结果
}
```

StreamEvent 扩展：

```go
// StreamEvent 流式响应中的一个事件。
// 文本增量和工具调用互斥——同一事件只会有一个字段非零。
type StreamEvent struct {
    Text     string    // 文本增量（模型的纯文本输出片段）
    ToolCall *ToolCall // 工具调用（流式拼接完成后一次性吐出，不是碎片）
    Done     bool      // 本轮流式结束
}
```

Provider 接口变更：

```go
// Provider 定义 LLM Provider 的统一接口。
// Stream 新增 tools 参数：传入工具定义列表，模型据此决定是否调用工具。
type Provider interface {
    Name() string
    Model() string
    Stream(ctx context.Context, msgs []Message, tools []ToolDefinition) (<-chan StreamEvent, <-chan error)
}
```

### tools 包

```go
// Tool 工具接口。每个核心工具实现它。
type Tool interface {
    // Name 返回工具名称，如 "read_file"、"bash"
    Name() string
    // Description 返回工具描述，告诉模型何时该用、怎么用
    Description() string
    // Schema 返回参数的 JSON Schema，模型据此构造合法参数
    Schema() map[string]interface{}
    // Timeout 返回该工具的超时时间（bash=5min，其他=30s）
    Timeout() time.Duration
    // Execute 执行工具，ctx 由 Registry.Execute 注入超时。
    // args 是 JSON 参数，内部 Unmarshal 到私有 struct；
    // 解析失败应返回 Result{IsError: true}，不要 panic。
    Execute(ctx context.Context, args map[string]interface{}) Result
}

// Result 工具执行结果。
// 成功时 Content 是输出文本，失败时 Content 包含完整错误详情。
// IsError=true 时模型会看到错误并决定是否重试。
type Result struct {
    Content string // 成功：工具输出；失败：错误详情（含上下文）
    IsError bool   // true=失败，false=成功
}

// Registry 工具注册中心。
// 集中登记所有工具，按名查找，转成 API 工具列表。
// order 保持注册顺序，保证导出的工具列表顺序稳定。
type Registry struct {
    order []string        // 注册顺序
    tools map[string]Tool // name → Tool
}

func NewRegistry() *Registry
func (r *Registry) Register(t Tool)
func (r *Registry) Get(name string) (Tool, bool)

// List 按注册顺序返回所有工具
func (r *Registry) List() []Tool

// ToToolDefinitions 转成 LLM API 工具定义列表，顺序与 List 一致
func (r *Registry) ToToolDefinitions() []llm.ToolDefinition

// Execute 查找工具并执行。内部：
// 1. 按名查找，找不到返回 Result{IsError: true}
// 2. 用 tool.Timeout() 创建带超时的 ctx
// 3. 调用 tool.Execute，捕获 panic 转为 Result
func (r *Registry) Execute(ctx context.Context, name string, args map[string]interface{}) Result
```

### agent 包

```go
// Agent 持有 provider 与注册中心，执行单轮闭环。
type Agent struct {
    provider llm.Provider
    registry *tools.Registry
}

func New(p llm.Provider, r *tools.Registry) *Agent

// Phase 工具事件阶段。
type Phase int
const (
    PhaseStart Phase = iota // 工具开始执行
    PhaseEnd                // 工具执行完毕
)

// ToolEvent 一次工具调用的开始/结束（供 TUI 渲染工具行与结果摘要）。
type ToolEvent struct {
    Name    string // 工具名称
    Args    string // 参数预览（用于 ● name(args)）
    Phase   Phase  // 开始 or 结束
    Result  string // PhaseEnd 时的结果摘要（过长截断）
    IsError bool   // PhaseEnd 时是否错误
}

// Event 单轮闭环对外事件流元素，TUI 据非零字段分派渲染。
// Text、Tool、Done、Err 四个字段互斥——同一事件只会有一个非零。
type Event struct {
    Text string     // 文本增量（preamble 或最终答复）
    Tool *ToolEvent // 工具调用开始/结束
    Done bool       // 本轮结束
    Err  error      // 出错（不中断会话，TUI 展示后回到 idle）
}

// Run 执行单轮闭环，返回事件 channel。
// 内部流程：请求 LLM（带工具）→ 收集 tool_use → Registry.Execute → 回灌 → 再次请求 LLM → 最终文本。
// conv 在 Run 内被修改（追加 assistant/tool 消息），调用方无需额外操作。
func (a *Agent) Run(ctx context.Context, conv *conversation.Conversation) <-chan Event
```

## 工具详细设计

每个工具私有入参 struct，`Execute` 内 `json.Unmarshal(args, &a)`，解析失败转为 `Result{IsError: true}`。

### read_file

| 项目 | 内容 |
|------|------|
| 参数 | `path`（必填，string）— 文件路径 |
| 成功 | 带行号文本（cat -n 风格），≤2000 行 / ≤256KB，超出截断标注 `[truncated]` |
| 错误 | 文件不存在、不可读、是目录 → `Result{IsError: true}` |

### write_file

| 项目 | 内容 |
|------|------|
| 参数 | `path`（必填，string）、`content`（必填，string） |
| 成功 | `os.MkdirAll` 建父目录后覆盖写，返回「写入成功：N bytes」 |
| 错误 | 写入失败 → `Result{IsError: true}` |

### edit_file

| 项目 | 内容 |
|------|------|
| 参数 | `path`、`old_string`、`new_string`（均必填，string） |
| 成功 | `strings.Count(content, old_string) == 1` 时唯一替换并写回，返回「替换成功」 |
| 错误 | 0 处 → 「未找到匹配」；>1 处 → 「匹配到 N 处，old_string 不唯一，请提供更长上下文」 |

### bash

| 项目 | 内容 |
|------|------|
| 参数 | `command`（必填，string）— shell 命令 |
| 执行 | 平台检测：Unix 用 `sh -c`，Windows 用 `cmd /C` |
| 超时 | 5 分钟，超时返回 `Result{IsError: true, Content: "命令超时"}` |
| 成功 | 合并 stdout + stderr + exit_code，截断 ~30000 字符 |
| 错误 | 非零退出码 → 正常回灌（不标 IsError，让模型自行判断）；超时 → IsError |

### glob

| 项目 | 内容 |
|------|------|
| 参数 | `pattern`（必填，string，如 `**/*.go`）、`path`（可选，默认 cwd） |
| 成功 | 匹配路径列表，≤100 条，排序 |
| 错误 | 无匹配 → 返回空说明（非 IsError） |

### grep

| 项目 | 内容 |
|------|------|
| 参数 | `pattern`（必填，RE2 正则）、`path`（可选，默认 cwd）、`glob`（可选，文件名过滤） |
| 成功 | `file:line:content` 列表，≤100 条，超出标注 `[showing first 100 of N matches]` |
| 错误 | 正则非法 → `Result{IsError: true}`；无命中 → 返回空说明（非 IsError） |

## 模块设计

### llm 适配器改造

**Anthropic 适配器：**

- Stream 方法接收 `tools []llm.ToolDefinition`，转成 `anthropic.ToolParam` 注入请求
- 流式解析：`content_block_start` 类型 `tool_use` 时开始收集（记录 ID 和 Name）；`content_block_delta` 的 `input_json_delta` 拼接到 JSON buffer；`content_block_stop` 时解析完整 JSON，通过 StreamEvent 吐出 ToolCall
- 流式中遇到文本 delta 照旧吐 Text 事件
- 工具结果回灌：将 `llm.ToolResult` 转成 `anthropic.NewToolResultBlock`，插入 user 消息

**OpenAI 适配器：**

- Stream 方法接收 `tools []llm.ToolDefinition`，转成 `openai.ToolParam` 注入请求
- 流式解析：`delta.tool_calls[0]` 出现时开始收集（记录 ID、Name）；`delta.tool_calls[0].function.arguments` 拼接到 JSON buffer；流结束时解析完整 JSON，吐出 ToolCall
- 工具结果回灌：将 `llm.ToolResult` 转成 `openai.ToolMessage`，插入消息列表

**JSON 碎片拼接：** 两个适配器共用同一模式——维护一个 `jsonBuffer strings.Builder`，每次 delta 追加，完成时 `json.Unmarshal`。解析失败时通过 errs channel 报错。

### agent 单轮闭环

```
Run(ctx, conv):
  events := make(chan Event, 1)
  go func():
    // 第一轮：带工具请求 LLM
    stream, errs := provider.Stream(ctx, conv.Messages(), registry.ToToolDefinitions())
    收集流式事件：
      - Text → events <- Event{Text: ...}
      - ToolCall → 记录第一个 tool_use（本阶段只处理一个）
      - Done → 进入工具执行

    // 有工具调用？执行
    if toolCall != nil:
      events <- Event{Tool: &ToolEvent{Name, Args, PhaseStart, ...}}
      result := registry.Execute(ctx, toolCall.Name, toolCall.Input)
      events <- Event{Tool: &ToolEvent{Name, Args, PhaseEnd, Result, IsError}}

      // 回灌对话历史
      conv.AddAssistantWithToolCalls(toolCalls)  // assistant 消息带 tool_use
      conv.AddToolResult(result)                  // tool 消息带 result

      // 第二轮：续答（不带工具）
      stream, errs := provider.Stream(ctx, conv.Messages(), nil)
      收集流式事件：
        - Text → events <- Event{Text: ...}
        - Done → events <- Event{Done: true}
    else:
      // 没有工具调用，直接结束
      conv.AddAssistant(text)
      events <- Event{Done: true}

  return events
```

### tools 六个工具实现

每个工具一个文件，结构相同：

```go
// internal/tools/read_file.go
type readFileTool struct{}
type readFileArgs struct {
    Path string `json:"path"`
}

func (t *readFileTool) Name() string        { return "read_file" }
func (t *readFileTool) Description() string  { return "读取文件内容..." }
func (t *readFileTool) Timeout() time.Duration { return 30 * time.Second }
func (t *readFileTool) Schema() map[string]interface{} { ... }
func (t *readFileTool) Execute(ctx context.Context, args map[string]interface{}) Result {
    var a readFileArgs
    if err := json.Unmarshal(args, &a); err != nil {
        return Result{Content: "参数解析失败: " + err.Error(), IsError: true}
    }
    // 实现逻辑...
}
```

### conversation 扩展

```go
// AddAssistantWithToolCalls 添加带工具调用的 assistant 消息
func (c *Conversation) AddAssistantWithToolCalls(toolCalls []llm.ToolCall)

// AddToolResult 添加工具结果消息
func (c *Conversation) AddToolResult(result llm.ToolResult)
```

### prompt 扩展

SystemPrompt 增加 Agent 角色说明：
- 你是一个能使用工具的编码助手
- 当用户请求需要读写文件、执行命令、搜索代码时，使用工具而非猜测
- 工具调用失败时，根据错误信息调整参数重试
- 工具结果已包含行号，回复时引用具体行号

### tui 改造

```go
// Model 新增字段
type Model struct {
    // ... 现有字段 ...
    agent    *agent.Agent    // 替代直接持有 provider
    toolRegs *tools.Registry // 用于传入 agent
}

// submit 改造
原来: provider.Stream(ctx, msgs)
改为: agent.Run(ctx, conv)

// 事件泵
func waitForAgentEvent(ch <-chan agent.Event) tea.Cmd {
    return func() tea.Msg {
        event := <-ch
        return agentEventMsg(event)
    }
}

// Update 处理 agentEventMsg
case agentEventMsg:
    switch {
    case msg.Text != "":
        // 累积文本，照旧
    case msg.Tool != nil && msg.Tool.Phase == agent.PhaseStart:
        // 展示工具行：● name(args)
    case msg.Tool != nil && msg.Tool.Phase == agent.PhaseEnd:
        // 展示结果摘要
    case msg.Done:
        // 渲染 markdown，回到 idle
    case msg.Err != nil:
        // 展示错误，回到 idle
    }
```

## 模块交互

### 端到端工作流

```
用户输入 "读 main.go 并总结"
        │
        ▼
    tui.Model.Update (enter)
        │
        │  conv.AddUser(input)
        │  agent.Run(ctx, conv)  ← 启动单轮闭环
        │
        ▼
    agent.Run (goroutine)
        │
        │  provider.Stream(ctx, msgs, registry.ToToolDefinitions())
        │
        ▼
    anthropic/openai 适配器
        │
        │  注入 6 个工具定义到 API 请求
        │  流式返回：文本片段 + tool_use 碎片
        │
        │  流式过程中：
        │  ├─ Text delta → StreamEvent{Text} → agent → Event{Text} → tui 渲染文本
        │  └─ tool_use 碎片拼接完成 → StreamEvent{ToolCall} → agent 记录
        │
        ▼
    agent 检测到 ToolCall
        │
        │  events <- Event{Tool: PhaseStart, Name: "read_file", Args: "main.go"}
        │  registry.Execute(ctx, "read_file", {path: "main.go"})
        │      │
        │      │  Registry 查找 tool
        │      │  tool.Timeout() → 30s
        │      │  ctx, cancel := context.WithTimeout(ctx, 30s)
        │      │  tool.Execute(ctx, args)
        │      │      │
        │      │      │  readFileTool.Execute
        │      │      │  os.ReadFile → 带行号格式化 → Result{Content, IsError: false}
        │      │      │
        │      │  return Result
        │      │
        │  events <- Event{Tool: PhaseEnd, Result: "文件内容..."}
        │
        │  回灌对话历史：
        │  conv.AddAssistantWithToolCalls(toolCalls)   // assistant: tool_use
        │  conv.AddToolResult(toolResult)               // tool: result
        │
        │  第二轮请求（不带工具）：
        │  provider.Stream(ctx, msgs, nil)
        │  流式返回：纯文本（模型根据工具结果生成总结）
        │
        │  Text delta → Event{Text} → tui 渲染
        │  Done → Event{Done}
        │
        ▼
    tui 收到 Event{Done}
        │
        │  渲染 markdown
        │  conv.AddAssistant(finalText)
        │  回到 idle
        │
        ▼
    用户看到最终回复
```

### 数据流方向

```
┌─────────┐    Event{Text}     ┌─────────┐
│         │ ──────────────────→ │         │
│  agent  │    Event{Tool}     │   tui   │
│         │ ──────────────────→ │         │
│         │    Event{Done}     │         │
│         │ ──────────────────→ │         │
└────┬────┘                    └─────────┘
     │
     │ provider.Stream(msgs, tools)
     ▼
┌─────────┐
│   llm   │ ←── ToolDefinition[]
│ 适配器   │ ──→ StreamEvent{Text, ToolCall}
└────┬────┘
     │
     │ registry.Execute(name, args)
     ▼
┌─────────┐
│  tools  │
│ Registry│ ──→ Result{Content, IsError}
└─────────┘

conversation 在 agent.Run 内被修改：
  AddUser → AddAssistantWithToolCalls → AddToolResult → AddAssistant
```

## 文件组织

```
src/internal/
├── llm/
│   ├── provider.go      // 修改：Message/StreamEvent/ToolDefinition/ToolCall/ToolResult 定义；Provider 接口增加 tools 参数
│   ├── anthropic.go     // 修改：Stream 注入工具定义、解析流式 tool_use、工具结果回灌
│   ├── openai.go        // 修改：Stream 注入工具定义、解析流式 tool_use、工具结果回灌
│   ├── factory.go       // 不变
│   └── errors.go        // 不变
├── tools/
│   ├── tool.go          // 新建：Tool 接口、Result 结构
│   ├── registry.go      // 新建：Registry（注册、查找、Execute、ToToolDefinitions）
│   ├── read_file.go     // 新建：read_file 工具实现
│   ├── write_file.go    // 新建：write_file 工具实现
│   ├── edit_file.go     // 新建：edit_file 工具实现
│   ├── bash.go          // 新建：bash 工具实现
│   ├── glob.go          // 新建：glob 工具实现
│   └── grep.go          // 新建：grep 工具实现
├── agent/
│   └── agent.go         // 新建：Agent、Event、ToolEvent、Run
├── conversation/
│   └── conversation.go  // 修改：新增 AddAssistantWithToolCalls、AddToolResult
├── prompt/
│   ├── prompt.go        // 不变
│   └── system.txt       // 修改：增补 Agent 角色与工具使用约定
├── tui/
│   ├── model.go         // 修改：submit 改走 agent.Run；事件泵处理工具事件；渲染工具行
│   └── styles.go        // 修改：新增工具行样式（● 图标、结果摘要、错误样式）
└── ...

cmd/onecode/
└── main.go              // 修改：构造 Registry、注入 tui.New
```

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 工具调用循环放哪 | 新建 `internal/agent` 包，TUI 退化为渲染器 | 循环（请求#1→执行→请求#2）无法塞进 ch01 的一次性 Stream；独立包可无 UI 单测（AC8/AC9），只依赖 llm+tools+conversation，不泄漏 SDK 类型 |
| 是否用 SDK 的工具自动执行 | 不用，手动单轮 | 两个 SDK 都有自动循环选项，违反 F6/AC10 单轮约束；手动控制每一步，保证只执行一次工具调用 |
| 工具定义传入哪一层 | `Provider.Stream` 第三参数 `[]ToolDefinition` | 两 SDK 都把 tools 放 per-request params；续答仍需带；保持 Provider 无状态 |
| 工具参数 Schema 生成 | 每工具手写 `map[string]any` | OpenAI `FunctionParameters` 直接用；Anthropic 取 `["properties"]`/`["required"]`。6 个固定工具手写最直白，不引入反射库 |
| 流式工具参数拼接 | 两个适配器各自维护 strings.Builder 累积 JSON 碎片 | SDK 累加器 API 不稳定或语义不同；手动拼接逻辑简单，完全可控 |
| Glob/Grep 实现 | 纯 Go（`filepath.WalkDir` + `regexp`） | 零依赖、跨平台（Windows 无 grep/rg）；spec 要求保持简单 |
| Bash 实现与超时 | 按 `runtime.GOOS` 选 shell + Tool.Timeout() 提供超时 | `sh -c`/`cmd /C` 支持管道/重定向；`exec.CommandContext` 超时杀进程。bash=5min，其他=30s，由各工具自报 |
| 工具失败的表达 | `Execute` 返回 `Result{Content, IsError}`，从不返回 error | F9/N2：所有失败包成结构化结果回灌，程序不崩，上层无需区分 error 路径 |
| 工具结果在 Message 的形态 | assistant 加 `ToolCalls`，role="tool" 加 `ToolResult` | 两 SDK 工具语义本就是 id 关联的 tool_use/tool_result；适配器吸收协议差异 |
| UI 截断 vs 回灌截断 | 两者分离：UI 摘要 ~8 行；回灌为工具级上限（read 2000 行 / bash 30000 字符等） | AC12/N6 要界面截断，但模型需较完整内容；尾部统一加 `[truncated]` 标注 |
| 续答请求是否带 tools | 不带 | 第二轮不应再触发工具调用（F6/AC10）；不传 tools 最干净 |
| thinking 与工具组合 | 历史含工具交互的请求不启用 thinking | Anthropic 要求 thinking 回合附原 thinking 块（含 signature），本章丢弃 thinking 增量；关闭以避免 400 |
| 空最终答复 | 续答为空时用占位提示推给 UI | 空 assistant 回合破坏下一轮请求（角色交替）；占位提示满足 AC10 |
| 空参数归一 | OpenAI 侧空 arguments 归一为 "{}" | 无参工具 arguments 可能为空串，回灌时须是合法 JSON |
| grep 超长行 | bufio.Scanner 遇超长行标注 `[line too long, skipped]` | 避免静默中止导致假"无命中"误导模型 |
| scrollback 顺序提交 | 多个 `tea.Println` 用 `tea.Sequence` | `tea.Batch` 并发无序，会打乱工具行/结果/最终答复的顺序 |
| 工具命名 | `read_file`/`write_file`/`edit_file`/`bash`/`glob`/`grep` | 符合 OpenAI 函数名规则与 Claude Code 习惯 |
| Registry.Execute | 集中查找+超时+panic 捕获 | Agent 只需一行调用；新增工具只需实现接口+Register |

# 工具系统 Implementation Notes

## 一、阶段目标回顾

Phase 2 的目标是把 OneCode 从「纯聊天 TUI」推进到「能使用内置工具的 Coding Agent」。本阶段实现的是**单次工具调用闭环**：

```text
用户输入
  -> 第一轮 LLM 请求（带工具定义）
  -> 模型流式返回文本和/或 tool_use
  -> Agent 收集第一个工具调用
  -> Registry 执行本地工具
  -> 将 assistant tool_use 与 tool_result 写回 Conversation
  -> 第二轮 LLM 请求（不带工具）
  -> 模型基于工具结果生成最终文本回复
```

本阶段刻意没有做完整 Agent Loop。也就是说，第二轮请求不再带工具定义；如果模型在最终回复阶段仍想调用工具，当前不会继续执行。这一点对应 spec 中的单轮约束，也避免早期实现进入不可控的连环调用。

本阶段最终包含两组主要提交：

```text
feat: add tool system with 6 core tools
refactor(tools): improve edit, glob, and grep tooling
```

第一组提交搭起工具系统的主干：工具接口、注册中心、6 个核心工具、LLM 工具调用解析、Agent 单轮闭环、TUI 工具事件展示。

第二组提交针对工具行为做收敛和加固：`edit_file` 改成 Claude Code 风格的精确替换，`glob` 改成真实的相对路径匹配，`grep` 增加路径过滤、二进制/大文件跳过、提前停止，并抽出 `searchutil` 复用路径匹配和文件遍历逻辑。

## 二、最终目录结构

Phase 2 后与工具系统相关的核心目录如下：

```text
src/
├── cmd/onecode/main.go
└── internal/
    ├── agent/
    │   └── agent.go
    ├── conversation/
    │   └── conversation.go
    ├── llm/
    │   ├── provider.go
    │   ├── anthropic.go
    │   └── openai.go
    ├── prompt/
    │   └── system.txt
    ├── tools/
    │   ├── tool.go
    │   ├── registry.go
    │   ├── read_file.go
    │   ├── write_file.go
    │   ├── edit_file.go
    │   ├── bash.go
    │   ├── glob.go
    │   ├── grep.go
    │   └── searchutil/
    │       ├── file_walk.go
    │       └── path_match.go
    └── tui/
        ├── model.go
        └── styles.go
```

依赖方向保持单向：

```text
tools/searchutil -> 标准库
tools            -> llm（仅 registry 导出 ToolDefinition 时使用）
conversation     -> llm
agent            -> llm + tools + conversation
tui              -> agent + llm + conversation + tools
llm              -> config + prompt + provider SDK
cmd/onecode      -> config + llm + tui + tools
```

关键设计是：`tools` 不依赖具体 LLM SDK；`agent` 只处理内部抽象；OpenAI/Anthropic 差异被收在 `llm` 适配器里。

## 三、架构分层、数据流与状态变化

### 3.1 分层职责

Phase 2 的核心架构变化，是把「TUI 直接请求 LLM」拆成多层协作：

```text
cmd/onecode
  负责启动、读取配置、创建 provider、注册工具、启动 TUI

tui
  负责界面状态、用户输入、事件渲染；不直接理解 LLM 工具协议

agent
  负责单轮工具闭环编排；把 LLM 流式事件、工具执行、conversation 写回串起来

llm
  负责 provider 抽象和 OpenAI/Anthropic 协议适配；不执行本地工具

conversation
  负责保存内部统一消息历史；不关心 OpenAI/Anthropic 的具体消息格式

tools
  负责本地能力执行；不关心模型、TUI 或 provider
  
```

这几个层的边界很重要：

- TUI 不直接调用 `provider.Stream`，而是调用 `agent.Run`。
- Agent 不 import OpenAI/Anthropic SDK，只依赖 `llm.Provider` 接口。
- Provider 不执行工具，只把模型返回的工具调用解析成内部 `ToolCall`。
- Tools 不知道 LLM 存在，只接收 `map[string]interface{}` 参数并返回 `Result`。
- Conversation 保存内部结构，具体协议翻译推迟到 provider 层。

这样拆分后，后续要新增 provider、新增工具、替换 UI，都不会互相强耦合。

### 3.2 端到端数据流

一次成功的工具调用请求，数据流如下：

```text
用户在 TUI 输入
  |
  v
tui.Model.Update
  - conv.AddUser(input)
  - agentEvents = agent.Run(ctx, conv)
  - 进入 stateStreaming
  |
  v
agent.Run 第一轮
  - registry.ToToolDefinitions()
  - provider.Stream(ctx, conv.Messages(), toolDefs)
  |
  v
llm provider
  - 内部 Message -> OpenAI/Anthropic 消息
  - ToolDefinition -> API tools
  - 流式读取 Text / tool_use
  - tool_use JSON 分片拼接完成后吐出 StreamEvent{ToolCall}
  |
  v
agent.Run 收到 ToolCall
  - 记录第一个 tool call
  - 发送 ToolEvent PhaseStart 给 TUI
  - registry.Execute(toolName, input)
  |
  v
tools.Registry
  - 查找 Tool
  - 注入 tool.Timeout()
  - 捕获 panic
  - 返回 Result{Content, IsError}
  |
  v
agent.executeTool
  - 发送 ToolEvent PhaseEnd 给 TUI
  - conv.AddAssistantWithToolCalls(...)
  - conv.AddToolResult(...)
  |
  v
agent.secondRound
  - provider.Stream(ctx, conv.Messages(), nil)
  - 第二轮不传 tools，避免再次工具调用
  - 收集最终文本
  |
  v
tui
  - 流式展示文本
  - Done 后渲染 Markdown
  - 回到 stateIdle
```

这里有两条数据同时流动：

```text
用户可见事件流：agent.Event -> agentEventMsg -> tui.Update -> tea.Println/View
模型上下文历史：Conversation.messages -> provider 转协议格式 -> LLM API
```

这两条流不要混淆。TUI 展示工具行只影响用户界面；Conversation 写回 tool_use/tool_result 才影响下一轮模型推理。

### 3.3 工具定义的数据流

工具定义从本地工具实现流向 LLM API：

```text
Tool 实现
  Name()
  Description()
  Schema()
    |
    v
Registry.ToToolDefinitions()
    |
    v
[]llm.ToolDefinition
    |
    v
provider.Stream(..., tools)
    |
    +-- OpenAI: FunctionDefinitionParam{Name, Description, Parameters}
    |
    +-- Anthropic: ToolParam{Name, Description, InputSchema}
```

`Schema()` 返回 `map[string]interface{}` 而不是 JSON 字符串，是因为 provider 层需要结构化读取和传递：

- OpenAI 直接把完整 schema 作为 function parameters。
- Anthropic 从 schema 中取 `properties` 和 `required`。

### 3.4 工具调用结果的数据流

模型调用工具时，provider 把厂商协议统一成内部结构：

```text
OpenAI delta.tool_calls / Anthropic tool_use block
    |
    v
llm.ToolCall{
  ID,
  Name,
  Input,
}
    |
    v
agent.executeTool
    |
    v
tools.Result{
  Content,
  IsError,
}
    |
    v
llm.ToolResult{
  ToolUseID: ToolCall.ID,
  Content,
  IsError,
}
```

`ToolUseID` 是关键关联字段。没有它，OpenAI/Anthropic 都无法知道某条工具结果对应哪次工具调用。

### 3.5 主要状态变化

#### TUI 状态机

TUI 仍然保持 Phase 1 的三态模型，但 `stateStreaming` 的含义扩展了：

```text
stateSelecting
  多 provider 时选择模型

stateIdle
  等待用户输入

stateStreaming
  Agent 正在运行：可能在等待 LLM、接收文本、执行工具、等待第二轮最终答复
```

状态转换：

```text
stateIdle
  -- Enter 且输入非空 -->
stateStreaming
  -- agent Done / delayed doneMsg -->
stateIdle

stateStreaming
  -- agent Err -->
stateIdle
```

`stateStreaming` 内部没有再细分成 `waiting_llm`、`running_tool`、`second_round`，因为这些细粒度状态被 `agent.Event` 表达：

```text
Event.Text          文本增量
Event.Tool.Start    工具开始
Event.Tool.End      工具结束
Event.Done          本轮完成
Event.Err           本轮错误
```

#### Agent 内部状态

Agent 没有显式 enum，但 `Run` 里实际上有这些阶段：

```text
first_round_streaming
  收集文本和第一个 ToolCall

tool_executing
  发送 PhaseStart，执行 Registry.Execute，发送 PhaseEnd

history_writeback
  写入 assistant tool_use 和 tool_result

second_round_streaming
  不带 tools 续答，只收集最终文本

done
  发送 Event{Done:true}
```

当前只保存一个：

```go
var toolCall *llm.ToolCall
```

这就是单工具调用限制的落点之一。即使 provider 吐出多个 tool call，当前 Agent 也只记录第一个。

#### Provider 流式解析状态

OpenAI provider 用：

```go
toolCallID
toolCallName
toolCallArgs strings.Builder
inToolCall
```

表示正在拼接一个 `delta.tool_calls` 的 JSON 参数。

Anthropic provider 用：

```go
toolUseID
toolUseName
toolUseJSON strings.Builder
inToolUse
```

表示正在拼接一个 `tool_use` content block 的 JSON 参数。

两者最终都归一为：

```go
StreamEvent{ToolCall: &ToolCall{...}}
```

#### Conversation 状态变化

无工具纯文本时，Conversation 仍然是：

```text
user -> assistant
```

有工具时，变成：

```text
user
assistant(tool_use)
tool(tool_result)
assistant(final_text)
```

这个顺序必须保持。OpenAI 和 Anthropic 都要求工具结果跟在对应的工具调用后面，并通过 ID 关联。

## 四、Tool 接口与 Registry

### Tool 接口

`internal/tools/tool.go` 定义统一工具接口：

```go
type Tool interface {
    Name() string
    Description() string
    Schema() map[string]interface{}
    Timeout() time.Duration
    Execute(ctx context.Context, args map[string]interface{}) Result
}
```

每个工具都提供：

- `Name`: LLM 工具名，例如 `read_file`、`grep`。
- `Description`: 让模型知道何时使用工具。
- `Schema`: JSON Schema，约束模型构造参数。
- `Timeout`: 每个工具自报超时。普通工具 30 秒，`bash` 5 分钟。
- `Execute`: 真正执行工具，所有失败都转成 `Result`，而不是 panic 或向上抛业务错误。

`Result` 是工具层统一返回：

```go
type Result struct {
    Content string
    IsError bool
}
```

这让工具失败可以作为结构化信息回灌给模型。比如文件不存在、正则非法、命令超时，都不会让程序崩溃，而是返回 `IsError=true` 的工具结果。

### Registry

`internal/tools/registry.go` 负责集中管理工具：

- `Register(t Tool)`: 按工具名注册，重复注册跳过。
- `Get(name)`: 按名查找工具。
- `List()`: 按注册顺序返回工具。
- `ToToolDefinitions()`: 将注册工具转成 `[]llm.ToolDefinition`，用于发给 LLM API。
- `Execute(ctx, name, args)`: 查找工具、注入超时、捕获 panic、返回 `Result`。

Registry 内部保存：

```go
order []string
tools map[string]Tool
```

`order` 的作用是保证导出的工具定义顺序稳定。稳定顺序有利于调试，也避免每次请求工具列表随机变化。

`Execute` 是安全边界之一：

```text
找不到工具 -> Result{IsError: true}
tool.Timeout() <= 0 -> 默认 30s
工具 panic -> 捕获并转成 Result
ctx deadline exceeded -> 返回工具执行超时
```

## 五、六个核心工具

### read_file

职责：读取文本文件，返回带行号的内容，方便模型引用位置。

主要行为：

- 参数：`path`
- 文件不存在返回 `文件不存在: <path>`，`IsError=true`
- 路径是目录或不可读也返回结构化错误
- 输出带行号
- 控制输出体量，避免大文件撑爆上下文

它适合作为后续 `edit_file` 的前置工具：模型先读文件确认原文，再用精确替换修改。

### write_file

职责：创建或覆盖文件。

主要行为：

- 参数：`path`、`content`
- 自动创建父目录
- 使用 `os.WriteFile` 覆盖写入
- 写入失败返回结构化错误

它适合创建新文件或完整重写文件。对于局部修改，推荐用 `edit_file`。

### edit_file

初版按照 spec 实现为唯一字符串替换。后续 refactor 进一步收敛为 Claude Code 风格：

```text
path
old_string
new_string
replace_all
```

最终行为：

- `old_string` 必须非空。
- 默认要求 `old_string` 在文件中恰好出现一次。
- 0 次匹配：返回 `未找到匹配`。
- 多次匹配且 `replace_all=false`：返回不唯一错误，并提示提供更长上下文或设置 `replace_all=true`。
- `replace_all=true` 时替换所有匹配位置。
- `new_string` 可以为空字符串，用于删除精确匹配文本。

这里故意移除了之前探索过的行号编辑和正则替换。原因是 agent 编辑代码时，行号容易随着上下文变化漂移；精确原文替换更可审计，也更接近 Claude Code 的 `Edit` 工具语义。

当前 `edit_file` 仍是全文读取和全文写回。对于源码和配置文件这是可接受的；如果未来要处理超大文件，应增加文件大小限制、二进制检测、权限保留和原子写入。

### bash

职责：执行 shell 命令，返回 stdout、stderr 和退出码。

主要行为：

- Windows 使用 `cmd /C`
- 非 Windows 使用 `sh -c`
- 超时为 5 分钟
- stdout/stderr 分别截断，避免输出过长
- 非零退出码目前不直接标记 `IsError=true`，而是让模型看到 `exit_code` 自行判断

这个工具风险最高。Phase 2 仍属于基础版本，尚未实现危险命令拦截、用户确认、工作目录沙箱或网络限制。后续如果要面向真实项目使用，`bash` 应优先补安全策略，例如 denylist、allowlist、工作区限制、交互确认。

### glob

职责：按 glob 模式找文件路径。

初版实现较简单，主要用 `filepath.WalkDir` 遍历，再用 `filepath.Base(path)` 匹配文件名。这个版本的问题是：

```text
**/*.go 和 src/**/*.go 几乎没有区别
src/*.go 这类路径约束不可靠
目录可能被返回
未跳过 .git/node_modules/vendor
```

后续重构后，glob 变成：

- 使用 `searchutil.WalkSearchFiles` 遍历文件，不返回目录。
- 跳过 `.git`、`node_modules`、`vendor`、`.idea`、`.vscode`。
- 对每个文件计算相对路径 `relPath`。
- 使用 `searchutil.MatchPattern(pattern, relPath)` 做路径段匹配。
- 支持 `**` 表示任意层级目录。
- 最多返回 100 条结果。
- 结果排序后输出。

最终语义：

```text
*.go          任意目录下文件名匹配 *.go
**/*.go       任意目录层级下的 .go 文件
src/**/*.go   仅 src 下任意层级 .go 文件
src/*.go      仅 src 第一层 .go 文件
```

### grep

职责：在文件内容中按正则搜索，返回 `file:line:content`。

初版实现：

- `filepath.WalkDir` 递归目录
- 可选 `glob` 只匹配文件名
- `bufio.Scanner` 逐行读
- 正则匹配后记录结果
- 最多保存 100 条，但会继续扫描以统计总数

后续优化后：

- 复用 `searchutil.WalkSearchFiles` 遍历文件。
- `glob` 参数改成路径过滤，支持 `src/**/*.go`。
- 跳过 `.git`、`node_modules`、`vendor` 等大目录。
- 单文件大于 10MB 跳过。
- 读取前 8KB 检测二进制文件；包含 `0x00` 则跳过。
- 单行最大 1MB，避免 Scanner 默认 64KB 限制太低。
- 每 128 行检查一次 `ctx.Done()`，让大文件扫描能响应取消/超时。
- 命中 100 条后直接停止整个搜索，不再为了统计继续扫完整仓库。
- 记录跳过摘要，例如 `[skipped files: binary=1, large=2]`。

`grep` 保留了 `glob` 参数，而不是要求模型先 `glob` 再逐文件 `grep`。原因是「在某类文件里搜索内容」是常见原子意图，一次工具调用更稳定，也避免模型把大量文件路径塞进上下文或多次调用工具。

## 六、searchutil 的抽取

第二轮工具优化后，`glob.go` 和 `grep.go` 都需要同样的能力：

```text
校验 glob pattern
按相对路径匹配 glob
遍历搜索文件
跳过常见大目录
计算 relPath
处理 ctx 取消
```

为了避免工具实现和工具类 helper 混在一起，最终抽到：

```text
internal/tools/searchutil/path_match.go
internal/tools/searchutil/file_walk.go
```

### path_match.go

公开函数：

```go
ValidateGlobPattern(pattern string) error
MatchPattern(pattern, relPath string) (bool, error)
```

`ValidateGlobPattern` 只检查 pattern 语法，不检查是否有文件匹配。它会跳过 `**`，其他段交给 `filepath.Match` 验证。

`MatchPattern` 的核心逻辑：

1. 标准化 pattern 和 relPath：去空格、`\` 转 `/`、去掉 `./` 和开头 `/`。
2. 如果 pattern 不含 `/`，按文件名匹配。例如 `*.go` 匹配任意目录下的 `glob.go`。
3. 如果 pattern 含 `/`，拆成路径段递归匹配。
4. 普通段用 `filepath.Match`。
5. `**` 可以匹配 0 个、1 个或多个路径段。

例如：

```text
patternParts = ["src", "**", "*.go"]
pathParts    = ["src", "internal", "tools", "glob.go"]
```

`**` 会尝试吞掉不同数量的路径段，直到后面的 `*.go` 能匹配 `glob.go`。

### file_walk.go

公开函数：

```go
NormalizeSearchRoot(root string) string
ValidateSearchRoot(root string) error
WalkSearchFiles(ctx context.Context, root string, fn func(path, relPath string) error) (int, error)
```

`WalkSearchFiles` 是共享遍历器：

- 使用 `filepath.WalkDir`
- 遇到不可读路径时统计 `skippedUnreadable`
- 每个节点检查 `ctx.Done()`
- 目录只用于遍历，不传给调用方
- 跳过 `.git`、`node_modules`、`vendor`、`.idea`、`.vscode`
- 对文件计算相对路径并转成 slash 风格
- 调用调用方回调 `fn(path, relPath)`

这样 `glob` 和 `grep` 的区别只剩业务逻辑：

```text
glob: relPath 匹配 pattern 后收集 path
grep: relPath 先过 glob 过滤，再扫描文件内容
```

## 七、LLM 抽象扩展

`internal/llm/provider.go` 从纯文本流扩展为工具感知协议。

新增结构：

```go
type ToolDefinition struct {
    Name        string
    Description string
    Schema      map[string]interface{}
}

type ToolCall struct {
    ID    string
    Name  string
    Input map[string]interface{}
}

type ToolResult struct {
    ToolUseID string
    Content   string
    IsError   bool
}
```

`Message` 扩展为：

```go
type Message struct {
    Role       string
    Content    string
    ToolCalls  []ToolCall
    ToolResult *ToolResult
}
```

`ToolCalls` 是切片，因为一条 assistant 消息在协议上可能包含多个工具调用；当前 Agent 只执行第一个。`ToolResult` 是指针，因为一条 tool 消息最多对应一个结果，`nil` 可以表示没有工具结果。

`StreamEvent` 增加：

```go
ToolCall *ToolCall
```

Provider 接口变成：

```go
Stream(ctx context.Context, msgs []Message, tools []ToolDefinition)
```

`tools == nil` 或空切片表示本轮不允许模型调用工具。Agent 第二轮续答就是用这个特性禁用工具调用。

## 八、OpenAI 适配细节

`internal/llm/openai.go` 负责把内部格式翻译成 OpenAI Chat Completions SDK 格式。

### 工具定义注入

`[]ToolDefinition` 被转成 OpenAI function tools：

```text
Name        -> function.name
Description -> function.description
Schema      -> function.parameters
```

OpenAI 的 function parameters 可以直接接收完整 JSON Schema，因此代码用：

```go
Parameters: shared.FunctionParameters(schema)
```

如果 schema 为 nil，会归一成空 object schema。

### 工具调用历史回放

内部 assistant 工具调用：

```go
Message{Role: "assistant", ToolCalls: []ToolCall{...}}
```

转成 OpenAI assistant message 的 `tool_calls`。注意 OpenAI 要求 `arguments` 是 JSON 字符串，因此内部 `map[string]interface{}` 要先 `json.Marshal`。

内部工具结果：

```go
Message{Role: "tool", ToolResult: &ToolResult{...}}
```

转成 OpenAI `tool` role message，并设置 `ToolCallID`。这个 ID 必须和前面的 tool call ID 对上。

### 流式工具调用解析

OpenAI 工具调用在 `choice.Delta.ToolCalls` 中分片返回。实现中维护：

```go
toolCallID
toolCallName
toolCallArgs strings.Builder
inToolCall
```

当 delta 里出现新 ID，说明开始一个工具调用；后续 `Function.Arguments` 分片持续写入 builder。遇到 `FinishReason == "tool_calls"` 或 `"stop"` 时，将累积的 JSON 字符串解析成 `map[string]interface{}`，再吐出统一的：

```go
StreamEvent{ToolCall: &ToolCall{...}}
```

当前只处理 `Choices[0]` 和 `Delta.ToolCalls[0]`，并行/多工具调用尚未完整支持。

## 九、Anthropic 适配细节

`internal/llm/anthropic.go` 负责 Anthropic Messages API 的工具协议翻译。

### 工具定义注入

内部 `ToolDefinition` 转为 `anthropic.ToolParam`：

```text
Name        -> tool.name
Description -> tool.description
Schema.properties -> input_schema.properties
Schema.required   -> input_schema.required
```

Anthropic 这里没有像 OpenAI 一样直接塞完整 schema，而是从 `t.Schema` 里取 `properties` 和 `required`。

### 工具调用历史回放

Anthropic 没有 OpenAI 那种 `tool_calls` 字段。assistant 工具调用要表示为 assistant message 里的 content block：

```text
text block（可选）
tool_use block
```

代码使用：

```go
anthropic.NewToolUseBlock(tc.ID, tc.Input, tc.Name)
```

工具结果也不是 `tool` role，而是 user message 里的 `tool_result` block：

```go
anthropic.NewToolResultBlock(toolUseID, content, isError)
```

这就是为什么内部 `ToolResult.ToolUseID` 必须保留：它把工具结果和前面的 tool_use 绑定起来。

### 流式工具调用解析

Anthropic 以 content block 事件流返回工具调用：

```text
ContentBlockStartEvent(tool_use) -> 记录 ID/Name
ContentBlockDeltaEvent(InputJSONDelta) -> 拼接 PartialJSON
ContentBlockStopEvent -> 解析完整 JSON，吐出 ToolCall
```

实现维护：

```go
toolUseID
toolUseName
toolUseJSON strings.Builder
inToolUse
```

普通文本通过 `TextDelta` 变成 `StreamEvent{Text: ...}`。`ThinkingDelta` 当前直接丢弃。

### Thinking 与工具

当前策略是：

```text
有工具定义时，不启用 thinking
无工具时，才按配置启用 thinking
历史里含工具调用时，明确清空 thinking 配置
```

原因是本阶段没有保存 Anthropic thinking block 和 signature。若带工具历史时仍启用 thinking，容易触发协议约束或 400 错误。这里选择保守关闭。

## 十、Conversation 扩展

`internal/conversation/conversation.go` 增加：

```go
AddAssistantWithToolCalls(toolCalls []llm.ToolCall)
AddToolResult(result llm.ToolResult)
```

工具调用闭环需要在历史里补两类消息：

```text
assistant: 我刚才请求了哪个工具、参数是什么、tool call id 是什么
tool: 这个 tool call id 的执行结果是什么
```

下一轮请求时，OpenAI/Anthropic 适配器再把这两类内部消息翻译成各自协议格式。

这使 Conversation 层保持协议无关，只保存内部统一结构。

## 十一、Agent 单轮闭环

`internal/agent/agent.go` 是本阶段新增的编排层。

`Agent` 持有：

```go
provider llm.Provider
registry *tools.Registry
```

`provider` 是接口值，本身可以持有具体 provider 的指针。`registry` 是具体结构体，用指针共享同一个工具注册中心。

`Run` 对外返回事件 channel：

```go
func (a *Agent) Run(ctx context.Context, conv *conversation.Conversation) <-chan Event
```

内部分两轮：

### 第一轮：带工具

```go
toolDefs := a.registry.ToToolDefinitions()
stream, errs := a.provider.Stream(ctx, conv.Messages(), toolDefs)
```

Agent 从流中收集：

- 文本增量：直接转发给 TUI。
- 工具调用：当前只保存第一个 `ToolCall`。
- Done：如果有工具调用，进入工具执行；否则结束本轮。

### 工具执行

`executeTool` 做四件事：

1. 格式化参数预览，用于 TUI 工具行。
2. 发 `PhaseStart` 事件。
3. 调用 `registry.Execute(ctx, toolCall.Name, toolCall.Input)`。
4. 发 `PhaseEnd` 事件，并将工具调用和结果写回 conversation。

写回历史：

```go
conv.AddAssistantWithToolCalls([]llm.ToolCall{*toolCall})
conv.AddToolResult(llm.ToolResult{
    ToolUseID: toolCall.ID,
    Content:   result.Content,
    IsError:   result.IsError,
})
```

### 第二轮：不带工具

```go
stream, errs := a.provider.Stream(ctx, conv.Messages(), nil)
```

第二轮只收集文本。若模型没有返回文本，会写入占位：

```text
[工具执行完成，无额外回复]
```

这个设计保证本阶段只执行一次工具调用。

## 十二、TUI 集成

`internal/tui/model.go` 从直接调用 `provider.Stream` 改成调用 `agent.Run`。

新增字段：

```go
agent       *agent.Agent
registry    *tools.Registry
agentEvents <-chan agent.Event
```

用户按 Enter 后：

```text
conv.AddUser(input)
agentEvents = agent.Run(ctx, conv)
启动 waitForAgentEvent + spinner + renderTick
```

`waitForAgentEvent` 把 `agent.Event` 包装成 Bubble Tea 消息：

```go
type agentEventMsg agent.Event
```

这样 `Update` 可以用 type switch 明确处理 agent 事件。

工具事件展示：

```text
PhaseStart -> 打印 `● name(args)`
PhaseEnd   -> 打印 `  └─ result summary`
错误结果   -> 带错误标记
```

文本增量仍然累积到 `curReply`，流式期间 View 显示原始文本。Done 后使用 glamour 渲染 Markdown，并打印耗时。

Phase 2 的 UI 目标不是做交互式工具详情，而是让用户能看到模型正在调用什么工具、参数大概是什么、结果摘要是什么。

## 十三、System Prompt 更新

`internal/prompt/system.txt` 增加了工具使用约定，核心意图是：

- OneCode 是可使用工具的 coding assistant。
- 需要读写文件、执行命令、搜索代码时应使用工具，而不是猜测。
- 工具失败时应读错误信息并调整策略。
- 工具结果包含行号时，回复应引用具体位置。

这部分很重要，因为模型是否稳定调用工具，不只取决于 API tools，也取决于 system prompt 是否明确告诉它工具边界和失败处理方式。

## 十四、main.go 注册工具

`cmd/onecode/main.go` 启动时构造 registry 并注册 6 个工具：

```text
read_file
write_file
edit_file
bash
glob
grep
```

然后把 registry 传入 `tui.New`，TUI 再用它构造 Agent。新增工具时的路径很清楚：

```text
实现 Tool 接口
在 main.go Register
必要时补测试
```

## 十五、测试与验证

工具层补充了 `internal/tools/tool_test.go`：

- `read_file`: 不存在文件报错，存在文件可读取。
- `write_file`: 写入文件后检查磁盘内容。
- `edit_file`: 唯一替换、无匹配、多匹配与 `replace_all`、空字符串删除。
- `bash`: 基础命令执行。
- `glob`: 基础搜索、`**/*.go`。
- `searchutil.MatchPattern`: 裸 `*.go`、`**/*.go`、`src/**/*.go`、`src/*.go` 语义差异。
- `grep`: 基础搜索、相对路径 glob 限制、二进制文件跳过、非法 glob 报错。
- `registry`: 注册、查找、导出定义、执行工具。

常用验证命令：

```powershell
cd src
$env:GOCACHE = Join-Path (Resolve-Path ..).Path '.gocache'
go test ./...
```

在当前 Windows 环境中，默认 Go build cache 曾遇到用户目录权限问题，所以测试时临时把 `GOCACHE` 放到仓库内 `.gocache`，测试后删除。

## 十六、几个关键取舍

### 1. 工具执行失败返回 Result，不返回 error

工具失败是模型可处理的业务信息，不应该让程序崩溃或中断会话。统一 `Result{Content, IsError}` 后，上层逻辑很简单，模型也能看到具体错误原因。

### 2. 第二轮请求不带 tools

这是实现单轮约束的最干净方式。如果第二轮仍带 tools，模型可能继续发起工具调用，Agent 就要决定是否忽略、报错或继续循环。当前直接不暴露工具，避免歧义。

### 3. edit_file 采用精确字符串替换

行号编辑实现过一版，但最终收敛为 `old_string/new_string`。原因是：

- 行号会漂移。
- 精确上下文更利于审计。
- 与 Claude Code 的 Edit 工具语义接近。
- 模型可以通过 `read_file` 先获取上下文，再构造唯一 old_string。

### 4. grep 保留 glob 参数

虽然模型可以先 `glob` 再逐个 grep，但这样会：

- 增加工具调用次数。
- 把大量路径塞进上下文。
- 增加模型漏文件或提前停止的概率。

`grep(pattern, glob="src/**/*.go")` 更符合真实搜索意图。

### 5. searchutil 是底层复用，不是工具互调

`grep` 没有调用 `glob` 工具，而是和 `glob` 共享路径匹配/文件遍历 helper。这样工具 API 仍然独立，代码逻辑又不重复。

### 6. 保留协议无关内部结构

OpenAI 和 Anthropic 对工具调用历史、工具结果回灌的格式完全不同。内部统一用 `ToolCall`/`ToolResult`，差异只留在 provider 适配器里。这是后续支持更多 provider 的基础。

## 十七、当前遗留问题与后续方向

### 安全与权限

Phase 2 仍未实现：

- 工具执行前用户确认。
- bash 危险命令拦截。
- 写文件路径白名单。
- 只允许操作 workspace。
- 网络命令限制。

这些是后续真正可用前的重点。

### 并行与多工具调用

当前 `Message.ToolCalls` 是切片，但 Agent loop 只执行第一个工具调用。OpenAI/Anthropic 解析器也基本按单工具状态实现。未来如果支持并行工具，需要：

- provider 侧按 tool call index 或 block id 管理多个 JSON buffer。
- agent 侧并行或顺序执行多个工具。
- conversation 写回多条 tool result。

### 完整 Agent Loop

当前是单轮：

```text
tool_use -> tool_result -> final answer
```

未来完整 Agent Loop 应支持：

```text
tool_use -> tool_result -> tool_use -> tool_result -> ... -> final answer
```

同时需要最大步数、费用/时间限制、错误重试策略。

### 工具输出体量和格式

当前工具输出主要是纯文本。未来可以考虑：

- read_file 支持 offset/range。
- grep 支持上下文行。
- glob/grep 支持 exclude。
- tool result 加结构化 metadata。
- UI 支持展开/折叠工具详情。

### bash 安全

`bash` 当前依然是最危险工具。建议后续优先做：

- denylist：`rm -rf`、`del /s`、`format`、`git reset --hard`、`curl | sh` 等。
- allowlist：测试、构建、查询类命令优先放行。
- 写操作或网络操作请求用户确认。
- 工作目录限制和环境变量审查。

## 十八、复盘总结

Phase 2 的核心价值是把 OneCode 的架构从「TUI 直接请求 LLM」升级为：

```text
TUI -> Agent -> LLM Provider
             -> Tool Registry
             -> Conversation
```

这个变化让工具调用成为一等能力：

- LLM 适配器负责协议翻译。
- Agent 负责单轮闭环编排。
- Registry 负责工具查找、超时和 panic 隔离。
- Tool 实现只关心自己的参数和执行结果。
- TUI 只消费 agent 事件并渲染。

第二轮 refactor 的重点是让工具更接近真实 coding agent 的使用方式：

- `edit_file` 更保守、更可审计。
- `glob` 语义从「文件名匹配」升级为「相对路径 glob」。
- `grep` 从朴素全文搜索升级为带路径过滤和文件保护的代码搜索。
- `searchutil` 把共享能力从工具实现中剥离出来，降低重复和后续维护成本。

最终这个阶段仍然是一个受控的 MVP：能完成单次工具调用和结果回灌，但还没有权限系统、完整循环、并行工具和强沙箱。这些限制是有意保留的，方便后续阶段逐步扩展。

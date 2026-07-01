# Prompt Runtime Implementation Notes

## 阶段目标回顾

Phase 4 的目标，是把 OneCode 的提示词系统从“单个全局 System Prompt”升级为“结构化 Prompt Runtime”。

Phase 3 已经完成 ReAct Agent Loop：模型可以多轮请求、调用工具、回写工具结果并继续推理。但当时提示词仍然偏粗粒度：身份设定、工具规则、模式约束和动态环境信息都容易混在一起。Phase 4 解决的是提示词层面的长期可维护性和缓存友好性：

```text
稳定规则
  -> 结构化模块拼接
  -> 形成 StableSystem
  -> 适合 provider 缓存通道

动态上下文
  -> 每轮构造 RequestContext
  -> 生成 system-reminder
  -> 不写入 conversation
  -> 只影响本轮请求
```

本阶段仍然不做项目指令文件加载、自动记忆、真实 MCP 接入、自动化评估和权限确认。这些留给后续阶段。

## 主要改动

### Prompt Runtime

新增 `internal/prompt` 下的结构化提示词运行时：

```text
modules.go       - 稳定系统提示模块定义与拼接
runtime.go       - Runtime、BuildOptions、RequestContext、Payload
reminder.go      - Environment reminder 与 Plan Mode reminder
modules_test.go  - 模块顺序、格式、可选模块测试
runtime_test.go  - 稳定/动态分离测试
reminder_test.go - Plan Mode reminder 轮次测试
```

Prompt Runtime 输出两类内容：

- `StableSystem`：稳定系统提示主体，来自固定模块和可选模块。
- `Reminders`：运行时动态补充消息，例如环境信息、Plan Mode 约束。

### Agent 接入

`Agent` 新增 `promptRuntime *prompt.Runtime`，每轮请求模型前构造本轮 prompt payload：

```text
Agent Loop
  -> buildRequestContext(ctx, opts, iteration)
  -> prompt.Runtime.BuildPayload(...)
  -> llm.StreamOptions{Prompt: payload}
  -> provider.Stream(...)
```

这样 Agent loop 不再只把 conversation 和 tools 交给 provider，还会带上本轮的稳定提示和动态 reminder。

### 环境上下文

新增 `internal/agent/environment.go`，集中收集运行时环境信息：

```text
WorkDir   -> os.Getwd()
OS        -> runtime.GOOS
Arch      -> runtime.GOARCH
Shell     -> tools.CommandShellName()
GitStatus -> git status --porcelain=v1 -b
Date      -> time.Now()
Mode      -> RunOptions.Mode
Iteration -> Agent loop 当前轮次
```

其中 Git 状态是轻量摘要，不读取 diff，不解析完整历史。

### Provider 扩展

`llm.Provider.Stream` 增加 `StreamOptions`：

```go
type StreamOptions struct {
	Prompt prompt.Payload
}
```

Provider 负责把 `StableSystem`、`Reminders`、conversation messages 和 tools 映射成具体 API 请求。

当前实现中：

- OpenAI：`StableSystem` 和 reminders 都作为 system message 加入 Chat Completions messages。
- Anthropic：`StableSystem` 和 reminders 都作为 `System` text blocks；稳定 block 设置 cache control。
- Anthropic tools：最后一个 tool schema 设置 cache control，尽量让工具描述进入缓存断点。

这保证了提示词抽象已经从 Agent 层解耦出来；Provider 侧的具体映射策略后续仍可继续优化。

### Usage / Cache Usage

`llm.Usage` 增加 `CacheUsage`：

```go
type CacheUsage struct {
	Available           bool
	CreationInputTokens int
	ReadInputTokens     int
}
```

Agent collector 会把 provider usage 转成 `EventUsage`，TUI 在 cache 信息可用时显示简短状态：

```text
tokens 1234 cache read 800/create 200
```

Provider 不提供 cache 字段时，`Cache.Available=false`，UI 不显示误导性的 `0`。

## 最终目录结构

Phase 4 后，与 Prompt Runtime 相关的核心文件如下：

```text
src/internal/
├── agent/
│   ├── agent.go        - Agent 持有 Prompt Runtime
│   ├── loop.go         - 每轮传入 prompt payload
│   ├── environment.go  - 运行时环境上下文采集
│   ├── events.go       - UsageEvent 增加 cache 字段
│   └── collector.go    - 转发 usage/cache usage
├── llm/
│   ├── provider.go     - StreamOptions、CacheUsage
│   ├── openai.go       - OpenAI prompt payload 映射
│   └── anthropic.go    - Anthropic prompt payload 与 cache usage 映射
├── prompt/
│   ├── modules.go      - 稳定系统提示模块
│   ├── runtime.go      - Runtime 与 Payload
│   └── reminder.go     - 动态 reminder
├── tools/
│   ├── bash.go         - CommandShellName 与工具描述强化
│   └── *.go            - 工具描述补强
└── tui/
    └── model.go        - usage/cache usage 状态展示
```

依赖方向保持清晰：

```text
prompt          -> 标准库
tools           -> 标准库 + llm schema 抽象
agent           -> prompt + llm + tools + conversation
llm providers   -> prompt.Payload + provider SDK
tui             -> agent events
conversation    -> llm.Message
```

`prompt` 不依赖 Agent 或 Provider；它只负责构造提示词内容。具体如何发给模型，由 `llm` 适配层负责。

## 架构、数据流与状态变化

### 1. StableSystem 数据流

稳定系统提示只在 Prompt Runtime 初始化时构建：

```text
prompt.NewRuntime(BuildOptions)
  -> BuildStableSystem(opts)
  -> DefaultModules()
  -> append OptionalModules
  -> renderModule
  -> Runtime{stable: "..."}
```

Agent 默认构造：

```go
runtime, err := prompt.NewRuntime(prompt.BuildOptions{})
```

`Runtime.StableSystem()` 返回稳定文本。这个文本不包含 cwd、日期、模式、轮次、git 状态等动态信息。

设计目的：

- 稳定模块顺序固定，便于测试和缓存。
- 动态信息不污染稳定 prompt。
- 后续接入项目指令、Skill、长期记忆时，可以作为可选模块追加，而不是改动固定模块。

### 2. Dynamic Reminder 数据流

每轮 Agent 请求模型前都会重新构造动态上下文：

```text
runLoop 第 n 轮
  -> promptPayload(ctx, opts, iteration)
  -> buildRequestContext(...)
  -> prompt.Runtime.BuildPayload(...)
  -> buildEnvironmentReminder(...)
  -> 如果 ModePlan，再 buildPlanModeReminder(...)
```

`Payload.Reminders` 只传给本次 provider 请求，不写入 conversation。

这点很重要。Conversation 只保存真实用户消息、assistant 回复和 tool result；环境 reminder、Plan reminder 都是运行时补充上下文，不能成为长期历史的一部分。

### 3. Environment Context 数据流

环境上下文由 `internal/agent/environment.go` 统一采集：

```text
buildRequestContext
  -> os.Getwd()
  -> runtime.GOOS
  -> runtime.GOARCH
  -> tools.CommandShellName()
  -> collectGitStatus(ctx, cwd)
  -> time.Now()
  -> prompt.RequestContext
```

渲染后的 reminder 类似：

```text
<system-reminder>
Environment:
- Working directory: E:\src\go\OneCode Coding Agent
- OS: windows
- Arch: amd64
- Shell: cmd
- Date: 2026-07-01
- Mode: execute
- Iteration: 1
- Git status (--porcelain=v1 -b):
  ## feature/prompt-runtime
   M src/internal/prompt/reminder.go
  ?? src/internal/agent/environment.go
</system-reminder>
```

其中 `Shell` 不是从用户环境变量猜测，而是来自 `tools.CommandShellName()`。这样提示词告诉模型的 shell，与 OneCode 的 `bash` 工具实际执行命令的 shell 保持一致：

```text
Windows -> cmd /C
其他系统 -> sh -c
```

### 4. Git Status 数据流

Git 状态使用一条命令采集：

```text
git status --porcelain=v1 -b
```

成功时输出类似：

```text
## branch...origin/branch [ahead 1]
 M src/internal/agent/loop.go
?? src/internal/agent/environment.go
```

这能告诉模型：

- 当前分支及 upstream 摘要。
- ahead / behind 情况。
- 工作区中 staged、unstaged、untracked 文件状态。

保护策略：

- `200ms` 超时。
- 最多 `20` 行。
- 最多 `2000` 字符。
- 超出后追加 `... truncated`。
- 失败、超时、非 git 目录时返回空字符串。
- 同一 cwd 失败后 `30s` 内退避，不反复执行慢命令。
- 子进程不继承普通用户环境；Windows 下只保留 `SystemRoot/WINDIR`，避免 `GIT_DIR`、`GIT_WORK_TREE` 等变量影响仓库探测。

GitStatus 为空时，environment reminder 不渲染 Git 段，避免浪费 token。

### 5. Plan Mode Reminder 状态

Plan Mode 的约束不再长期写在稳定系统提示里，而是按运行模式和轮次注入：

```text
ModeExecute:
  Reminders = [environment]

ModePlan:
  Reminders = [environment, plan_mode]
```

完整 Plan reminder 的注入规则：

```text
iteration 1  -> full
iteration 6  -> full
iteration 11 -> full
其他轮次      -> compact
```

实现函数：

```go
ShouldInjectFullPlanReminder(iteration, interval)
```

默认 interval 是 5。这个设计让模型在长计划任务中周期性重新看到完整只读约束，同时避免每轮重复大段 Plan Mode 指令。

### 6. Provider 映射数据流

Agent 传给 provider 的数据：

```text
msgs           - conversation 历史
tools          - 当前模式可见工具定义
opts.Prompt    - StableSystem + Reminders
```

OpenAI 当前映射：

```text
messages:
  system: StableSystem
  system: reminder 1
  system: reminder 2
  conversation messages...

tools:
  ChatCompletionToolUnionParam
```

Anthropic 当前映射：

```text
System:
  text block: StableSystem + cache_control
  text block: reminder 1
  text block: reminder 2

Messages:
  conversation messages

Tools:
  ToolParam，最后一个 tool 设置 cache_control
```

这个实现已经把 prompt payload 作为统一抽象传入 provider，但当前 reminder 仍被映射到 system 区域。这样优先级较高，行为直接；代价是动态 reminder 变化时可能影响 system 区域缓存命中。后续如果继续优化缓存，可以把动态 reminder 改成消息通道中的 `<system-reminder>` 文本，而稳定 system 只保留长期不变模块。

### 7. Usage / Cache Usage 数据流

Provider 流式事件里如果包含 usage，会吐出：

```go
StreamEvent{Usage: &Usage{...}}
```

Agent collector 做两件事：

```text
1. mergeUsage 到本轮 ModelResponse
2. 发送 EventUsage 给 TUI
```

TUI 只在 `Usage.Available=true` 时更新 token 状态。Cache usage 可用时额外显示 cache read/create；不可用时只显示普通 token。

Anthropic 当前从 message delta usage 中解析：

```text
input_tokens
output_tokens
cache_creation_input_tokens
cache_read_input_tokens
```

OpenAI Chat Completions 当前只解析普通 prompt/completion/total token，cache 字段标记为不可用。

## 关键实现细节

### modules.go：稳定模块

`DefaultModules()` 固定返回七个模块：

```text
Identity
System Constraints
Task Modes
Action Execution
Tool Use
Tone
Text Output
```

模块内容强调：

- 真实仓库上下文优先。
- 不覆盖用户已有改动。
- 工具失败后要读错误并调整策略。
- 编辑前先读文件。
- glob 搜路径，grep 搜内容，bash 用于构建、测试、脚本等 shell-only 操作。

`BuildStableSystem` 会验证模块合法性：

- kind 不能为空。
- title 不能为空。
- content 不能为空。
- 固定模块之后追加的模块必须 `Optional=true`。

模块之间用空行分隔，渲染格式稳定：

```text
# Title
Content

# Next Title
Content
```

### runtime.go：Payload 边界

`Runtime` 只保存 stable string：

```go
type Runtime struct {
	stable string
}
```

这样每轮不需要重新拼接固定模块，只需要构造动态 reminder。

`BuildPayload` 的输出边界很清楚：

```go
type Payload struct {
	StableSystem string
	Reminders    []Reminder
}
```

Provider 只依赖这个 payload，不再自己读取全局 prompt 文本。

### reminder.go：动态 reminder

Environment reminder 每轮都会生成，因为它包含动态信息：

- cwd
- os
- arch
- shell
- date
- mode
- iteration
- git status

Plan Mode reminder 只有在 `Mode == "plan"` 时生成。

`emptyDefault` 用于避免空字段直接进入 prompt：

```text
空 cwd -> unknown
空 os  -> unknown
空 mode -> execute
```

Git status 会先标准化 Windows 换行：

```go
strings.ReplaceAll(value, "\r\n", "\n")
```

再逐行缩进到 reminder 中。

### environment.go：环境采集

把环境采集从 `loop.go` 抽出后，`loop.go` 不再关心 cwd、OS、shell、git 的来源，只负责 Agent 状态机。

`collectGitStatus` 是 best-effort：

```text
有结果 -> 返回截断后的 porcelain 输出
失败/超时/非 git -> 返回 ""
```

它不会把失败作为 Agent 错误冒泡，因为 Git 状态只是辅助上下文，不应该阻塞模型请求。

失败退避缓存只缓存失败，不缓存成功。这样：

- 慢/失败仓库不会每轮卡超时。
- 正常仓库每轮仍能看到工具执行后的最新 dirty 状态。

### bash.go：Shell 类型

`CommandShellName()` 把 shell 名称从 bash 工具中抽出来：

```go
func CommandShellName() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "sh"
}
```

`BashTool.Execute` 也复用这个函数，避免 prompt 中写的 shell 与实际执行 shell 不一致。

### agent.go / loop.go：Agent 接入

Agent 初始化时持有 Prompt Runtime：

```go
type Agent struct {
	provider      llm.Provider
	registry      *tools.Registry
	promptRuntime *prompt.Runtime
}
```

每轮请求模型时：

```go
Prompt: a.promptPayload(ctx, opts, iteration)
```

而 `promptPayload` 现在只做一件事：

```go
return a.promptRuntime.BuildPayload(buildRequestContext(ctx, opts, iteration))
```

这使得 loop 主状态机保持干净。

### tools 描述强化

六个内置工具的 description 补充了提示规则：

- `read_file`：读取真实上下文，编辑前先读。
- `edit_file`：适合精确局部修改，依赖已知 old text。
- `write_file`：用于创建文件或明确覆盖全文件。
- `glob`：用于路径搜索。
- `grep`：用于内容搜索。
- `bash`：可能有副作用，文件读写搜索优先用专用工具。

这和 stable system 中的工具规则形成双重强化：模型在全局规则和工具选择时都能看到同一类约束。

## 状态变化总结

### Conversation 状态

不变。Conversation 仍然只保存真实交互：

```text
user message
assistant message / tool_calls
tool result
```

不会保存：

```text
environment reminder
plan mode reminder
git status
```

这避免动态上下文污染长期历史。

### Agent 状态

Agent 新增 `promptRuntime` 字段。RunOptions 新增 `ReminderInterval`。

Loop 过程新增一层 prompt payload 构造：

```text
每轮请求模型前
  -> buildRequestContext
  -> BuildPayload
  -> StreamOptions
```

其他停止条件、工具调度、取消、Plan/Do 流程保持 Phase 3 行为。

### Provider 状态

Provider 接口增加 `StreamOptions`，但 provider 内部流式解析状态基本不变。

OpenAI 仍维护 tool call index 聚合状态。

Anthropic 仍维护 tool_use JSON 累积状态和 finish reason。

新增的是 usage/cache usage 的上报路径。

### TUI 状态

TUI 没有新增主状态机，只扩展 usage 状态文案：

```text
普通 usage:
  tokens N

cache usage 可用:
  tokens N cache read R/create C
```

## 测试覆盖

### prompt 包

覆盖点：

- 固定模块顺序稳定。
- 可选模块必须声明 Optional。
- stable system 不包含 cwd、日期、iteration、git status 等动态内容。
- environment reminder 包含 cwd、OS、arch、shell、date、mode、iteration、git status。
- Plan Mode 第 1/6/11 轮注入 full reminder。
- Plan Mode 其他轮次注入 compact reminder。
- Execute Mode 不注入 Plan reminder。

### agent 包

覆盖点：

- Agent 会把 prompt payload 传给 provider。
- Reminder 不进入 conversation。
- Execute/Plan 模式下 reminder 种类正确。
- `buildRequestContext` 能填充 mode、iteration、cwd、OS、arch、shell、now、interval。
- `limitGitStatus` 会截断过长输出。
- 空 GitStatus 返回空。

### llm / usage

覆盖点：

- Provider 接口编译通过。
- Collector 能保留 cache usage。
- UsageEvent 能传递 cache read/create 字段。

### tools

覆盖点：

- Registry 工具描述包含关键规则词。
- 工具 schema 和执行逻辑不因描述强化而改变。

验证命令：

```powershell
cd src
$env:GOCACHE = Join-Path (Resolve-Path ..).Path ".gocache"
go test ./...
```

本阶段当前验证结果：全项目测试通过。

## 人工场景

Phase 4 增加 `manual-scenarios.md`，用于人工观察结构化 prompt 的效果。

场景覆盖：

- 普通编辑任务是否先读再改。
- 路径查找是否优先 glob。
- 内容查找是否优先 grep。
- Plan Mode 是否保持只读。
- 多轮 Plan Mode 是否持续记得约束。
- 工具错误后是否调整策略。
- cache usage 可用时 UI 是否显示 read/create。

这些场景不是自动 benchmark，而是为了后续复盘 prompt 改造效果时有共同观察口径。

## 设计取舍

### 为什么 StableSystem 和 Reminders 分开

稳定 prompt 和动态上下文变化频率不同。

稳定内容适合缓存，也适合测试顺序和文本格式；动态内容每轮都可能变化，例如 iteration、date、git status。把它们混在一起，会让缓存策略、测试和后续扩展都变复杂。

因此 Phase 4 用 `Payload` 明确分离：

```text
StableSystem: 长期规则
Reminders: 本轮动态补充
```

### 为什么 GitStatus 用 porcelain 原文

结构化解析 Git 状态会引入更多代码和边界处理。当前阶段只需要让模型知道“仓库是否 dirty、哪些文件变了、分支大致状态”，`git status --porcelain=v1 -b` 已经足够稳定且短小。

后续如果需要更精细的 Git 能力，可以新增专用 git tool 或解析结构化 GitContext。

### 为什么 GitStatus 不失败即报错

Git 状态只是辅助上下文。非 git 目录、git 不可用、命令超时都不应阻止 Agent 请求模型。

所以策略是：

```text
成功 -> 注入
失败 -> 空字符串
```

### 为什么 Shell 取工具实际 shell

用户环境变量中的 shell 不一定等于 OneCode 工具执行命令的 shell。尤其 Windows 下，Agent 可能运行在 PowerShell 环境里，但当前 `bash` 工具实际使用的是 `cmd /C`。

提示词告诉模型的 shell 应该与工具执行一致，否则模型可能生成不兼容命令。

### 为什么 Plan Mode full reminder 要按轮次重复

Plan Mode 是强约束：只读、不能修改、不能执行副作用工具。

只在第一轮注入，模型在多轮工具调用后可能弱化约束；每轮完整注入又浪费 token。因此使用：

```text
首轮完整
每 5 轮完整
其他轮次精简
```

这是约束稳定性和上下文体量之间的折中。

## 当前限制

- Provider 层当前仍把 reminders 映射到 system 区域。这样优先级强，但动态内容可能影响 system prompt 的缓存命中。后续可以进一步改成“稳定 system + 消息通道 `<system-reminder>`”。
- OpenAI Chat Completions 当前 cache usage 标记为不可用；如果 SDK/接口提供 cached tokens 字段，可以补充解析。
- Anthropic `MaxTokens` 当前仍是 provider 内部固定值，未做模型级策略表。
- GitStatus 只是 porcelain 截断文本，不解析成结构化 branch/dirty/ahead/behind 字段。
- Shell 目前只表达 `cmd` 或 `sh`，没有区分 PowerShell、bash、zsh 等更细粒度 shell。
- Environment reminder 每轮注入，虽然内容较短，但 GitStatus dirty 文件很多时仍会占 token；当前通过限行限字符控制。
- 没有自动化评估 prompt 改造前后的行为差异，只提供人工场景。
- 没有加载项目自定义指令、Skill、长期记忆或 MCP 工具说明。

## 复盘要点

Phase 4 的核心价值不是“多写了一段 system prompt”，而是把提示词变成可维护的数据流：

```text
稳定规则
  -> 模块化
  -> 可测试
  -> 可缓存

动态上下文
  -> 每轮采集
  -> reminder 注入
  -> 不污染 conversation

Provider 差异
  -> 收在 llm 适配层
  -> Agent/TUI 不感知具体 API 消息格式
```

这为后续几件事打基础：

- 项目指令文件可以作为 optional module 或 runtime reminder 接入。
- Skill 可以以 optional module 或 active reminder 形式注入。
- 长期记忆可以进入可选模块或运行时补充消息。
- 权限系统可以把模式和决策结果作为动态 reminder 注入。
- Prompt cache 可以围绕 StableSystem 和工具定义继续优化。

最终 OneCode 从 Phase 3 的“能循环调用工具”进一步变成 Phase 4 的“有稳定行为准则和运行时环境意识的 coding agent”。

# 持久化记忆与会话恢复 Plan

## 架构概览

Phase 8 把 OneCode 的“记忆”划分为四种不同生命周期的数据，但只新增一个 `internal/memory` 包：

```text
当前工作记忆
  conversation.Conversation
  只保存当前模型上下文，由现有上下文管理和压缩机制维护

显式项目记忆
  ONECODE.md
  由用户手写，保存项目约定、技术栈和长期指令

情景记忆
  .onecode/sessions/*.jsonl
  记录每次会话中的完整消息和上下文快照，支持中断后恢复

长期提炼记忆
  .onecode/memory/ 与 ~/.onecode/memory/
  由后台 LLM 从自然结束的回合中提取笔记，通过 INDEX.md 注入后续请求
```

这些能力属于同一个“持久化记忆”领域，因此集中在 `internal/memory`；包内仍使用职责明确的具体类型，不建立统一的 `MemoryManager`：

```text
InstructionLoader  加载并展开 ONECODE.md
SessionStore       扫描、恢复和清理会话
SessionJournal     追加当前会话记录
NoteStore          读写笔记并维护索引
Worker             串行执行自动笔记提取
```

现有包继续承担原来的职责：

```text
conversation
  当前工作上下文和上下文压缩，不感知磁盘会话

prompt
  组装稳定 System Prompt 和 request-local reminders，不读取记忆文件

agent
  执行 ReAct loop，并通过事件报告 Conversation 的追加或整体替换

tui
  协调当前 SessionJournal、会话恢复、动态 prompt 内容和后台笔记任务

main
  创建具体组件并管理启动和退出生命周期
```

主依赖方向保持无环：

```text
main ─────────────→ memory, tui
tui ──────────────→ memory, agent, conversation
agent ─────────────→ conversation, prompt
memory ────────────→ llm
prompt ────────────→ 无 memory 依赖
conversation ──────→ 无 memory 依赖
```

## 核心数据结构

### 作用域与自动笔记

```go
type Scope string

const (
    ScopeUser    Scope = "user"
    ScopeProject Scope = "project"
)

type NoteCategory string

const (
    CategoryPreference       NoteCategory = "preference"
    CategoryCorrection       NoteCategory = "correction"
    CategoryProjectKnowledge NoteCategory = "project_knowledge"
    CategoryReference        NoteCategory = "reference"
)

type Note struct {
    ID              string
    Scope           Scope
    Category        NoteCategory
    Title           string
    Summary         string
    Body            string
    SourceSessionID string
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

`Note` 映射为一份带 YAML frontmatter 的 Markdown 文件。`Summary` 用于生成紧凑索引，`Body` 保存完整内容；项目级是默认作用域，用户级只保存用户明确表达的跨项目长期偏好。

### 项目指令

```go
type InstructionSet struct {
    Content  string
    Sources  []string
    Warnings []LoadWarning
}

type LoadWarning struct {
    Path    string
    Message string
}

type InstructionLoader struct {
    ProjectRoot string
    UserRoot    string
}
```

`Content` 是最终按优先级拼接的内容；`Sources` 保存成功加载的文件；`Warnings` 保存非法引用、循环引用和读取失败等非致命问题。递归深度和 `visited` 集合属于加载过程的内部状态，不暴露为公共模型。

### 会话记录

```go
type RecordType string

const (
    RecordMessage  RecordType = "message"
    RecordSnapshot RecordType = "snapshot"
)

type SessionRecord struct {
    Type      RecordType    `json:"type"`
    Timestamp time.Time     `json:"timestamp"`
    Message   *llm.Message  `json:"message,omitempty"`
    Messages  []llm.Message `json:"messages,omitempty"`
}
```

`message` 表示追加一条完整消息；`snapshot` 表示上下文压缩或工具结果裁剪后，以当前有效消息列表替换此前历史。snapshot 是恢复 Phase 7 上下文状态所必需的记录，不是单独的会话元数据。

```go
type SessionInfo struct {
    ID           string
    Title        string
    CreatedAt    time.Time
    UpdatedAt    time.Time
    MessageCount int
}

type RestoreResult struct {
    Info         SessionInfo
    Messages     []llm.Message
    LastActiveAt time.Time
    SkippedLines int
    Truncated    bool
}
```

`SessionInfo` 通过扫描 JSONL 动态计算，不另存 meta 文件。`RestoreResult` 把恢复异常显式返回给 TUI，但不判断当前模型窗口；恢复后的第一次 Agent 请求仍先经过现有 `Conversation.Preflight`，由 Phase 7 的估算和压缩机制在正式模型请求前处理超限历史。

### 自动笔记任务

```go
type MutationOperation string

const (
    MutationSkip   MutationOperation = "skip"
    MutationCreate MutationOperation = "create"
    MutationUpdate MutationOperation = "update"
)

type NoteMutation struct {
    Operation MutationOperation
    TargetID  string
    Note      Note
}

type TurnCandidate struct {
    SessionID string
    Messages  []llm.Message
    StoppedAt time.Time
}
```

一轮可以产生多个 mutation，例如更新项目知识的同时新增一条用户偏好。`update` 必须指向本地已存在的笔记；新笔记 ID 和文件名始终由本地生成。

### 动态 Prompt 上下文

```go
type SessionPromptContext struct {
    Instructions string
    MemoryIndex  string
    ResumeGap    string
}
```

该结构由 TUI 在启动 Agent 前组装，经 `agent.RunOptions` 传给 `prompt.Runtime`。它只影响当前模型请求，不进入稳定 System Prompt、Conversation 或 JSONL。

### Conversation 变化事件

```go
type ConversationEventKind int

const (
    ConversationAppend ConversationEventKind = iota
    ConversationSnapshot
)

type ConversationEvent struct {
    Kind     ConversationEventKind
    Message  *llm.Message
    Messages []llm.Message
}
```

Agent 不直接依赖 `SessionJournal`，而是通过现有事件流报告“追加消息”和“整体替换”两类事实。TUI 按事件顺序更新本轮候选并持久化。

## 模块设计

### InstructionLoader

加载顺序固定为：

1. `项目根/.onecode/ONECODE.md`
2. `项目根/ONECODE.md`
3. `~/.onecode/ONECODE.md`

高优先级内容排在前面，并使用带来源路径的边界包裹：

```text
<project-instructions source=".../.onecode/ONECODE.md">
...
</project-instructions>
```

`@include` 是占据完整一行的指令，支持普通路径和带空格的引号路径：

```text
@include docs/backend-rules.md
@include "docs/path with spaces.md"
```

include 在出现位置原地展开。相对路径以当前文件目录为基准；项目级来源只能引用项目根内文件，用户级来源只能引用 `~/.onecode` 内文件。加载器解析绝对路径和符号链接后再检查边界，递归深度最多为 5，并使用规范化真实路径作为 `visited` key。非法引用只产生 warning。

### SessionStore 与 SessionJournal

程序启动时只创建空的内存 Conversation；第一次提交用户消息时才创建会话文件，避免产生空会话。会话 ID 格式为：

```text
YYYYMMDD-HHMMSS-xxxx
```

`xxxx` 由 `crypto/rand` 生成两个随机字节后编码成四位十六进制字符串。

主要操作为：

```go
func (s *SessionStore) Create() (*SessionJournal, error)
func (s *SessionStore) List() ([]SessionInfo, error)
func (s *SessionStore) Latest() (RestoreResult, error)
func (s *SessionStore) Load(id string) (RestoreResult, error)
func (s *SessionStore) Cleanup(before time.Time) error

func (j *SessionJournal) AppendMessage(message llm.Message) error
func (j *SessionJournal) AppendSnapshot(messages []llm.Message) error
func (j *SessionJournal) Close() error
```

Journal 使用互斥锁保护同一文件的追加，每条完整 JSON 记录写入后添加换行并刷新用户态缓冲区。用户消息在提交时保存；助手消息在完整收集后保存；工具结果在每个工具结束后保存；流式 token 不保存。

恢复使用 `bufio.Reader` 逐行读取，避免 `bufio.Scanner` 默认 64KB 上限。坏行跳过；message 追加；snapshot 替换当前列表。重放完成后校验工具调用链，只保留最长有效前缀。SessionStore 不估算 Token，也不导入 conversation；恢复后的第一次 Agent 请求由现有 Preflight 在调用正式模型前判断并压缩，压缩后的有效历史再通过 snapshot 事件追加到 JSONL。

### NoteStore

用户级和项目级目录采用相同结构：

```text
memory/
├── INDEX.md
└── notes/
    └── <note-id>.md
```

NoteStore 负责 Markdown frontmatter 解析、笔记写入、精确去重和索引重建。笔记与索引更新使用“同目录临时文件 + rename”的原子替换方式。`INDEX.md` 根据实际笔记重新生成，不做脆弱的字符串局部修改。

每份用户级或项目级索引自身最多 200 行、25KB。注入时再应用总计 200 行、25KB 的组合上限：用户级先加入并预留最多约 25% 空间；用户内容不足时，剩余预算全部提供给项目级索引。

### Worker

Worker 只接受自然结束回合，使用容量为 16 的有界 channel 和单个 goroutine 串行处理。`Enqueue` 非阻塞，队列满时放弃当前自动笔记任务并记录 warning，不阻塞 TUI。

处理流程：

1. 检查 memory 是否启用。
2. 跳过空回合、极短问候或确认。
3. 计算规范化回合 SHA-256，跳过进程内完全重复任务。
4. 读取当前用户级和项目级索引。
5. 使用该回合对应的 `llm.Provider` 发起无工具的记忆提取请求。
6. 收集完整文本并用 `encoding/json` 解析 mutation 数组。
7. 校验 operation、scope、category 和 update 目标。
8. 在本地检查敏感信息并执行精确去重。
9. 应用 mutation，并原子重建受影响的索引。

LLM 负责语义合并；本地代码不实现模糊相似度算法。Worker 复用现有 `llm.Provider`，不新增 Extractor 接口，也不开放 max token 配置。

## 数据流与状态变化

### 启动

```text
main 加载配置
  -> 创建 InstructionLoader、SessionStore、NoteStore、Worker
  -> 加载三层 ONECODE.md
  -> 异步清理 30 天前的合法 session 文件
  -> 创建新的空 Conversation
  -> 启动 TUI
```

指令读取和过期清理失败只展示 warning，不阻断启动。自动 session 和 memory 目录加入 `.gitignore`，手写的 `.onecode/ONECODE.md` 不忽略。

### 普通请求

```text
用户提交
  -> 首次请求时创建 SessionJournal
  -> Conversation.AddUser
  -> AppendMessage(user)
  -> 读取最新 memory indexes
  -> 组合 SessionPromptContext
  -> Agent.Run
```

每次模型请求的动态 reminder 顺序为：环境信息、项目指令、长期记忆索引、恢复时间跨度、Plan Mode。Plan Mode 最后注入以强化只读约束。项目指令和索引在每次 Agent 请求前提供，但不改变稳定 System Prompt。

### Agent 消息持久化

```text
Agent 修改 Conversation
  -> 发送 EventConversation
  -> TUI 收到事件
  -> 更新当前 TurnCandidate 副本
  -> SessionJournal 追加 message 或 snapshot
```

助手消息、带工具调用的助手消息和每个工具结果触发 append。Preflight、PostToolResults、手动压缩或紧急压缩导致消息列表变化时触发 snapshot。事件携带消息副本，避免 TUI 并发读取 Agent 正在修改的 Conversation。

Session 创建或追加失败不会回滚 Conversation，也不会把存储错误发送给 LLM；TUI 展示 warning，并停止继续使用已经失败的 Journal。

### `/continue` 与 `/resume`

`/continue` 选择更新时间最新且能恢复出有效消息的会话。`/resume` 进入会话选择器，展示标题、ID、更新时间和消息数，并支持上下键、Enter 和 Esc。

恢复执行以下原子切换：

```text
扫描并重放 JSONL
  -> 校验工具调用完整性
  -> 打开原 session 供后续追加
  -> Conversation.Restore(messages)
  -> 成功后替换当前 Conversation 和 Journal
```

加载和 Journal 打开成功前保留当前 Conversation。恢复失败不会破坏当前会话。恢复后如果历史超限，下一次 `Agent.Run` 的 Preflight 会先尝试现有压缩流程，成功后发送 snapshot，失败则停止该轮请求并报告错误，不把超限历史直接发送给正式模型。距上次活动超过 24 小时时，TUI 保存一次性 `ResumeGap`；它只在恢复后的第一次真实模型请求中注入，其他 slash command 不会提前消耗。

### 自动笔记

TUI 从本轮用户消息和 `EventConversation` 构造独立的回合副本。只有收到 `EventDone(StopModelDone)` 才执行：

```text
TUI 组装 TurnCandidate
  -> Worker.Enqueue(provider, candidate)
  -> TUI 立即恢复输入
  -> Worker 后台串行提取和写入
```

取消、流错误、迭代上限、无效工具上限等停止原因均不生成自动笔记。Worker 不修改当前 Prompt 或 Conversation；下一轮 `startAgentRun` 重新读取索引后，新记忆才进入模型上下文。

## 文件组织

新增包保持为四个实现文件，不预先拆出只含少量辅助函数的小文件：

```text
src/internal/memory/
├── instructions.go
├── instructions_test.go
├── session.go
├── session_test.go
├── notes.go
├── notes_test.go
├── worker.go
└── worker_test.go
```

职责分配：

- `instructions.go`：三层加载、include 展开、深度和路径安全。
- `session.go`：会话类型、Journal、扫描、恢复和清理。
- `notes.go`：笔记类型、frontmatter、敏感信息、索引和原子写入。
- `worker.go`：候选过滤、后台队列、LLM 提取和 mutation 校验。

现有文件修改范围：

```text
src/cmd/onecode/main.go
  组件创建和关闭

src/internal/config/config.go
src/internal/config/config_test.go
  memory.enabled 配置、默认值和两层合并

src/internal/prompt/runtime.go
src/internal/prompt/reminder.go
  SessionPromptContext 和三种动态 reminder

src/internal/agent/events.go
src/internal/agent/agent.go
src/internal/agent/loop.go
src/internal/agent/loop_test.go
  PromptContext、Conversation 变化事件，以及正常循环和手动压缩触发点

src/internal/conversation/conversation.go
src/internal/conversation/conversation_test.go
  Conversation.Restore

src/internal/tui/model.go
src/internal/tui/model_test.go
  Journal 生命周期、事件持久化、PromptContext 和 Worker 触发

src/internal/tui/session_picker.go
src/internal/tui/session_picker_test.go
  /resume 列表状态、按键和渲染

.gitignore
  忽略 .onecode/sessions/ 和 .onecode/memory/
```

本阶段不创建 `internal/instructions`、`internal/session`、`memory/manager.go`、通用 Repository、事件总线或第二套消息类型。

## 关键技术决策

### JSONL 和一致性

- 会话采用完整消息级追加，不保存流式片段。
- 不维护 meta 文件，列表信息由 JSONL 扫描得到。
- 工具调用通过 `ToolCall.ID` 和 `ToolResult.ToolUseID` 配对。
- 一组 tool calls 在下一条普通 user/assistant 消息前必须全部完成。
- 孤立工具结果或末尾未完成调用导致恢复截断到此前最长有效前缀。

### 路径与标识安全

include 路径使用 `Abs -> EvalSymlinks -> Rel` 判断是否仍在允许根内，不使用字符串前缀。`/resume` 只接受本地 session ID 格式；清理任务只删除符合命名规则的 JSONL；创建笔记时忽略 LLM 提供的文件名，update 只能指向现有本地 ID。

### 配置默认值

配置读取阶段使用可选布尔值区分“未配置”和“明确关闭”：

```go
type MemoryConfig struct {
    Enabled *bool `yaml:"enabled"`
}
```

优先级为项目明确配置、用户明确配置、默认 `true`。合并后转换为普通 bool，运行时不传播三态逻辑。本阶段不暴露队列大小、索引预算和恢复阈值等调优参数。

### 敏感信息

写入前本地检查 PEM 私钥、常见 API key/token 前缀、Bearer token、JWT、敏感变量赋值和异常长的高熵凭据候选。命中后跳过对应 mutation，warning 不输出原文。本阶段不自动打码后保存。

### Prompt 缓存

稳定 System Prompt 保持字节不变。项目指令、记忆索引、恢复提醒、环境和模式状态均走 request-local reminder。动态内容仍会消耗每次请求的输入 token，因此通过索引的行数和字节预算限制增长，不注入全部笔记正文。

### 故障隔离

| 故障 | 行为 |
|---|---|
| 指令读取失败 | warning，继续启动 |
| 非法 include | warning，跳过引用 |
| session 创建或追加失败 | warning，继续内存会话 |
| 会话恢复失败 | 保留当前会话 |
| 单条 JSONL 损坏 | 跳过并继续扫描 |
| 自动笔记提取失败 | 放弃本轮更新 |
| INDEX 重建失败 | 保留旧索引 |
| 过期清理失败 | warning，不影响启动 |

持久化记忆是增强能力，不成为 Agent 完成当前编码任务的硬依赖。

## 需求覆盖

- F1-F7：由 InstructionLoader 的优先级、include 展开和路径安全实现。
- F8-F20：由 SessionStore、SessionJournal、恢复校验、snapshot 和 TUI 恢复状态实现。
- F21-F32：由 NoteStore、Worker、作用域策略、索引预算和敏感信息检测实现。
- F33-F38：由 PromptContext、动态 reminder、配置默认值、Git 忽略和故障隔离实现。
- N1-N25：通过 JSONL 追加、原子文件替换、无环依赖、串行 Worker、有界上下文和单元测试覆盖。

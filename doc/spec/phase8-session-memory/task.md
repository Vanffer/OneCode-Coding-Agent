# 持久化记忆与会话恢复 Tasks

## 文件清单

| 操作 | 文件 | 职责 |
|---|---|---|
| 新建 | `src/internal/memory/instructions.go` | 三层 `ONECODE.md` 加载、include 展开与路径安全 |
| 新建 | `src/internal/memory/instructions_test.go` | 指令优先级、递归、环路、深度和越界测试 |
| 新建 | `src/internal/memory/session.go` | JSONL 会话、Journal、扫描、恢复、列表和清理 |
| 新建 | `src/internal/memory/session_test.go` | 追加、坏行、工具配对、snapshot、列表和清理测试 |
| 新建 | `src/internal/memory/notes.go` | Markdown 笔记、frontmatter、索引、预算和敏感信息检查 |
| 新建 | `src/internal/memory/notes_test.go` | 笔记更新、原子性、去重、索引预算和凭据拦截测试 |
| 新建 | `src/internal/memory/worker.go` | 自动记忆过滤、串行队列、LLM 提取与 mutation 应用 |
| 新建 | `src/internal/memory/worker_test.go` | 队列、过滤、结构校验、串行更新和失败隔离测试 |
| 新建 | `src/internal/tui/session_picker.go` | `/resume` 会话选择器状态、按键与渲染 |
| 新建 | `src/internal/tui/session_picker_test.go` | 会话选择、取消和列表渲染测试 |
| 修改 | `src/internal/config/config.go` | `memory.enabled` 默认值与用户/项目配置合并 |
| 修改 | `src/internal/config/config_test.go` | Memory 配置默认、继承和覆盖测试 |
| 修改 | `src/internal/prompt/runtime.go` | 接收会话级动态 Prompt 上下文 |
| 修改 | `src/internal/prompt/reminder.go` | 指令、记忆索引和恢复时间跨度 reminder |
| 修改 | `src/internal/prompt/runtime_test.go` | 稳定 System Prompt 和动态上下文隔离测试 |
| 修改 | `src/internal/prompt/reminder_test.go` | reminder 顺序、内容和注入频率测试 |
| 修改 | `src/internal/conversation/conversation.go` | 从恢复消息重建当前 Conversation |
| 修改 | `src/internal/conversation/conversation_test.go` | Restore 复制、状态重置和协议保留测试 |
| 修改 | `src/internal/agent/events.go` | Conversation append/snapshot 事件与 PromptContext |
| 修改 | `src/internal/agent/loop.go` | 正常 Agent loop 发送持久化事件 |
| 修改 | `src/internal/agent/agent.go` | 手动压缩成功后发送 snapshot 事件 |
| 修改 | `src/internal/agent/loop_test.go` | 消息事件顺序、snapshot 和动态 prompt 转发测试 |
| 修改 | `src/internal/tui/model.go` | 组件生命周期、会话持久化、恢复、Prompt 注入和 Worker 触发 |
| 修改 | `src/internal/tui/model_test.go` | TUI 会话状态、失败隔离和自动记忆触发测试 |
| 修改 | `src/cmd/onecode/main.go` | Memory 组件组装、启动警告、清理和退出关闭 |
| 修改 | `.gitignore` | 忽略自动 session 和 memory 数据，不忽略手写项目指令 |

## T1：增加 Memory 配置并实现两层合并

**文件：** `src/internal/config/config.go`、`src/internal/config/config_test.go`  
**依赖：** 无

**步骤：**

1. 增加只包含可选 `enabled` 字段的 Memory 配置结构。
2. 在用户级和项目级配置合并时保留“未设置”状态。
3. 按“项目明确值 > 用户明确值 > 默认 true”计算最终开关。
4. 确保最终运行配置对调用方暴露明确的启用状态。
5. 增加缺省、用户关闭、项目覆盖和项目继承测试。

**验证：** 在 `src` 目录运行 `go test ./internal/config`。

## T2：实现三层项目指令基础加载

**文件：** `src/internal/memory/instructions.go`、`src/internal/memory/instructions_test.go`  
**依赖：** 无

**步骤：**

1. 定义 `InstructionLoader`、`InstructionSet` 和加载 warning。
2. 按项目 `.onecode`、项目根、用户目录的顺序查找入口文件。
3. 正常跳过不存在的入口文件。
4. 为成功加载的入口增加包含来源路径的边界标签。
5. 测试三层顺序、缺失文件和空文件行为。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run TestInstructionLoader`。

## T3：实现 `@include` 递归展开

**文件：** `src/internal/memory/instructions.go`、`src/internal/memory/instructions_test.go`  
**依赖：** T2

**步骤：**

1. 识别独占一行的普通路径和引号路径 include 指令。
2. 以当前文件目录为基准解析相对路径，并在原位置展开内容。
3. 使用真实规范化路径维护本次加载的 `visited` 集合。
4. 将递归深度限制为 5，超过时记录 warning 并跳过。
5. 测试多层引用、重复引用、循环引用和带空格路径。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestInstruction.*Include'`。

## T4：实现指令引用路径沙箱

**文件：** `src/internal/memory/instructions.go`、`src/internal/memory/instructions_test.go`  
**依赖：** T3

**步骤：**

1. 对允许根和目标执行绝对路径及符号链接解析。
2. 使用 `filepath.Rel` 判断目标是否留在允许根内。
3. 项目入口约束在项目根，用户入口约束在 `~/.onecode`。
4. 拒绝 `..`、外部绝对路径、跨卷路径和可用环境中的软链接逃逸。
5. warning 包含来源文件和失败原因，但继续加载其他内容。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestInstruction.*(Escape|Warning|Symlink)'`。

## T5：建立会话记录、ID 和 Journal 创建

**文件：** `src/internal/memory/session.go`、`src/internal/memory/session_test.go`  
**依赖：** 无

**步骤：**

1. 定义 message/snapshot 记录、会话摘要、恢复结果和行级 warning。
2. 使用当前时间和 `crypto/rand` 生成 `YYYYMMDD-HHMMSS-xxxx` ID。
3. 校验外部传入的 session ID，禁止通过 ID 拼接任意路径。
4. 创建项目 `.onecode/sessions` 目录和新的 Journal 文件。
5. 测试 ID 格式、同秒随机后缀和非法 ID 拒绝。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestSession(ID|Create|InvalidID)'`。

## T6：实现完整记录追加和 Journal 生命周期

**文件：** `src/internal/memory/session.go`、`src/internal/memory/session_test.go`  
**依赖：** T5

**步骤：**

1. 使用互斥锁串行化同一 Journal 的写入。
2. 实现 `AppendMessage` 和 `AppendSnapshot`。
3. 确保每次追加只生成一条完整 JSON 行并刷新缓冲区。
4. 实现关闭后的重复关闭和写入错误处理。
5. 测试并发追加仍可逐行解析，且 snapshot 保留完整消息列表。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestSessionJournal'`。

## T7：实现 JSONL 重放和会话摘要计算

**文件：** `src/internal/memory/session.go`、`src/internal/memory/session_test.go`  
**依赖：** T6

**步骤：**

1. 使用 `bufio.Reader` 读取可能超过 64KB 的单行记录。
2. 按顺序重放 message，遇到 snapshot 时替换当前历史。
3. 跳过坏行并记录文件、行号和解析原因。
4. 从第一条非空用户消息生成 Unicode 安全的 60 字符标题。
5. 从有效记录计算创建时间、更新时间和最终消息数。
6. 测试坏行、超长行、无末尾换行、snapshot 和中文标题。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestSession(Restore|Summary|LongLine|BadLine)'`。

## T8：实现工具调用合法前缀校验

**文件：** `src/internal/memory/session.go`、`src/internal/memory/session_test.go`  
**依赖：** T7

**步骤：**

1. 按 `ToolCall.ID` 建立当前待完成调用集合。
2. 使用 `ToolResult.ToolUseID` 消除对应待完成项。
3. 检测孤立结果、重复结果、ID 不匹配和下一条普通消息过早出现。
4. 在第一处非法工具交互前截断，原始 JSONL 保持不变。
5. 测试单工具、多工具、结果乱序、缺失结果和孤立结果。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestSession.*Tool'`。

## T9：实现会话列表、最近会话和继续追加

**文件：** `src/internal/memory/session.go`、`src/internal/memory/session_test.go`  
**依赖：** T7、T8

**步骤：**

1. 只枚举符合 session 命名格式的 JSONL 文件。
2. 扫描得到 `SessionInfo`，忽略空会话和无有效消息会话。
3. 按最后有效活动时间倒序输出列表。
4. `Latest` 返回最近有效且未过期的会话。
5. 支持打开已有 session 的 Journal 并继续追加。
6. 测试排序、无有效会话、无关文件忽略和原文件续写。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestSession(List|Latest|Reopen)'`。

## T10：实现过期会话清理

**文件：** `src/internal/memory/session.go`、`src/internal/memory/session_test.go`  
**依赖：** T9

**步骤：**

1. 根据最后有效活动时间判断 30 天过期。
2. 接受当前活跃 session ID，并在清理时明确跳过。
3. 只删除合法 session 文件，不触碰目录内其他文件。
4. 单个删除失败时继续处理其他文件并汇总 warning。
5. 使用可控时间测试过期、未过期、活跃和无关文件。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestSessionCleanup'`。

## T11：建立笔记模型和 Markdown frontmatter

**文件：** `src/internal/memory/notes.go`、`src/internal/memory/notes_test.go`  
**依赖：** 无

**步骤：**

1. 定义 Scope、四种 Category、Note 和 mutation 类型。
2. 确定用户级、项目级 memory 与 notes 目录。
3. 使用 `yaml.v3` 编解码 frontmatter，正文保留普通 Markdown。
4. 本地生成稳定笔记 ID，不接受 LLM 文件名。
5. 测试四类笔记往返解析、中文正文和非法 frontmatter。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestNote(Frontmatter|RoundTrip|Invalid)'`。

## T12：实现笔记创建、更新和精确去重

**文件：** `src/internal/memory/notes.go`、`src/internal/memory/notes_test.go`  
**依赖：** T11

**步骤：**

1. 使用同目录临时文件和 rename 原子写入笔记。
2. create 时忽略外部 ID，由 NoteStore 生成 ID 和时间。
3. update 只接受作用域内已存在的 TargetID，并保留创建时间。
4. 对规范化后完全相同的正文和元数据执行本地跳过。
5. 测试 create、update、非法目标、重复内容和写入失败不破坏旧文件。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestNoteStore(Create|Update|Duplicate|Atomic)'`。

## T13：实现单层索引重建和磁盘预算

**文件：** `src/internal/memory/notes.go`、`src/internal/memory/notes_test.go`  
**依赖：** T12

**步骤：**

1. 从实际可解析笔记生成包含分类、标题、摘要和相对路径的 Markdown 索引。
2. 以确定性顺序输出索引，避免无意义重写。
3. 同时执行最多 200 行和 25KB 限制，并保证 UTF-8 不被截断。
4. 只有笔记写入成功后才原子替换 INDEX.md。
5. 测试大量笔记预算、损坏笔记跳过和索引写入失败保留旧索引。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestNoteIndex(Rebuild|Budget|Atomic)'`。

## T14：实现用户级与项目级索引组合

**文件：** `src/internal/memory/notes.go`、`src/internal/memory/notes_test.go`  
**依赖：** T13

**步骤：**

1. 分别读取用户级和项目级 INDEX.md。
2. Memory 关闭时不读取任何索引并返回空内容。
3. 用户级内容优先，并为其预留组合预算的约 25%。
4. 用户级不足时把空余预算让给项目级。
5. 保证合计不超过 200 行和 25KB，并保留来源边界。
6. 测试用户级保留、预算让渡、单层缺失和两层超限。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestCombinedIndex'`。

## T15：实现本地敏感信息检查

**文件：** `src/internal/memory/notes.go`、`src/internal/memory/notes_test.go`  
**依赖：** T12

**步骤：**

1. 检测 PEM 私钥、Bearer token、JWT 和常见 API key 前缀。
2. 检测敏感变量名赋值及异常长凭据候选。
3. 命中时拒绝对应 mutation，不输出命中的原始值。
4. 保留普通代码、哈希值和非敏感技术文本，控制明显误报。
5. 为每类凭据和正常内容增加表驱动测试。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestSensitiveContent'`。

## T16：实现 Worker 队列和本地候选过滤

**文件：** `src/internal/memory/worker.go`、`src/internal/memory/worker_test.go`  
**依赖：** T14、T15

**步骤：**

1. 建立容量 16 的任务 channel 和单 goroutine 消费循环。
2. `Enqueue` 非阻塞，并在关闭或队列满时明确返回 false。
3. 跳过空回合、仅问候/确认的极短回合和关闭状态。
4. 对规范化完整回合计算 SHA-256，跳过进程内精确重复。
5. 实现可取消关闭，避免退出后 goroutine 泄漏。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestWorker(Queue|Filter|Duplicate|Close)'`。

## T17：实现记忆提取请求和流式收集

**文件：** `src/internal/memory/worker.go`、`src/internal/memory/worker_test.go`  
**依赖：** T16

**步骤：**

1. 构造包含候选回合、当前两层索引、作用域规则和输出约束的提取提示词。
2. 使用该任务携带的 `llm.Provider`，工具定义传 nil。
3. 收集完整文本、处理 stream error 和 context timeout。
4. 模型返回工具调用时将本次提取判定为失败。
5. 使用 `encoding/json` 解析纯 JSON 或外层 JSON code fence。
6. 使用 fake provider 测试成功、错误、超时、工具调用和无效 JSON。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestWorkerExtract'`。

## T18：实现 mutation 校验、应用和错误观察

**文件：** `src/internal/memory/worker.go`、`src/internal/memory/worker_test.go`  
**依赖：** T12、T13、T15、T17

**步骤：**

1. 只接受 skip/create/update 和四种已知分类。
2. 对不明确作用域使用项目级；拒绝从临时请求推断用户级偏好。
3. update 校验 TargetID 存在且作用域一致。
4. 敏感 mutation 单独跳过，其余合法 mutation 仍可应用。
5. 串行调用 NoteStore，并让后台错误可被 TUI 观察但不终止 Agent。
6. 测试未知操作、非法分类、越界 update、多 mutation 和连续任务顺序。

**验证：** 在 `src` 目录运行 `go test ./internal/memory -run 'TestWorker(Mutation|Serial|Error)'`。

## T19：增加会话级动态 Prompt 上下文

**文件：** `src/internal/prompt/runtime.go`、`src/internal/prompt/reminder.go`、相关测试  
**依赖：** 无

**步骤：**

1. 定义 `SessionPromptContext` 并接入 `RequestContext` 或等价运行时输入。
2. 增加 instructions、memory index 和 resume gap 三种 ReminderKind。
3. 按环境、指令、记忆、时间跨度、Plan Mode 的顺序构造 reminders。
4. Resume gap 只在 iteration 1 注入；指令和索引按每次模型请求注入。
5. 测试动态内容不改变 StableSystem，空内容不产生空 reminder。

**验证：** 在 `src` 目录运行 `go test ./internal/prompt`。

## T20：增加 Conversation 恢复入口

**文件：** `src/internal/conversation/conversation.go`、`src/internal/conversation/conversation_test.go`  
**依赖：** 无

**步骤：**

1. 实现 `Conversation.Restore(messages)`，复制输入消息切片。
2. 重置旧会话的 usage 和 compaction fuse，但保留当前项目根和上下文配置。
3. 不重新执行工具，也不修改恢复消息的 ToolCall/ToolResult 关联。
4. 测试外部切片后续修改不影响 Conversation，旧状态被正确清理。

**验证：** 在 `src` 目录运行 `go test ./internal/conversation -run 'TestConversationRestore'`。

## T21：扩展 Agent Conversation 事件模型

**文件：** `src/internal/agent/events.go`、`src/internal/agent/loop_test.go`  
**依赖：** T19

**步骤：**

1. 在 RunOptions 中加入 `prompt.SessionPromptContext`。
2. 增加 EventConversation、append/snapshot kind 和消息载荷。
3. 为事件中的消息和消息列表建立不会被后续修改的副本。
4. 保持已有文本、工具、权限、Context 和 Done 事件兼容。
5. 增加事件结构与 PromptContext 转发的基础测试。

**验证：** 在 `src` 目录运行 `go test ./internal/agent -run 'Test.*ConversationEvent|Test.*PromptContext'`。

## T22：在正常 Agent loop 中发送追加事件

**文件：** `src/internal/agent/loop.go`、`src/internal/agent/loop_test.go`  
**依赖：** T21

**步骤：**

1. 普通最终助手消息加入 Conversation 后发送 append。
2. 带工具调用的助手消息加入后发送 append。
3. 每条工具结果加入后分别发送 append。
4. 保证 Conversation 先修改、持久化事件后发送、Done 最后发送。
5. 测试无工具和多工具响应的事件顺序与完整载荷。

**验证：** 在 `src` 目录运行 `go test ./internal/agent -run 'TestLoop.*ConversationAppend'`。

## T23：在上下文替换后发送 snapshot 事件

**文件：** `src/internal/agent/loop.go`、`src/internal/agent/agent.go`、`src/internal/agent/loop_test.go`  
**依赖：** T21、T22

**步骤：**

1. Preflight/PostToolResults 发生工具结果裁剪或压缩时发送 snapshot。
2. 紧急压缩成功后发送 snapshot。
3. `/compact` 手动压缩成功后发送 snapshot。
4. 失败或没有改变消息历史时不发送 snapshot。
5. 测试 snapshot 位于后续正式请求或 Done 之前，并包含当前完整有效历史。

**验证：** 在 `src` 目录运行 `go test ./internal/agent -run 'Test.*ConversationSnapshot'`。

## T24：为 TUI 接入 Memory 组件和生命周期状态

**文件：** `src/internal/tui/model.go`、`src/internal/tui/model_test.go`  
**依赖：** T1、T2、T9、T14、T16

**步骤：**

1. 为 TUI 增加 InstructionLoader、InstructionSet、SessionStore、当前 Journal、NoteStore 和 Worker 引用。
2. 增加当前 session ID、pending resume gap 和本轮消息副本状态。
3. 使用一个无行为的依赖参数结构传入组件，避免继续增加 `tui.New` 的位置参数。
4. 增加关闭当前 Journal 的明确入口，支持 main 在程序退出后清理。
5. 测试空依赖下原有 TUI 仍能运行，完整依赖下状态正确初始化。

**验证：** 在 `src` 目录运行 `go test ./internal/tui -run 'Test.*MemoryDependencies'`。

## T25：持久化用户消息和 Agent Conversation 事件

**文件：** `src/internal/tui/model.go`、`src/internal/tui/model_test.go`  
**依赖：** T6、T21、T24

**步骤：**

1. 第一次真实用户请求时创建 Journal，并在 AddUser 后立即追加用户消息。
2. 处理 EventConversation append/snapshot 并按事件顺序写入 Journal。
3. append 事件同时加入本轮 TurnCandidate 副本，snapshot 不覆盖候选原始回合。
4. 写入失败时展示 warning、关闭当前 Journal 并继续内存对话。
5. 测试普通响应、工具响应、snapshot 和磁盘写入失败。

**验证：** 在 `src` 目录运行 `go test ./internal/tui -run 'Test.*SessionPersistence'`。

## T26：在请求前组装指令、记忆和时间跨度上下文

**文件：** `src/internal/tui/model.go`、`src/internal/tui/model_test.go`  
**依赖：** T1、T4、T14、T19、T24

**步骤：**

1. 新会话启动和恢复成功时重新加载三层项目指令。
2. 每次启动 Agent 前读取受预算限制的最新记忆索引。
3. Memory 关闭时不读取或注入索引，但项目指令仍然工作。
4. 把 pending resume gap 加入本次 RunOptions，并在真正启动后清除。
5. 指令或索引读取失败只展示 warning，不阻止 Agent 运行。
6. 测试 reminder 内容、关闭开关和一次性 gap 消耗。

**验证：** 在 `src` 目录运行 `go test ./internal/tui -run 'Test.*PromptContext'`。

## T27：自然停止后提交自动记忆任务

**文件：** `src/internal/tui/model.go`、`src/internal/tui/model_test.go`  
**依赖：** T18、T22、T25

**步骤：**

1. 在本轮开始时记录用户消息并清空旧候选。
2. 从 Conversation append 事件收集完整助手和工具消息。
3. 仅在 EventDone 原因为 StopModelDone 时异步 Enqueue。
4. 取消、流错误、迭代上限和坏工具上限不提交候选。
5. Enqueue 失败或 Worker 报错时只展示 warning，不延迟 done 渲染。
6. 使用 fake Worker/provider 验证触发矩阵和非阻塞行为。

**验证：** 在 `src` 目录运行 `go test ./internal/tui -run 'Test.*AutoMemory'`。

## T28：实现 `/resume` 会话选择器

**文件：** `src/internal/tui/session_picker.go`、`src/internal/tui/session_picker_test.go`、`src/internal/tui/model.go`  
**依赖：** T9、T24

**步骤：**

1. 增加独立的 session picker 状态和会话列表数据。
2. 展示标题、更新时间、消息数和 ID，并保持长文本不破坏布局。
3. 上下键移动选中项，选中项使用明显高亮和标记。
4. Enter 返回目标 ID，Esc 退出并保留当前会话。
5. 测试空列表、上下边界、确认、取消和长标题渲染。

**验证：** 在 `src` 目录运行 `go test ./internal/tui -run 'TestSessionPicker'`。

## T29：实现 `/continue` 和 `/resume` 恢复流程

**文件：** `src/internal/tui/model.go`、`src/internal/tui/model_test.go`  
**依赖：** T8、T9、T20、T28

**步骤：**

1. `/continue` 异步加载最近有效且未过期的 session。
2. `/resume` 异步扫描列表，并在选择后加载对应 session。
3. 加载和重新打开 Journal 都成功后再原子替换当前 Conversation。
4. 把坏行、截断等 warning 连同行号显示给用户。
5. 格式化并展示恢复后的有效历史，不重新执行旧工具。
6. 根据 LastActiveAt 设置超过 24 小时的一次性 gap reminder。
7. 测试成功恢复、无会话、加载失败、Journal 打开失败和当前会话保留。

**验证：** 在 `src` 目录运行 `go test ./internal/tui -run 'Test.*(Continue|Resume|Restore)'`。

## T30：验证恢复后复用现有 Preflight 压缩

**文件：** `src/internal/tui/model_test.go`、`src/internal/agent/loop_test.go`  
**依赖：** T23、T29

**步骤：**

1. 构造超过当前模型窗口的恢复消息并启动下一次 Agent 请求。
2. 验证正式 provider 请求前先进入现有 Preflight。
3. 压缩成功时发送 snapshot 并由 TUI 追加到原 JSONL。
4. 压缩失败时停止该轮请求，不无限重试、不改写旧记录。
5. 验证 memory 包没有导入 conversation 或复制 Token 估算逻辑。

**验证：** 在 `src` 目录运行 `go test ./internal/agent ./internal/tui -run 'Test.*Restored.*Compact'`。

## T31：组装启动、清理和退出流程

**文件：** `src/cmd/onecode/main.go`、`.gitignore`  
**依赖：** T1、T4、T10、T18、T24

**步骤：**

1. 根据 cwd 和用户目录创建 Loader、SessionStore、NoteStore 和 Worker。
2. 启动时加载项目指令并输出非致命 warning。
3. 后台执行 30 天清理，清理失败不阻止 TUI 启动。
4. 把具体组件作为依赖传给 TUI。
5. Bubble Tea 退出后关闭最终 Journal 和 Worker。
6. 忽略 `.onecode/sessions/`、`.onecode/memory/`，确认 `.onecode/ONECODE.md` 未被忽略。

**验证：** 在 `src` 目录运行 `go build ./cmd/onecode`，并在仓库根运行 `git check-ignore -v .onecode/sessions/test.jsonl .onecode/memory/INDEX.md`。

## T32：执行格式化和分包回归测试

**文件：** 本阶段全部 Go 文件  
**依赖：** T1-T31

**步骤：**

1. 对修改过的 Go 文件运行 `gofmt`。
2. 分别运行 config、memory、prompt、conversation、agent、tui 测试。
3. 修复新代码引入的编译、竞态或既有行为回归。
4. 确认没有新增多余 package、manager、repository 或重复消息模型。

**验证：** 在 `src` 目录运行 `go test ./internal/config ./internal/memory ./internal/prompt ./internal/conversation ./internal/agent ./internal/tui`。

## T33：执行全量构建和测试

**文件：** 全项目  
**依赖：** T32

**步骤：**

1. 运行全量单元和集成测试。
2. 构建 OneCode 命令行程序。
3. 运行 `go vet` 检查常见 Go 错误。
4. 运行 `git diff --check` 检查空白和补丁质量。
5. 记录任何因环境限制无法执行的验证项。

**验证：** 在 `src` 目录运行 `go test ./...`、`go build ./cmd/onecode`、`go vet ./...`，并在仓库根运行 `git diff --check`。

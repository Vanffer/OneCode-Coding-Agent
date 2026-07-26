# 持久化记忆与会话恢复 Checklist

> 每一项必须通过测试、文件检查或真实 TUI 行为获得证据后才能勾选。工作目录未特别说明时为 `src`。

## 架构与实现完整性

- [ ] 只新增 `internal/memory` 一个业务包，项目指令、会话和自动笔记分别由聚焦的具体类型负责，没有新增 `MemoryManager`、通用 Repository 或第二套消息模型。（验证：检查 `src/internal/memory` 文件和 import 关系）
- [ ] `memory` 不导入 `tui`、`agent`、`prompt` 或 `conversation`，依赖图没有循环。（验证：运行 `go list -deps ./internal/memory` 并检查源码 import）
- [ ] `conversation` 和 `prompt` 不反向导入 `memory`；Agent 通过事件而不是 SessionJournal 具体类型报告消息变化。（验证：运行 `rg 'internal/memory' internal/conversation internal/prompt internal/agent`）
- [ ] Memory 配置按“项目明确值 > 用户明确值 > 默认 true”合并，运行阶段得到明确布尔值。（验证：运行 `go test ./internal/config -run Test.*Memory`）
- [ ] 自动生成的 session 和 memory 目录被 Git 忽略，`.onecode/ONECODE.md` 仍可提交。（验证：在仓库根运行 `git check-ignore -v .onecode/sessions/test.jsonl .onecode/memory/INDEX.md`，并确认 `git check-ignore .onecode/ONECODE.md` 返回未忽略）

## 项目指令

- [ ] **[AC1]** 三份入口同时存在时，加载顺序为项目 `.onecode/ONECODE.md`、项目根 `ONECODE.md`、用户 `~/.onecode/ONECODE.md`；任一文件缺失时其余文件仍正常加载。（验证：运行 `go test ./internal/memory -run TestInstructionLoader`）
- [ ] **[AC2]** 合法 `@include` 能在原位置展开至第 5 层；同一真实文件重复引用或形成环路时不会重复或无限展开。（验证：运行 include 递归、重复和 cycle 单测）
- [ ] **[AC3]** `..`、项目外绝对路径、跨卷路径和符号链接逃逸会被拒绝，其他合法内容继续加载。（验证：运行 `go test ./internal/memory -run 'TestInstruction.*(Escape|Symlink)'`）
- [ ] **[AC4]** include 文件不存在、不可读、语法错误或超过深度时产生包含来源文件和原因的 warning，启动不失败。（验证：运行 warning 单测并观察启动输出）
- [ ] **[AC5]** 第一次模型请求已经包含加载后的项目指令，不需要模型先调用 read_file；创建或恢复会话时会重新加载，运行中不热更新。（验证：fake provider 捕获 Prompt.Reminders，并手动修改 ONECODE.md 后新建/恢复会话）
- [ ] 项目指令使用来源边界包裹，高优先级内容位于低优先级内容之前。（验证：断言 InstructionSet.Content 的标签和下标顺序）
- [ ] 项目指令只作为 request-local reminder，不写入 Conversation、session JSONL 或稳定 System Prompt。（验证：检查 fake provider 请求、Conversation.Messages 和 JSONL）

## 会话写入

- [ ] **[AC6]** 普通启动进入新的空 Conversation，不自动恢复旧历史；本次逻辑会话 ID 符合 `YYYYMMDD-HHMMSS-xxxx`，首次用户消息前不产生空 JSONL。（验证：启动两次并观察状态，运行 Session ID 单测）
- [ ] **[AC7]** 完成普通对话后生成一个 session JSONL，用户和完整助手消息各自是可独立解析的一行。（验证：真实对话后逐行运行 JSON 解析或对应单测）
- [ ] **[AC8]** 工具任务依次保存 assistant tool calls、每条 tool result 和最终 assistant 回复，ID 关联完整。（验证：运行包含多工具调用的 Agent/TUI 持久化测试并检查 JSONL）
- [ ] **[AC9]** 流式响应中途退出时不会保存半条 assistant 文本，之前完成的用户、助手和工具消息仍能恢复。（验证：mock provider 流式阻塞后取消或终止，重新加载 JSONL）
- [ ] 流式文本增量不会逐 token 写盘，同一完整 assistant 消息只出现一条 message 记录。（验证：让 fake provider 返回多个 text delta，检查 JSONL 行数）
- [ ] 上下文压缩或工具结果裁剪后追加 snapshot；恢复后得到的是模型实际可见的有效历史，而不是被替换前的完整旧历史。（验证：运行 snapshot replay 测试）
- [ ] 同一 Journal 的并发追加不会把两个 JSON 对象写到同一行；关闭后写入返回明确错误。（验证：运行 `go test ./internal/memory -run TestSessionJournal`）

## 会话列表与恢复

- [ ] **[AC10]** 不存在独立 meta 文件时，`/resume` 仍能展示从 JSONL 得到的标题、更新时间、消息数和 ID；中文标题按字符安全截断。（验证：运行 SessionSummary 和 SessionPicker 测试）
- [ ] **[AC11]** 多个有效会话中，`/continue` 恢复最后活动时间最新且未过期的会话；无有效会话时提示明确并保留当前空会话。（验证：运行 Latest 和 TUI Continue 测试）
- [ ] **[AC12]** `/resume` 显示历史列表，上下键改变高亮项，Enter 恢复，Esc 取消；恢复后新消息继续写入原 session 文件。（验证：运行 session picker 测试并手动操作 TUI）
- [ ] **[AC13]** JSONL 中间存在坏行时，该行被跳过，warning 包含 session 和行号，其他合法记录继续恢复。（验证：运行 BadLine 测试并手动恢复损坏样例）
- [ ] **[AC14]** assistant tool calls 缺少结果时，从该 assistant 消息之前截断，恢复历史不包含悬空调用。（验证：运行缺失结果测试）
- [ ] **[AC15]** 孤立结果、重复结果或 ToolUseID 不匹配时执行相同的最长合法前缀截断，原 JSONL 不被改写。（验证：恢复前后比较文件哈希并运行工具协议测试）
- [ ] **[AC16]** 恢复历史超限后，下一次 Agent 请求先走 Phase 7 Preflight；压缩成功才发正式请求并追加 snapshot，失败时停止且不无限重试。（验证：运行 restored compact 的 Agent/TUI 集成测试）
- [ ] **[AC17]** 会话间隔超过 24 小时时，恢复后的第一次真实模型请求包含时间跨度提醒；第二次不再包含，slash command 不会提前消耗，JSONL 和用户历史中均不存在该提醒。（验证：使用可控时钟和 fake provider 连续检查两次请求）
- [ ] **[AC18]** 30 天前的合法非活动会话被清理，当前活跃、未过期和非 session 文件保留；单文件删除失败不阻止其他清理。（验证：运行 `go test ./internal/memory -run TestSessionCleanup`）
- [ ] **[AC19]** 项目生成 `.onecode/sessions/*.jsonl` 后，`git status --short` 不显示这些文件。（验证：真实生成会话后运行 Git 状态）
- [ ] 恢复切换是原子的：加载失败或原 Journal 无法打开时，当前 Conversation 和 Journal 保持不变。（验证：运行 TUI Restore failure 测试）
- [ ] 恢复只重建消息，不重新执行旧工具，不自动批准任何新工具权限。（验证：恢复带工具历史，断言 Registry/PermissionManager 未收到旧调用）

## 自动记忆触发与作用域

- [ ] **[AC20]** 默认开启时，有长期价值的自然结束回合会异步提交笔记任务；最终回复和输入框恢复不等待后台 LLM。（验证：fake provider 阻塞记忆请求，断言 TUI 已进入 idle）
- [ ] **[AC21]** 配置关闭后不读取、不注入、不生成自动记忆，已有文件不删除；项目指令和 session 仍可使用。（验证：关闭配置运行请求并比较 memory 目录前后内容）
- [ ] **[AC22]** 项目知识只写入项目 `.onecode/memory`，明确的跨项目长期用户偏好才写入 `~/.onecode/memory`；临时要求不会升级为全局偏好。（验证：fake extraction 返回不同 scope，运行 scope 校验测试）
- [ ] **[AC23]** preference、correction、project_knowledge、reference 四种分类均可创建和解析，未知分类被拒绝。（验证：运行 Note frontmatter 和 mutation validation 表驱动测试）
- [ ] **[AC24]** 只有 `StopModelDone` 提交 TurnCandidate；取消、流错误、迭代上限和坏工具上限均不触发。（验证：运行 TUI AutoMemory 触发矩阵测试）
- [ ] 本地过滤会跳过空回合、明显仅问候/确认的极短回合和完全重复回合，避免额外 LLM 调用。（验证：fake provider 记录调用次数，运行 Worker Filter 测试）
- [ ] Worker 队列有固定边界且串行消费；队列满或关闭后 Enqueue 不阻塞 TUI。（验证：运行 Worker Queue/Serial/Close 测试）

## 自动笔记与索引

- [ ] **[AC25]** 相同信息不会重复创建文件；更具体的补充或纠正通过 update 修改已有笔记并保留 CreatedAt。（验证：连续应用 create/update mutation，检查 notes 文件数量和 frontmatter）
- [ ] **[AC26]** 快速提交多个相同候选时，后台任务按提交顺序串行处理，不会因并发读取旧索引创建重复笔记。（验证：带同步点的 fake provider/NoteStore 并发测试）
- [ ] **[AC27]** 每条笔记是可读 Markdown，frontmatter 至少含 ID、分类、作用域、标题、创建时间和更新时间。（验证：打开实际笔记并用 yaml.v3 重新解析）
- [ ] **[AC28]** 用户级和项目级各自维护 INDEX.md；条目含摘要和相对路径，不复制完整正文。（验证：创建长正文笔记后检查索引）
- [ ] **[AC29]** 每份索引和单次组合注入均不超过 200 行、25KB；用户偏好获得预留空间，空余预算可让给项目索引。（验证：运行 NoteIndexBudget 和 CombinedIndex 测试）
- [ ] **[AC30]** 新会话第一次正常请求已经包含主要用户偏好和项目知识索引，无需先调用工具检索。（验证：fake provider 捕获第一轮 reminders）
- [ ] **[AC31]** PEM 私钥、API key、Bearer token、JWT、密码和 secret/token 赋值不会进入笔记、索引或错误文本。（验证：运行 SensitiveContent 测试并搜索测试目录输出）
- [ ] **[AC32]** 未知 operation、非法字段、非法 scope/category 和不存在的 update 目标均被拒绝，不能控制本地文件路径。（验证：运行 Worker Mutation 测试）
- [ ] **[AC33]** 笔记临时写入或 rename 失败时，INDEX.md 不新增不存在的目标，用户回复和后续 Agent 请求不受影响。（验证：注入文件系统失败并检查旧索引）
- [ ] **[AC34]** INDEX.md 更新失败时旧索引保持可解析，其他笔记文件不损坏，并存在不含敏感原文的可观察错误。（验证：运行 NoteIndexAtomic failure 测试）
- [ ] Worker 接受纯 JSON 和外层 JSON code fence；无效 JSON、工具调用、流错误或超时均不写入部分结果。（验证：运行 WorkerExtract 测试）
- [ ] 笔记和索引通过同目录临时文件原子替换，进程中断不会主动删除旧有效文件。（验证：审查写入顺序并运行 atomic tests）

## Prompt 与 Provider 集成

- [ ] **[AC35]** OpenAI 和 Anthropic 均能接收 instructions、memory、resume gap reminders，并继续完成普通文本和工具任务，不新增专有消息角色。（验证：运行 `go test ./internal/llm ./internal/prompt`，再分别做一次真实 provider smoke test）
- [ ] 动态 reminder 顺序为环境、项目指令、记忆索引、时间跨度、Plan Mode；空内容不产生空 reminder。（验证：运行 prompt reminder 顺序测试）
- [ ] StableSystem 在加入项目指令和记忆后保持字节不变，动态内容只进入 request-local message 通道。（验证：运行 `go test ./internal/prompt -run Test.*Stable`）
- [ ] 自动记忆提取复用当前 `llm.Provider`、不提供工具定义、不新增 max token 用户配置。（验证：fake provider 检查 tools=nil，审查配置结构）
- [ ] Memory index 的组合预算按 UTF-8 安全边界裁剪，不把中文字符截成非法字节。（验证：使用中文超长索引运行预算测试）

## 既有能力回归

- [ ] **[AC36]** 恢复后 Token 估算、工具结果裁剪、自动/手动/紧急压缩和 Context 用量展示继续工作。（验证：运行 `go test ./internal/conversation ./internal/agent ./internal/tui`）
- [ ] **[AC37]** 恢复历史不改变权限模式；恢复后的新 Bash、写文件或 MCP 工具仍经过统一 PermissionManager。（验证：恢复后触发 ask/deny 测试）
- [ ] **[AC38]** 指令加载、会话清理、自动记忆三者任一失败时，另外两项仍能运行。（验证：分别注入三类失败并运行故障隔离集成测试）
- [ ] Phase 2 内置工具行为不变。（验证：运行 `go test ./internal/tools/...`）
- [ ] Phase 3 Agent loop、Plan Mode、取消和停止条件行为不变。（验证：运行 `go test ./internal/agent -count=1`）
- [ ] Phase 5 权限确认和 MCP 工具权限分类行为不变。（验证：运行 `go test ./internal/permission ./internal/mcp`）
- [ ] Phase 7 当前会话压缩不依赖 memory 包，普通不恢复的会话仍按原路径工作。（验证：运行 conversation/agent 原有上下文测试）

## 性能、安全与边界

- [ ] 普通新会话启动不扫描所有 session 正文；只有 `/continue`、`/resume` 和清理任务触发历史扫描。（验证：注入计数文件系统或审查调用路径）
- [ ] `/resume` 扫描大量文件时逐个计算摘要，不把所有会话的完整正文同时保存在内存中。（验证：生成大量测试会话并观察扫描实现/内存）
- [ ] Session ID、Note ID 和 include 路径均经过本地校验，LLM 或用户输入不能构造任意写入路径。（验证：路径遍历表驱动测试）
- [ ] 自动笔记调用只有正常配置的 LLM 请求会出网，没有新增其他上传、同步或遥测。（验证：审查 Worker 和依赖）
- [ ] 本阶段没有向量数据库、Embedding、RAG、团队同步、自动删除、会话分叉、AI 标题或 `/memory` 管理 UI。（验证：检查依赖、命令和新增文件）
- [ ] `ONECODE.md` 与记忆不能覆盖 PermissionManager、工具安全分类和 Plan Mode 限制。（验证：在指令中要求绕过权限，确认实际规则仍拦截）
- [ ] 所有 warning 包含可定位对象和阶段，但不打印已检测到的凭据原文。（验证：错误路径测试和敏感信息测试）

## 编译与自动化测试

- [ ] Config 测试通过。（验证：`go test ./internal/config`）
- [ ] Memory 测试通过且重复运行无偶发失败。（验证：`go test ./internal/memory -count=3`）
- [ ] Prompt 和 Conversation 测试通过。（验证：`go test ./internal/prompt ./internal/conversation`）
- [ ] Agent 和 TUI 测试通过且无事件顺序偶发失败。（验证：`go test ./internal/agent ./internal/tui -count=3`）
- [ ] Permission、MCP、Tools 和 LLM 回归测试通过。（验证：`go test ./internal/permission ./internal/mcp ./internal/tools/... ./internal/llm`）
- [ ] 全项目测试通过。（验证：`go test ./...`）
- [ ] OneCode 命令构建通过。（验证：`go build ./cmd/onecode`）
- [ ] `go vet` 无新增问题。（验证：`go vet ./...`）
- [ ] 修改过的 Go 文件均已 gofmt，补丁无空白错误。（验证：运行 gofmt 后执行 `git diff --check`）
- [ ] 条件允许时并发测试无数据竞争。（验证：`go test -race ./internal/memory ./internal/agent ./internal/tui`；环境不支持 race 时记录原因）

## 端到端场景

- [ ] **场景 1：项目指令自动生效。** 在三层入口写入可区分规则，其中项目入口 include 一个子文件；启动后直接询问相关项目约定，模型无需读取文件即可回答，并遵循高优先级项目规则。
- [ ] **场景 2：普通会话持久化。** 启动 OneCode，完成一次普通回复和一次工具任务，退出后检查 JSONL 每行可解析且没有流式碎片。
- [ ] **场景 3：`/continue` 跨进程续接。** 重启后确认默认是空的新会话，执行 `/continue` 恢复上一会话，再追问依赖旧上下文的问题，观察回复正确并继续写入原 JSONL。
- [ ] **场景 4：`/resume` 选择历史。** 准备多个会话，进入列表后用方向键切换高亮，Esc 能取消；再次进入并选择较早会话，观察标题、消息数、历史展示和后续续写均正确。
- [ ] **场景 5：损坏会话恢复。** 在测试 session 中插入坏 JSON 行并留下悬空工具调用；恢复时看到行号 warning，历史截断在合法位置，程序仍可继续对话。
- [ ] **场景 6：恢复历史压缩。** 恢复一个超过当前窗口的会话并发送新问题，观察正式请求前出现一次压缩，成功后继续；模拟压缩失败时不循环重试。
- [ ] **场景 7：时间跨度提醒。** 把测试会话最后活动时间设置为 24 小时以前，恢复后连续发送两次请求；抓取请求确认 reminder 只出现在第一次，TUI 和 JSONL 中不可见。
- [ ] **场景 8：项目自动记忆。** 明确纠正一个项目命令或架构事实，让 Agent 自然结束；回复立即完成，后台生成项目笔记和索引，新会话第一轮能看到摘要。
- [ ] **场景 9：用户长期偏好。** 明确声明“以后所有项目都……”的稳定偏好，确认写入用户级记忆；只说“这一次……”时不产生用户级笔记。
- [ ] **场景 10：关闭自动记忆。** 设置 `memory.enabled: false` 后对话，确认 session 仍写入、项目指令仍生效、记忆文件不变且请求中没有 memory index。
- [ ] **场景 11：敏感信息保护。** 在测试回合中提供明显的假 API key、JWT 和 PEM 私钥，确认自然结束后这些值没有出现在 notes、INDEX 或 warning。
- [ ] **场景 12：协议一致性。** 使用 OpenAI 和 Anthropic 配置分别完成“恢复会话后读取文件并总结”，确认 reminders、工具调用、权限和最终回复链路均可用。
- [ ] **场景 13：故障隔离。** 分别制造 include 失败、session 目录不可写和记忆提取错误，确认当前 Agent 任务仍能完成，其他两种持久能力仍可工作。

## 验收覆盖

| Spec AC | Checklist 位置 |
|---|---|
| AC1-AC5 | 项目指令 |
| AC6-AC9 | 会话写入 |
| AC10-AC19 | 会话列表与恢复 |
| AC20-AC24 | 自动记忆触发与作用域 |
| AC25-AC34 | 自动笔记与索引 |
| AC35 | Prompt 与 Provider 集成 |
| AC36-AC38 | 既有能力回归 |

# Agent Loop Tasks

## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 修改 | `src/internal/llm/provider.go` | 扩展 `StreamEvent`、新增 `Usage` 与 `FinishReason` |
| 修改 | `src/internal/llm/openai.go` | 支持多个 tool call 增量收集、finish reason、usage 映射 |
| 修改 | `src/internal/llm/anthropic.go` | 支持多个 tool_use block、finish reason、usage 映射 |
| 修改 | `src/internal/conversation/conversation.go` | 扩展 assistant tool_calls 写入，支持 content + 多 tool calls |
| 修改 | `src/internal/conversation/conversation_test.go` | 覆盖 content + tool calls 写入行为 |
| 修改 | `src/internal/tools/tool.go` | 新增工具安全分类类型 |
| 修改 | `src/internal/tools/registry.go` | 保存 `ToolInfo`、按安全分类导出工具定义、查询安全分类 |
| 新建 | `src/internal/tools/registry_test.go` | 覆盖安全分类、兼容注册、按模式导出 |
| 修改 | `src/internal/tools/tool_test.go` | 根据 Registry API 变化修正现有工具测试 |
| 修改 | `src/internal/agent/agent.go` | 保留 `Agent`、`New`、`Run` 入口与默认选项 |
| 新建 | `src/internal/agent/events.go` | 定义 Agent 事件、进度、完成、停止原因、运行模式 |
| 新建 | `src/internal/agent/format.go` | 参数摘要、结果摘要、停止原因文本 |
| 新建 | `src/internal/agent/collector.go` | 实现流式双路收集器 |
| 新建 | `src/internal/agent/scheduler.go` | 实现工具调用分批、只读并发、副作用串行、坏工具统计 |
| 新建 | `src/internal/agent/loop.go` | 实现 ReAct loop 主状态机和停止条件 |
| 新建 | `src/internal/agent/collector_test.go` | 覆盖文本转发、多个 tool call、usage、流错误 |
| 新建 | `src/internal/agent/scheduler_test.go` | 覆盖只读并发、副作用串行、混合批次顺序、坏工具 |
| 新建 | `src/internal/agent/loop_test.go` | 覆盖多轮循环、迭代上限、取消、禁用工具停止 |
| 修改 | `src/internal/tui/model.go` | 解析 `/plan` `/do`，保存 pending plan，支持 ESC cancel，消费新事件 |
| 修改 | `src/internal/tui/styles.go` | 如有需要，补充取消、进度或错误展示样式 |
| 修改 | `src/internal/prompt/system.txt` | 更新 ReAct loop、Plan Mode、工具错误处理规则 |
| 修改 | `src/cmd/onecode/main.go` | 注册工具时显式或隐式应用安全分类 |

## T1: 扩展 LLM 流式事件模型

**文件：** `src/internal/llm/provider.go`

**依赖：** 无

**步骤：**
1. 新增 `Usage` 结构，包含 `InputTokens`、`OutputTokens`、`TotalTokens`、`Available`。
2. 新增 `FinishReason` 枚举，包含 `FinishUnknown`、`FinishStop`、`FinishToolCalls`、`FinishLength`、`FinishError`。
3. 扩展 `StreamEvent`，增加 `Usage *Usage` 和 `FinishReason FinishReason` 字段。
4. 保持 `Provider.Stream(ctx, msgs, tools)` 方法签名不变。
5. 更新注释，说明 `Usage.Available=false` 代表 provider 未提供可靠用量。

**验证：** 在 `src` 下运行 `go test ./internal/llm`，期望编译通过。

## T2: 扩展 Conversation 写入能力

**文件：** `src/internal/conversation/conversation.go`、`src/internal/conversation/conversation_test.go`

**依赖：** T1

**步骤：**
1. 将 `AddAssistantWithToolCalls` 改为接收 `content string` 和 `toolCalls []llm.ToolCall`。
2. 写入 `llm.Message{Role: "assistant", Content: content, ToolCalls: toolCalls}`。
3. 更新所有现有调用点的签名。
4. 在测试中覆盖：只带 tool calls、同时带 content + tool calls、普通 assistant 文本仍正常。

**验证：** 在 `src` 下运行 `go test ./internal/conversation`，期望全部通过。

## T3: 增加工具安全分类

**文件：** `src/internal/tools/tool.go`、`src/internal/tools/registry.go`、`src/internal/tools/registry_test.go`、`src/internal/tools/tool_test.go`

**依赖：** T1

**步骤：**
1. 在 `tool.go` 新增 `Safety` 枚举：`SafetyReadOnly`、`SafetySideEffect`。
2. 在 `registry.go` 新增 `ToolInfo`，保存 `Tool` 和 `Safety`。
3. 将 Registry 内部 map 从 `map[string]Tool` 调整为 `map[string]ToolInfo`。
4. 保留 `Register(t Tool)`，内部按工具名推断默认安全分类。
5. 新增 `RegisterWithSafety(t Tool, safety Safety)`，供测试或未来扩展使用。
6. 新增 `Safety(name string) (Safety, bool)`。
7. 新增 `ToToolDefinitionsBySafety(allowed map[Safety]bool)`。
8. 保持 `List`、`Get`、`Execute` 的用户可见语义不变。
9. 测试默认分类：`read_file/glob/grep` 为只读，`write_file/edit_file/bash` 为有副作用。
10. 测试 Plan Mode 允许集合只导出只读工具。

**验证：** 在 `src` 下运行 `go test ./internal/tools`，期望全部通过。

## T4: 拆分 Agent 事件与运行选项

**文件：** `src/internal/agent/agent.go`、`src/internal/agent/events.go`、`src/internal/agent/format.go`

**依赖：** T1、T3

**步骤：**
1. 在 `events.go` 定义 `Mode`、`RunOptions`、`EventType`、`Event`、`ToolEvent`、`UsageEvent`、`ProgressEvent`、`ProgressStatus`、`DoneEvent`、`StopReason`。
2. 在 `agent.go` 保留 `Agent`、`New` 和新的 `Run(ctx, conv, opts)` 入口。
3. 在 `agent.go` 增加默认选项归一逻辑：默认 `ModeExecute`、默认迭代上限约 20、默认连续坏工具上限约 3。
4. 将原有 `formatArgsPreview`、`truncateResult` 迁移到 `format.go`。
5. 在 `format.go` 增加 `stopReasonMessage(reason StopReason) string`，供 UI 或最终提示使用。
6. 暂时保留旧 loop 逻辑不可用时的编译路径，后续 T7 替换为新 loop。

**验证：** 在 `src` 下运行 `go test ./internal/agent`，期望编译通过。

## T5: 实现流式双路收集器

**文件：** `src/internal/agent/collector.go`、`src/internal/agent/collector_test.go`

**依赖：** T4

**步骤：**
1. 新增 `ModelResponse`，包含完整 `Text`、`ToolCalls`、`Usage`。
2. 实现 `collectModelResponse(ctx, stream, errs, events, iteration)`。
3. 收到 `llm.StreamEvent.Text` 时，同时写入 `ModelResponse.Text` 并发送 `EventText`。
4. 收到 `ToolCall` 时 append 到 `ModelResponse.ToolCalls`，不立刻执行工具。
5. 收到 `Usage` 时合并用量并发送 `EventUsage`。
6. 收到 `Done` 时返回完整 `ModelResponse`。
7. 收到 `errs` 时返回错误；如果 ctx 已取消，返回取消错误。
8. 测试文本分片会实时转发且完整拼接。
9. 测试同一轮多个 `ToolCall` 全部收集。
10. 测试 usage 可用和不可用的行为。
11. 测试流错误不会返回成功响应。

**验证：** 在 `src` 下运行 `go test ./internal/agent -run TestCollect`，期望全部通过。

## T6: 实现工具调度器

**文件：** `src/internal/agent/scheduler.go`、`src/internal/agent/scheduler_test.go`

**依赖：** T3、T4

**步骤：**
1. 实现 `executeToolCalls(ctx, calls, mode, events)`。
2. 对每个 tool call 查询 Registry：不存在则生成 `llm.ToolResult{IsError: true}` 并计入 bad tool。
3. 在 `ModePlan` 下遇到 `SafetySideEffect` 工具时，生成禁用工具错误结果并计入 bad tool。
4. 允许执行的只读工具进入只读批次。
5. 允许执行的有副作用工具进入串行批次。
6. 按模型返回顺序切分批次：连续只读并发，副作用单个或连续串行，后续只读不提前越过副作用。
7. 工具开始前发送 `EventToolStart`，结束后发送 `EventToolResult`。
8. 工具结果顺序按原始 tool call 顺序回写，保证 provider 协议关联稳定。
9. 测试多个只读 fake tools 的执行时间重叠，证明并发。
10. 测试有副作用 fake tools 按顺序串行。
11. 测试 `[read, read, write, read]` 的批次顺序。
12. 测试未知工具和 Plan Mode 禁用工具返回结构化错误。

**验证：** 在 `src` 下运行 `go test ./internal/agent -run TestScheduler`，期望全部通过。

## T7: 实现 ReAct Loop 主状态机

**文件：** `src/internal/agent/loop.go`、`src/internal/agent/agent.go`、`src/internal/agent/loop_test.go`

**依赖：** T2、T5、T6

**步骤：**
1. 在 `Run` 中启动 goroutine，创建事件 channel，并调用 `runLoop`。
2. `runLoop` 按 iteration 循环请求 provider。
3. 每轮开始发送 `ProgressRequestingModel`。
4. 根据 `RunOptions.Mode` 选择工具定义：Execute 暴露全部，Plan 只暴露只读。
5. 调用 provider stream 后交给 collector 收集完整响应。
6. 如果没有工具调用：写入 `conv.AddAssistant(response.Text)`，发送 `EventDone{StopModelDone}`。
7. 如果有工具调用：写入 `conv.AddAssistantWithToolCalls(response.Text, response.ToolCalls)`。
8. 调用 scheduler 执行全部工具调用。
9. 将所有 `llm.ToolResult` 写入 conversation。
10. 更新连续 bad tool 计数；达到上限时停止并发送 `StopBadToolLimit`。
11. 达到迭代上限时停止并发送 `StopMaxIterations`。
12. ctx 取消时发送取消事件和 `StopCancelled`，不再进入下一轮。
13. collector 返回流错误时发送 `EventError` 和 `StopStreamError`。
14. 用 mock provider 覆盖：两轮工具后最终文本、多工具同轮、迭代上限、连续坏工具、ctx 取消。

**验证：** 在 `src` 下运行 `go test ./internal/agent -run TestLoop`，期望全部通过。

## T8: 迁移旧 Agent 调用点

**文件：** `src/internal/tui/model.go`、`src/internal/agent/agent.go`

**依赖：** T7

**步骤：**
1. 将 TUI 中 `m.agent.Run(ctx, m.conv)` 改为传入 `agent.RunOptions{Mode: agent.ModeExecute}`。
2. 处理新的 `agent.Event.Type` 分发方式。
3. 移除对旧 `PhaseStart/PhaseEnd` 的依赖，改用 `EventToolStart/EventToolResult`。
4. 确认普通聊天和普通工具任务能编译通过。

**验证：** 在 `src` 下运行 `go test ./internal/tui ./internal/agent`，期望编译通过。

## T9: 更新 OpenAI Provider 适配

**文件：** `src/internal/llm/openai.go`

**依赖：** T1、T2

**步骤：**
1. 保持请求构造逻辑兼容现有 conversation 消息。
2. 更新 assistant tool calls 转换逻辑，支持同一 assistant 消息同时带 content 和多个 tool calls。
3. 将 tool call 流式状态从单个变量改为按 OpenAI delta index 聚合。
4. 每个 index 分别累积 ID、Name、Arguments。
5. 当 finish reason 为 `tool_calls` 或 stream 结束时，按 index 顺序吐出全部完整 `ToolCall`。
6. 将 OpenAI finish reason 映射为 `llm.FinishReason`。
7. 如果 SDK 流事件提供 usage，则映射为 `llm.Usage{Available: true}`；否则保持不可用。
8. 确保 ctx 取消时退出 goroutine 并关闭 channel。

**验证：** 在 `src` 下运行 `go test ./internal/llm`，期望编译通过；再运行 `go test ./...`，期望无编译错误。

## T10: 更新 Anthropic Provider 适配

**文件：** `src/internal/llm/anthropic.go`

**依赖：** T1、T2

**步骤：**
1. 保持消息构造逻辑兼容现有 conversation 消息。
2. 确认 assistant content + 多个 tool calls 会转成同一 assistant message 的多个 content block。
3. 继续在每个 `ContentBlockStopEvent` 结束时吐出完整 `ToolCall`。
4. 确认同一轮多个 `tool_use` block 都会各自吐出事件。
5. 将 Anthropic stop reason 映射为 `llm.FinishReason`。
6. 如果 message delta 或最终事件提供 usage，则映射为 `llm.Usage{Available: true}`；否则保持不可用。
7. 保持 thinking 增量丢弃策略。
8. 确保 ctx 取消时退出 goroutine 并关闭 channel。

**验证：** 在 `src` 下运行 `go test ./internal/llm`，期望编译通过；再运行 `go test ./...`，期望无编译错误。

## T11: 更新系统提示

**文件：** `src/internal/prompt/system.txt`

**依赖：** T7

**步骤：**
1. 增加 ReAct loop 行为说明：可以连续使用工具，直到任务完成。
2. 增加工具结果处理说明：读取错误结果并调整策略。
3. 增加多个工具调用说明：可在同一轮请求多个独立只读工具。
4. 增加 Plan Mode 说明：计划阶段只读、输出清晰计划、不修改文件。
5. 增加 Execute 模式说明：基于计划和工具结果执行，完成后总结实际改动和验证。
6. 保持原有工具使用指南和简洁风格。

**验证：** 在 `src` 下运行 `go test ./internal/prompt`，期望编译通过。

## T12: 实现 TUI slash 命令解析与 pending plan

**文件：** `src/internal/tui/model.go`

**依赖：** T8、T11

**步骤：**
1. 新增 `PendingPlan` 结构和 `pendingPlan *PendingPlan` 字段。
2. 新增 `currentMode agent.Mode` 字段，用于记录当前运行模式。
3. 实现 `handleSlashCommand(input)`。
4. 保留 `/exit` 行为。
5. 实现 `/plan <目标>`：目标为空时给出可读提示；目标非空时注入计划请求并以 `ModePlan` 启动 Agent。
6. 实现 `/do`：没有 pending plan 时打印提示，不启动 Agent。
7. `/do` 有 pending plan 时注入执行计划请求，以 `ModeExecute` 启动 Agent，并标记计划已消费。
8. 普通输入继续走 Execute 模式。

**验证：** 在 `src` 下运行 `go test ./internal/tui`，期望编译通过；手动检查 `/do` 无计划路径不会调用 Agent。

## T13: 实现 TUI 取消与新事件渲染

**文件：** `src/internal/tui/model.go`、`src/internal/tui/styles.go`

**依赖：** T12

**步骤：**
1. 在 Model 中新增 `cancelCurrent context.CancelFunc`。
2. 启动 Agent 前使用 `context.WithCancel` 创建 ctx，并保存 cancel。
3. 在 `stateStreaming` 下处理 ESC：调用 cancel，状态栏进入 cancelling 文案。
4. 消费 `EventText`：继续写入 `curReply` 并实时显示。
5. 消费 `EventToolStart`：打印工具开始行。
6. 消费 `EventToolResult`：打印结果摘要，错误结果使用可区分样式或标记。
7. 消费 `EventProgress`：更新状态栏状态文本。
8. 消费 `EventUsage`：在状态栏或完成摘要中显示可用 token，用量不可用时不显示数字。
9. 消费 `EventCancelled` 和 `EventDone{StopCancelled}`：显示取消提示，恢复输入。
10. 消费 `EventError`：显示错误，恢复输入。
11. `/plan` 正常完成后，将本轮 assistant 文本保存为 pending plan。
12. `/do` 完成、取消或失败后清理 pending plan。

**验证：** 在 `src` 下运行 `go test ./internal/tui`，期望编译通过；手动启动 TUI 后按 ESC，观察能回到输入状态。

## T14: 应用工具安全注册

**文件：** `src/cmd/onecode/main.go`、`src/internal/tools/registry.go`

**依赖：** T3

**步骤：**
1. 决定入口是否继续使用 `Register` 默认推断，或改为 `RegisterWithSafety` 显式注册。
2. 如果采用显式注册，在 `main.go` 中为六个工具标注对应 safety。
3. 如果采用默认推断，在 `registry.go` 中确保未知工具默认分类保守为 `SafetySideEffect`。
4. 确认注册顺序仍稳定，导出工具定义顺序与 Phase 2 一致。

**验证：** 在 `src` 下运行 `go test ./internal/tools`，期望分类测试通过；运行 `go test ./cmd/onecode`，期望编译通过。

## T15: 兼容并修正全项目调用点

**文件：** 所有受签名变化影响的 Go 文件

**依赖：** T2、T7、T8、T9、T10、T13、T14

**步骤：**
1. 使用 `rg "AddAssistantWithToolCalls|agent.Run|StreamEvent|PhaseStart|PhaseEnd"` 找出旧 API 调用点。
2. 将旧调用点迁移到新签名或新事件类型。
3. 确认旧 `Phase` 类型不再被 TUI 依赖；若无用则移除。
4. 确认所有新增文件 package/import 正确。
5. 运行 gofmt 格式化所有修改过的 Go 文件。

**验证：** 在 `src` 下运行 `go test ./...`，期望全部编译并测试通过。

## T16: 增强 Agent Loop 自动化测试覆盖

**文件：** `src/internal/agent/loop_test.go`、`src/internal/agent/scheduler_test.go`、`src/internal/agent/collector_test.go`

**依赖：** T15

**步骤：**
1. 增加 mock provider：可按脚本返回多轮 stream events。
2. 增加 fake tools：可记录执行顺序、阻塞等待 ctx、模拟错误。
3. 覆盖“搜索 → 读取 → 编辑 → 测试 → 最终总结”的多轮脚本化场景。
4. 覆盖同一轮两个只读工具调用都被执行并回写。
5. 覆盖 Plan Mode 请求 `edit_file` 时返回禁用工具错误。
6. 覆盖连续坏工具达到上限时停止。
7. 覆盖 ctx cancel 后不再发起下一轮 provider 请求。

**验证：** 在 `src` 下运行 `go test ./internal/agent -count=1`，期望全部通过且无偶发超时。

## T17: 做一次端到端编译与基础 smoke 检查

**文件：** 全项目

**依赖：** T16

**步骤：**
1. 在 `src` 下运行全量测试。
2. 如测试需要本地 Go 缓存，在 PowerShell 中设置 `GOCACHE` 到仓库内临时目录。
3. 启动程序，确认能进入 TUI 或 provider 选择界面。
4. 在没有真实 API 的情况下，至少确认配置加载失败、provider 选择、普通编译路径不崩。
5. 如有可用 provider 配置，手动验证普通聊天或简单只读工具任务。

**验证：**

```powershell
cd src
$env:GOCACHE = Join-Path (Resolve-Path ..).Path ".gocache"
go test ./...
go run ./cmd/onecode
```

期望测试通过；程序能启动到预期界面或给出清晰配置错误。

## T18: 更新实现复盘入口

**文件：** `doc/spec/phase3-agent-loop/task.md`、后续 `implementation-notes.md`

**依赖：** T17

**步骤：**
1. 开发完成后，根据实际实现补充 `implementation-notes.md`。
2. 记录最终数据流、状态变化、关键取舍、测试证据。
3. 如果实现过程中偏离 plan，在 notes 中说明原因和最终方案。

**验证：** 文档存在且能从 spec/plan/task/checklist 串起 Phase 3 的完整脉络。

## 执行顺序

```text
T1
├─> T2
├─> T3 ──> T6
└─> T4 ──> T5 ──> T7 ──> T8

T1 + T2 ──> T9 ──┐
T1 + T2 ──> T10 ├─> T15 ──> T16 ──> T17 ──> T18
T7 ─────────> T11 ┘
T8 + T11 ──> T12 ──> T13
T3 ─────────> T14 ┘
```

推荐实际执行批次：

1. 基础类型与注册能力：T1、T2、T3、T4。
2. Agent 内核：T5、T6、T7、T8。
3. Provider 适配与 prompt：T9、T10、T11。
4. TUI 与入口：T12、T13、T14。
5. 全局兼容、测试和复盘：T15、T16、T17、T18。

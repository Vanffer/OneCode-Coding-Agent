# Agent Loop Checklist

> 每一项通过运行代码、单元测试或观察 TUI 行为来验证，聚焦 Phase 3 的系统行为。

## 实现完整性

- [ ] Agent 支持多轮 ReAct loop：同一用户请求中能连续请求模型、执行工具、回写结果并继续下一轮。（验证：运行 `go test ./internal/agent -run TestLoop`，观察多轮脚本场景通过）
- [ ] 工具调用和工具结果会写入 conversation，后续模型请求能看到前一轮结果。（验证：在 agent loop 测试中断言 provider 第 N 轮收到前一轮 assistant tool_calls 和 tool_result）
- [ ] 同一轮模型返回多个 tool call 时，Agent 会执行全部调用并回写全部结果。（验证：运行包含两个只读 tool call 的 mock provider 测试，断言产生两个 tool result）
- [ ] Agent 事件流包含文本、工具开始、工具结果、用量、进度、完成、错误、取消事件。（验证：运行 agent collector/loop 事件测试，断言各事件类型出现）
- [ ] 流式收集器一边实时转发文本增量，一边累积完整文本、tool calls 和 usage。（验证：运行 `go test ./internal/agent -run TestCollect`）
- [ ] Conversation 支持 assistant content + 多个 tool calls 写入。（验证：运行 `go test ./internal/conversation`）
- [ ] Registry 支持工具安全分类，并能按安全范围导出工具定义。（验证：运行 `go test ./internal/tools -run TestRegistry`）

## 工具调度

- [ ] 多个只读工具在同一批次中并发执行。（验证：fake tools 记录开始/结束时间，运行 `go test ./internal/agent -run TestScheduler`）
- [ ] 有副作用工具按模型返回顺序串行执行。（验证：fake tools 记录顺序，断言 write/edit/bash 类工具没有并发重叠）
- [ ] 混合工具批次不会把副作用工具后的只读工具提前执行。（验证：用 `[read, read, write, read]` 场景断言执行批次为 read/read -> write -> read）
- [ ] 未知工具被转成结构化错误 tool result，而不是导致程序崩溃。（验证：mock provider 请求不存在工具，断言 conversation 中写入 `IsError=true` 的 tool result）
- [ ] Plan Mode 中请求写文件、编辑文件或 bash 会被转成禁用工具错误结果。（验证：mock provider 在 `ModePlan` 下请求 `edit_file`，断言错误结果和 bad tool 计数）

## 停止条件

- [ ] 模型输出最终文本且没有工具调用时，Agent 以 `StopModelDone` 正常结束。（验证：mock provider 最后一轮只返回文本，断言 done reason）
- [ ] 达到迭代上限时，Agent 以 `StopMaxIterations` 停止并给出可读说明。（验证：mock provider 持续返回工具调用，设置小上限后运行测试）
- [ ] 连续未知工具或禁用工具达到上限时，Agent 以 `StopBadToolLimit` 停止。（验证：mock provider 连续请求 bad tool，断言停止原因）
- [ ] LLM 流式错误会停止本轮 loop，不继续执行工具或进入下一轮。（验证：collector/loop 测试注入 stream error，断言 `EventError` 和 `StopStreamError`）
- [ ] 用户取消后，Agent 不再发起下一轮 provider 请求。（验证：ctx cancel 测试中记录 provider 调用次数）

## Plan Mode

- [ ] `/plan <目标>` 会以 Plan 模式启动 Agent，只向模型暴露 `read_file`、`glob`、`grep`。（验证：TUI 或 agent 测试中检查传给 provider 的 tool definitions）
- [ ] `/plan` 正常完成后，最终 assistant 计划文本保存为 pending plan。（验证：TUI 测试或手动输入 `/plan ...` 后检查 `/do` 可执行）
- [ ] `/do` 在没有 pending plan 时只显示提示，不启动 Agent loop。（验证：手动或 TUI 测试输入 `/do`，断言没有 agent run）
- [ ] `/do` 在有 pending plan 时注入“执行该计划”的用户消息，并以 Execute 模式开放全部工具。（验证：TUI 测试断言 conversation 新增执行计划消息和工具定义全集）
- [ ] `/do` 开始消费计划后，无论完成、取消或失败，pending plan 都会被清理。（验证：分别模拟 Done/Error/Cancelled，断言 pending plan 为空）

## TUI 行为

- [ ] 普通用户输入仍以 Execute 模式启动 Agent loop。（验证：手动输入普通任务或 TUI 测试，观察 agent options）
- [ ] TUI 不直接调用 provider 或 registry，只消费 `agent.Event` 渲染。（验证：检查 `src/internal/tui/model.go` 调用边界，并运行 `go test ./internal/tui`）
- [ ] 文本增量在流式过程中实时显示，完成后仍能 Markdown 渲染最终回复。（验证：手动运行 TUI 或使用现有渲染路径 smoke test）
- [ ] 工具开始和工具结果以可区分的工具行展示，错误工具结果有明显标记。（验证：手动触发 read_file 成功和不存在文件失败，观察输出）
- [ ] 进度状态能体现请求模型、执行工具、继续下一轮、完成、取消或错误。（验证：成功、错误、取消三类场景观察状态栏或输出区）
- [ ] 执行中按 ESC 会取消当前任务，UI 显示取消提示并恢复输入状态。（验证：启动长任务后按 ESC，观察状态恢复）
- [ ] 取消不会清空已有 conversation 历史。（验证：取消后继续提问，确认历史仍可被后续请求携带）
- [ ] 可用 token 用量能显示；不可用时不显示伪造的 0。（验证：mock usage 可用/不可用两种事件，观察渲染）

## Provider 适配

- [ ] OpenAI 适配器能收集同一轮多个 tool call，而不是只保留第一个。（验证：运行 OpenAI 流事件单测或 mock stream 测试）
- [ ] OpenAI 适配器支持 assistant content + 多 tool calls 的历史消息转换。（验证：构造 conversation 消息并运行 provider 转换相关测试或编译检查）
- [ ] Anthropic 适配器能收集同一轮多个 `tool_use` block。（验证：运行 Anthropic 流事件单测或 mock stream 测试）
- [ ] Anthropic 适配器支持 assistant text block + 多 tool_use block 的历史消息转换。（验证：构造 conversation 消息并运行 provider 转换相关测试或编译检查）
- [ ] 两个 provider 都能映射 finish reason；拿不到 usage 时设置 `Available=false`。（验证：运行 `go test ./internal/llm`）
- [ ] ctx 取消时两个 provider 都能退出流式读取并关闭 channel。（验证：provider 取消测试或手动取消长请求观察无挂死）

## 兼容性与边界

- [ ] Phase 2 六个核心工具的参数和用户可见返回语义保持不变。（验证：运行 `go test ./internal/tools`）
- [ ] 普通无工具聊天仍可正常完成。（验证：mock provider 返回纯文本；或真实 provider smoke test）
- [ ] 单次工具任务仍可正常完成，只是现在走多轮 loop。（验证：请求读取一个文件并总结，观察工具结果回灌后最终回复）
- [ ] 本阶段没有加入权限确认、命令审批或 allow/deny 系统。（验证：检查代码入口和文档，没有交互式权限流程）
- [ ] 本阶段没有加入上下文压缩、自动摘要或长期记忆。（验证：检查 conversation/agent 中没有压缩或持久化逻辑）
- [ ] 本阶段没有持久化 pending plan，退出程序后不会恢复计划。（验证：检查 TUI 状态仅保存在内存字段）
- [ ] 本阶段没有引入 MCP 或外部工具协议。（验证：检查新增依赖和工具注册入口）
- [ ] 本阶段不计算费用，只处理 token 用量。（验证：检查 TUI/agent 没有价格计算逻辑）

## 编译与测试

- [ ] LLM 包测试通过。（验证：在 `src` 下运行 `go test ./internal/llm`）
- [ ] Conversation 包测试通过。（验证：在 `src` 下运行 `go test ./internal/conversation`）
- [ ] Tools 包测试通过。（验证：在 `src` 下运行 `go test ./internal/tools`）
- [ ] Agent 包测试通过且无偶发超时。（验证：在 `src` 下运行 `go test ./internal/agent -count=1`）
- [ ] TUI 包编译和测试通过。（验证：在 `src` 下运行 `go test ./internal/tui`）
- [ ] 全项目测试通过。（验证：在 `src` 下运行 `go test ./...`）
- [ ] 所有修改过的 Go 文件已 gofmt。（验证：运行 `gofmt` 后 `git diff` 不再出现格式化差异）

## 端到端场景

- [ ] 场景 1：普通多步任务能自动完成。输入“搜索某函数，读取相关文件，修改实现并运行测试”后，观察多轮工具调用、测试执行和最终总结。（验证：真实 provider 可用时手动运行；无真实 provider 时用 mock provider 集成测试）
- [ ] 场景 2：同一轮多个只读工具能全部执行。模型一次请求两个文件读取或一个 glob 加一个 grep，观察两个结果都回写后再继续。（验证：mock provider 集成测试）
- [ ] 场景 3：Plan Mode 两段式可用。输入 `/plan <目标>` 得到计划，再输入 `/do` 执行该计划，观察 `/plan` 阶段无写操作，`/do` 阶段可写可测。（验证：手动 TUI 或 scripted TUI 测试）
- [ ] 场景 4：没有计划时 `/do` 不误执行。启动后直接输入 `/do`，观察只显示提示，不发起 LLM 请求。（验证：手动 TUI 或 TUI 单测）
- [ ] 场景 5：取消可恢复。启动长任务后按 ESC，观察任务取消、输入框恢复，然后继续发送普通问题能得到响应。（验证：手动 TUI）
- [ ] 场景 6：循环保护生效。模型持续请求工具时，达到迭代上限后停止并说明原因。（验证：mock provider 集成测试）
- [ ] 场景 7：坏工具保护生效。模型连续请求不存在工具或 Plan Mode 禁用工具时，达到限制后停止并说明原因。（验证：mock provider 集成测试）
- [ ] 场景 8：Provider 行为一致。Anthropic 与 OpenAI 在多轮工具调用、多个 tool call、错误结果回写、usage 不可用表达上行为一致。（验证：分别用 mock 或真实配置运行同一脚本）

## 验收覆盖

| Spec AC | Checklist 覆盖 |
|---------|----------------|
| AC1 | 实现完整性、多轮 ReAct、端到端场景 1 |
| AC2 | 多工具调用、工具调度、端到端场景 2 |
| AC3 | 工具调度只读并发、副作用串行、混合批次顺序 |
| AC4 | Agent 事件流、TUI 行为、进度状态 |
| AC5 | 流式收集器双路输出 |
| AC6 | 迭代上限停止 |
| AC7 | ESC 取消流程、端到端场景 5 |
| AC8 | 连续坏工具停止、端到端场景 7 |
| AC9 | Plan Mode 两段式、端到端场景 3 |
| AC10 | 无 pending plan 的 `/do` 行为、端到端场景 4 |
| AC11 | 兼容性与边界、Phase 2 工具测试 |
| AC12 | Provider 适配、端到端场景 8 |
| AC13 | TUI 进度状态 |
| AC14 | Agent 自动化测试覆盖、编译与测试 |
| AC15 | 兼容性与边界中的不做事项检查 |

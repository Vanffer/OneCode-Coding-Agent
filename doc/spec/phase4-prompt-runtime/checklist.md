# Structured System Prompt Runtime Checklist

> 每一项通过运行代码、测试或观察行为验证，聚焦系统行为。

## 实现完整性

- [ ] 七个固定系统提示模块已实现，并按身份、系统约束、任务模式、动作执行、工具使用、语气风格、文本输出稳定排序。（验证：运行 `go test ./internal/prompt -run TestDefaultModules`）
- [ ] 稳定系统提示支持可选模块追加，且可选模块位于固定模块之后。（验证：运行 `go test ./internal/prompt -run TestBuildStableSystem`）
- [ ] 稳定系统提示主体不包含 cwd、日期、轮次、当前模式等动态信息。（验证：运行 `go test ./internal/prompt -run TestStableSystemExcludesDynamicContext`）
- [ ] Prompt Runtime 每轮生成环境 reminder，并包含 cwd、os、date、mode、iteration。（验证：运行 `go test ./internal/prompt -run TestBuildPayloadIncludesEnvironmentReminder`）
- [ ] Plan Mode 第 1、6、11 轮生成完整 reminder，其他轮次生成精简 reminder。（验证：运行 `go test ./internal/prompt -run TestPlanReminderCadence`）
- [ ] Execute Mode 不生成 Plan Mode reminder。（验证：运行 `go test ./internal/prompt -run TestExecuteModeSkipsPlanReminder`）
- [ ] 系统提示和工具描述都包含关键工具规则：编辑前先读、路径搜索用 glob、内容搜索用 grep、工具失败后调整策略。（验证：运行 prompt/tools 相关测试，检查断言通过）
- [ ] 人工对比场景文档已覆盖普通编辑、路径搜索、内容搜索、Plan Mode、多轮 reminder、工具错误、cache usage。（验证：阅读 `manual-scenarios.md`）

## 集成

- [ ] Provider 接口接收 `StreamOptions`，provider 内部不再直接读取全局 `prompt.SystemPrompt`。（验证：运行 `rg "SystemPrompt" src/internal`，确认 provider 中无直接依赖）
- [ ] Agent 每轮请求都会把 prompt payload 传给 provider。（验证：运行 `go test ./internal/agent -run TestAgentPassesPromptPayload`）
- [ ] Reminder 不写入 conversation 历史。（验证：运行 `go test ./internal/agent -run TestPromptRemindersDoNotEnterConversation`）
- [ ] Plan Mode 能收到 Plan reminder，Execute Mode 不收到 Plan reminder。（验证：运行 `go test ./internal/agent -run TestPromptPayloadByMode`）
- [ ] OpenAI provider 使用 prompt payload 构造 system/reminder 消息，普通 usage 仍可解析。（验证：运行 `go test ./internal/llm`）
- [ ] Anthropic provider 使用 prompt payload 构造 system blocks，并解析 cache creation/read usage。（验证：运行 `go test ./internal/llm`）
- [ ] Cache usage 不可用时通过 `Available=false` 表达，不展示为命中 0。（验证：运行 usage 映射测试和 TUI 编译）
- [ ] TUI 在 cache usage 可用时能展示简短 cache 信息，不可用时保持原 token 展示。（验证：运行 `go test ./...` 编译通过，必要时人工观察状态栏）

## 回归

- [ ] Phase 3 的多轮 Agent loop 仍然通过测试。（验证：运行 `go test ./internal/agent`）
- [ ] 现有 conversation 测试仍然通过。（验证：运行 `go test ./internal/conversation`）
- [ ] 现有 tools 测试仍然通过，工具 schema 和执行语义未改变。（验证：运行 `go test ./internal/tools`）
- [ ] 普通 Execute 模式仍能发起工具调用并完成任务。（验证：使用 mock provider 测试或现有 loop 测试）
- [ ] `/plan`、`/do`、ESC 取消相关路径没有编译或状态处理回归。（验证：运行 `go test ./...`，并做启动 smoke）

## 编译与测试

- [ ] 全项目测试通过。（验证：在 `src` 下运行 `go test ./...`）
- [ ] 启动 smoke 能进入 TUI。（验证：在 `src` 下运行 `go run ./cmd/onecode`，观察进入交互界面）
- [ ] 无 provider 直接依赖旧的全局 SystemPrompt。（验证：运行 `rg "SystemPrompt" src/internal`）
- [ ] 改动范围符合 Phase4，不包含无关文件回滚或无关重构。（验证：运行 `git status --short`）

## 端到端场景

- [ ] 场景 1：输入普通代码修改任务，模型先读取相关文件，再使用 edit/write，并在完成后报告验证结果。（验证：按 `manual-scenarios.md` 人工观察工具事件）
- [ ] 场景 2：输入路径查找任务，模型优先调用 glob，而不是 bash/find/猜路径。（验证：按 `manual-scenarios.md` 人工观察工具事件）
- [ ] 场景 3：输入内容搜索任务，模型优先调用 grep，而不是 bash/grep 命令。（验证：按 `manual-scenarios.md` 人工观察工具事件）
- [ ] 场景 4：输入 `/plan <任务>`，模型只调用只读工具并输出计划；计划不执行文件修改。（验证：观察工具事件和文件状态）
- [ ] 场景 5：Plan Mode 多轮工具调用后仍保持只读约束。（验证：构造需要多轮读取的计划任务，观察无 write/edit/bash）
- [ ] 场景 6：工具调用报错后，模型读取错误并调整策略。（验证：构造错误路径或错误参数，观察下一步修正）
- [ ] 场景 7：Provider 返回 cache usage 时，TUI/事件中能看到 cache read/create 信息；不支持时不显示误导信息。（验证：使用支持字段的 provider 或 mock usage）

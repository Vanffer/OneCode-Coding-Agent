# 上下文管理能力 Tasks

## 文件清单

| 操作 | 文件 | 职责 |
|---|---|---|
| 修改 | `src/internal/config/config.go` | `ProviderConfig` 增加 `context_window` |
| 修改 | `src/internal/conversation/conversation.go` | `Conversation` 持有 `ContextState` |
| 新建 | `src/internal/conversation/context.go` | 上下文管理总入口 |
| 新建 | `src/internal/conversation/bounder.go` | 工具结果轻量处理 |
| 新建 | `src/internal/conversation/estimator.go` | Token 估算 |
| 新建 | `src/internal/conversation/window.go` | 上下文窗口解析 |
| 新建 | `src/internal/conversation/store.go` | 项目上下文产物存储 |
| 新建 | `src/internal/conversation/files.go` | 近期文件索引 |
| 新建 | `src/internal/conversation/compactor.go` | 重量压缩策略 |
| 新建 | `src/internal/conversation/summary.go` | 摘要 prompt、解析、边界消息 |
| 修改 | `src/internal/agent/agent.go` | Agent 增加上下文管理相关 Option |
| 修改 | `src/internal/agent/loop.go` | preflight、usage 回写、紧急压缩重试 |
| 修改 | `src/internal/agent/events.go` | context event |
| 新建 | `src/internal/agent/compressor.go` | provider 压缩适配器 |
| 修改 | `src/internal/tui/model.go` | `/compact`、`/context`、状态栏展示 |
| 修改 | `src/README.md` | 使用说明 |
| 新建/修改 | `*_test.go` | 单元和集成测试 |

## T1: 配置字段扩展

**文件：** `src/internal/config/config.go`, `src/internal/config/config_test.go`  
**依赖：** 无  
**步骤：**
1. 给 `ProviderConfig` 增加可选 `context_window` 字段。
2. 在 provider 校验中允许该字段为空或正数。
3. 为 YAML 解析增加测试，覆盖未配置、配置正数、配置非法值。

**验证：** 在 `src` 下运行 `go test ./internal/config`，期望通过。

## T2: Conversation 状态骨架

**文件：** `src/internal/conversation/conversation.go`, `src/internal/conversation/context.go`  
**依赖：** 无  
**步骤：**
1. 增加 `ContextState`、`Option` 和默认初始化逻辑。
2. 让 `Conversation` 持有上下文状态。
3. 保留现有 `AddUser`、`AddAssistant`、`AddAssistantWithToolCalls`、`AddToolResult`、`Messages`、`MessageCount`、`Clear` 行为。
4. 提供 `ContextState()` 查询方法。

**验证：** 在 `src` 下运行 `go test ./internal/conversation -run TestConversation`，期望现有 conversation 行为不回退。

## T3: 项目上下文存储

**文件：** `src/internal/conversation/store.go`, `src/internal/conversation/store_test.go`  
**依赖：** T2  
**步骤：**
1. 实现 `ProjectStore` 和 `NewProjectStore`。
2. 实现 `.onecode/context` 和 `.onecode/context/tool-results` 创建。
3. 实现工具结果保存，返回保存路径、字节数和预览。
4. 实现 `local.yaml` 读写。
5. 实现 `.onecode/context/.gitignore` 自动维护，只补齐 `local.yaml` 和 `tool-results/`，保留已有内容。
6. 校验所有写入路径位于 `.onecode/context` 内。

**验证：** 在 `src` 下运行 `go test ./internal/conversation -run TestProjectStore`，期望目录、文件、忽略规则和路径校验通过。

## T4: Token 估算器

**文件：** `src/internal/conversation/estimator.go`, `src/internal/conversation/estimator_test.go`  
**依赖：** T2  
**步骤：**
1. 实现文本 token 近似估算。
2. 实现单条消息估算，覆盖 user、assistant、tool 和 tool calls。
3. 实现完整消息列表估算。
4. 实现 usage anchor + 新增消息增量估算。
5. 计算 limit、percent、estimated 和 updated time。

**验证：** 在 `src` 下运行 `go test ./internal/conversation -run TestTokenEstimator`，期望全量估算和锚点增量估算通过。

## T5: 窗口解析器

**文件：** `src/internal/conversation/window.go`, `src/internal/conversation/window_test.go`  
**依赖：** T1, T3  
**步骤：**
1. 实现 `WindowInfo`、`WindowSource` 和 `WindowResolver`。
2. 按 local > provider > inferred > default 的顺序解析窗口。
3. 实现初始模型窗口推断表。
4. 未识别模型返回默认窗口和默认来源。

**验证：** 在 `src` 下运行 `go test ./internal/conversation -run TestWindowResolver`，期望四级优先级和来源标记正确。

## T6: 工具结果轻量处理

**文件：** `src/internal/conversation/bounder.go`, `src/internal/conversation/bounder_test.go`  
**依赖：** T3, T4  
**步骤：**
1. 实现单项工具结果超阈值存盘。
2. 实现同一轮工具结果合计超阈值时按大小优先存盘。
3. 替换消息内容为预览、保存路径和重新读取提示。
4. 增加稳定 marker，避免同一工具结果重复存盘。
5. 确认用户消息和 assistant 文本不会被轻量处理改写。

**验证：** 在 `src` 下运行 `go test ./internal/conversation -run TestToolResultBounder`，期望单项、批量、幂等和用户消息保护测试通过。

## T7: 文件索引

**文件：** `src/internal/conversation/files.go`, `src/internal/conversation/files_test.go`  
**依赖：** T2, T3  
**步骤：**
1. 从 `read_file` 参数中提取路径，并从工具结果前若干行生成预览。
2. 从 `write_file` 和 `edit_file` 参数中提取路径，标记 edited reason。
3. 从存盘工具结果中记录保存路径。
4. 对工具结果文本中的项目内路径做简单提取。
5. 同一路径重复出现时更新 preview、reason 和 last seen。
6. 限制索引数量和单条 preview 长度。

**验证：** 在 `src` 下运行 `go test ./internal/conversation -run TestFileIndex`，期望路径提取、去重和长度限制通过。

## T8: 摘要构造与解析

**文件：** `src/internal/conversation/summary.go`, `src/internal/conversation/summary_test.go`  
**依赖：** T7  
**步骤：**
1. 定义 `Compressor`、`CompactInput`、`CompactOutput`。
2. 实现压缩输入构造，包含消息、文件索引、存盘结果和预算。
3. 实现摘要 prompt，要求不调用工具、先写草稿、再写正式摘要。
4. 解析 `<formal_summary>` 内容，丢弃 `<analysis_draft>`。
5. 构造 summary boundary user message，包含摘要、文件索引和重新读取提醒。

**验证：** 在 `src` 下运行 `go test ./internal/conversation -run TestSummaryBuilder`，期望摘要解析和 boundary message 通过。

## T9: 压缩器策略

**文件：** `src/internal/conversation/compactor.go`, `src/internal/conversation/compactor_test.go`  
**依赖：** T4, T5, T8  
**步骤：**
1. 实现 auto、manual、force、emergency 四种模式。
2. 实现自动、手动和强制阈值计算。
3. 实现从尾部保留约 10k token 或至少 5 条最近消息。
4. 调用 `Compressor` 获取正式摘要。
5. 用 summary boundary + recent messages 替换消息历史。
6. 返回 compact status 和 usage。

**验证：** 在 `src` 下运行 `go test ./internal/conversation -run TestCompactor`，期望阈值、近期保留和消息替换通过。

## T10: Preflight 总入口

**文件：** `src/internal/conversation/context.go`, `src/internal/conversation/context_test.go`  
**依赖：** T5, T6, T7, T9  
**步骤：**
1. 串联 `ProjectStore.Ensure`、`WindowResolver.Resolve`、`ToolResultBounder.Bound`、`TokenEstimator.Estimate` 和 `Compactor`。
2. 实现自动压缩失败计数和成功清零。
3. 连续失败达到限制时标记熔断。
4. 熔断后跳过自动压缩，但保留强制压缩逻辑。
5. 返回 `PreflightResult` 和 `ContextStatus`。

**验证：** 在 `src` 下运行 `go test ./internal/conversation -run TestPreflight`，期望轻量处理、自动压缩、熔断和状态返回通过。

## T11: Agent 事件扩展

**文件：** `src/internal/agent/events.go`  
**依赖：** T10  
**步骤：**
1. 新增 `EventContext`。
2. 新增 `ContextEvent` 和 context event kind。
3. 保持现有 event 类型和字段兼容。

**验证：** 在 `src` 下运行 `go test ./internal/agent`，期望现有 agent 测试仍通过。

## T12: ProviderCompressor

**文件：** `src/internal/agent/compressor.go`, `src/internal/agent/compressor_test.go`  
**依赖：** T8, T11  
**步骤：**
1. 实现 `providerCompressor`。
2. 用 `provider.Stream` 发起压缩请求，tools 参数传 nil。
3. 收集完整文本和 usage。
4. 遇到工具调用时返回压缩失败。
5. 找不到正式摘要区时返回压缩失败。

**验证：** 在 `src` 下运行 `go test ./internal/agent -run TestProviderCompressor`，期望禁工具、usage 收集和失败路径测试通过。

## T13: Agent Loop 接入

**文件：** `src/internal/agent/agent.go`, `src/internal/agent/loop.go`  
**依赖：** T10, T11, T12  
**步骤：**
1. Agent 增加上下文管理相关 Option 和默认 options。
2. 在每次 `provider.Stream` 前调用 `conversation.Preflight`。
3. 将 `PreflightResult.Statuses` 转成 context event。
4. 模型响应完成后调用 `conversation.UpdateUsage`。
5. 保持原有 ReAct loop、工具执行和停止条件不变。

**验证：** 在 `src` 下运行 `go test ./internal/agent -run TestLoop`，期望现有 loop 行为和新增 preflight 行为通过。

## T14: 紧急压缩重试

**文件：** `src/internal/agent/loop.go`, `src/internal/agent/loop_test.go`  
**依赖：** T13  
**步骤：**
1. 捕获 `llm.ContextTooLongError`。
2. 执行 emergency compact。
3. 压缩成功后重试原请求一次。
4. 压缩失败或重试仍超限时发送错误并停止。
5. 发送紧急压缩和重试 context event。

**验证：** 在 `src` 下运行 `go test ./internal/agent -run TestContextTooLong`，期望一次重试、失败停止和事件发送通过。

## T15: TUI 状态栏

**文件：** `src/internal/tui/model.go`, `src/internal/tui/model_test.go`  
**依赖：** T11  
**步骤：**
1. Model 增加 context usage、window、status 字段。
2. 处理 `EventContext`，更新上下文展示状态。
3. 修改 `statusBar`，展示 `Context ~used / limit · percent`。
4. 当窗口来源为 inferred/default 时显示估算标记。
5. 压缩开始、完成、失败、熔断、紧急重试时打印短提示。

**验证：** 在 `src` 下运行 `go test ./internal/tui`，期望状态栏文本和事件处理测试通过。

## T16: TUI 命令

**文件：** `src/internal/tui/model.go`, `src/internal/tui/model_test.go`  
**依赖：** T13, T15  
**步骤：**
1. 新增 `/compact`，触发手动压缩。
2. 新增 `/context`，展示当前窗口来源、使用量和提示。
3. 新增 `/context window`，进入数字输入流程。
4. 用户输入窗口大小后调用 `SetContextWindow` 并保存到项目本地配置。
5. 保存成功后立即刷新状态栏上下文窗口。

**验证：** 在 `src` 下运行 `go test ./internal/tui -run TestContextCommands`，期望命令解析、保存和展示刷新通过。

## T17: 文档更新

**文件：** `src/README.md`  
**依赖：** T16  
**步骤：**
1. 说明 `/compact` 的行为。
2. 说明 `/context` 和 `/context window` 的行为。
3. 说明上下文窗口推断和本地覆盖。
4. 说明 `.onecode/context` 产物目录和子目录 `.gitignore` 策略。

**验证：** 人工检查 README 示例命令与实现一致。

## T18: 全量验证

**文件：** 全项目  
**依赖：** T1-T17  
**步骤：**
1. 运行 conversation 测试。
2. 运行 agent 测试。
3. 运行 tui 测试。
4. 运行 config 测试。
5. 运行全模块测试。

**验证：** 在 `src` 下运行 `go test ./...`，期望通过。

## 执行顺序

```text
T1 ─┐
T2 ─┼─> T3 ─┬─> T5 ─┐
     │       └─> T6 ─┼─> T10 ─> T11 ─> T12 ─> T13 ─> T14
     ├─> T4 ────────┘
     └─> T7 ─> T8 ─> T9 ───────┘

T15 ─> T16 ─> T17 ─> T18
```

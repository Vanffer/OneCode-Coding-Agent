# Agent Loop Implementation Notes

## 阶段目标回顾

Phase 3 将 Phase 2 的“单次工具调用闭环”升级为 ReAct 风格的多轮 Agent loop：

```text
用户输入
  -> LLM 流式响应
  -> Agent 收集文本、工具调用、usage
  -> 执行本轮全部工具
  -> tool result 回写 Conversation
  -> 继续下一轮 LLM 请求
  -> 直到模型完成、取消、出错或触发保护上限
```

这次仍然不做权限系统、上下文压缩、交互式确认和 MCP，只处理内置工具的多轮调度。

## 主要改动

### Agent Runtime

`internal/agent` 从单文件单轮闭环拆成了几个职责明确的文件：

```text
agent.go       - Agent、Run、默认 RunOptions
events.go      - Mode、Event、Progress、Usage、Done、StopReason
collector.go   - 流式双路收集器
scheduler.go   - 多工具调度器
loop.go        - ReAct loop 主状态机
format.go      - 参数摘要、结果摘要、停止原因文案
```

`Agent.Run` 现在接收：

```go
Run(ctx, conv, RunOptions)
```

默认配置：

- 最大迭代数：20
- 连续未知/禁用工具上限：3
- 默认模式：Execute

### 流式收集器

`collector.go` 做两件事：

- 将 `llm.StreamEvent.Text` 立即转成 `agent.EventText` 给 TUI。
- 同时累积完整 `ModelResponse`，包括 `Text`、`ToolCalls`、`Usage`、`FinishReason`。

这样 UI 能实时显示文本，而 Agent 又能在一轮结束后判断是否需要执行工具。

### 工具调度器

`scheduler.go` 负责同一轮多个 tool call：

- 未知工具：转成 `ToolResult{IsError: true}`。
- Plan Mode 禁用工具：转成结构化错误。
- 连续只读工具：并发执行。
- 有副作用工具：按模型返回顺序串行执行。
- 混合批次：不把副作用工具后面的只读工具提前执行。

结果回写保持原始 tool call 顺序，避免 provider 协议关联混乱。

### Conversation

`AddAssistantWithToolCalls` 从只保存 tool calls，扩展为：

```go
AddAssistantWithToolCalls(content string, toolCalls []llm.ToolCall)
```

这样模型在工具调用前输出的文本不会丢失。

### Tools Registry

Registry 增加固定安全分类：

```text
SafetyReadOnly:
  read_file
  glob
  grep

SafetySideEffect:
  write_file
  edit_file
  bash
```

入口层 `cmd/onecode/main.go` 使用 `RegisterWithSafety` 显式注册六个内置工具。

### Provider 适配

`llm.StreamEvent` 增加：

```go
Usage *Usage
FinishReason FinishReason
```

OpenAI 适配器现在按 `tool_call.index` 聚合多个 tool call 参数，结束时按 index 顺序吐出全部完整调用。

Anthropic 适配器继续按 `tool_use` content block 吐出工具调用，并补充 message delta 中的 usage 与 stop reason 映射。

### TUI

TUI 接入了新的 Agent 事件流：

- `EventText`：继续实时追加到当前回复。
- `EventToolStart` / `EventToolResult`：展示工具行和结果摘要。
- `EventProgress`：更新状态栏文案。
- `EventUsage`：usage 可用时更新状态。
- `EventCancelled` / `EventDone` / `EventError`：恢复输入或展示错误。

新增状态：

```go
cancelCurrent context.CancelFunc
pendingPlan   *PendingPlan
currentMode   agent.Mode
progressStatus string
```

`/plan` 会以只读模式启动 Agent，并在正常完成后保存 pending plan。

`/do` 会消费 pending plan，以 Execute 模式执行；执行开始后无论完成、失败或取消都会清理计划。

ESC 会调用当前 run 的 cancel 函数。

## 数据流

### 普通 Execute

```text
TUI 输入
  -> conv.AddUser(input)
  -> context.WithCancel
  -> agent.Run(ctx, conv, ModeExecute)
  -> provider.Stream(..., 全部工具)
  -> collector 收集响应
  -> scheduler 执行工具
  -> conv.AddToolResult(...)
  -> 下一轮 provider.Stream
```

### Plan / Do

```text
/plan target
  -> 注入 Plan Mode 用户消息
  -> 只导出 read_file/glob/grep
  -> 完成后 pendingPlan = assistant final text

/do
  -> 注入 Execute pending plan 用户消息
  -> 导出全部工具
  -> 执行完成/错误/取消后 pendingPlan = nil
```

### 取消

```text
ESC
  -> TUI 调 cancelCurrent()
  -> provider.Stream / registry.Execute / tool.Execute 收到 ctx.Done()
  -> Agent 发 EventCancelled + EventDone(StopCancelled)
  -> TUI 恢复输入
```

## 测试覆盖

新增或扩展的测试：

- `internal/conversation`：assistant content + tool calls 写入。
- `internal/tools`：安全分类、按 safety 导出工具定义。
- `internal/agent/collector_test.go`：文本转发、tool call 收集、usage、流错误。
- `internal/agent/scheduler_test.go`：只读并发、副作用串行、未知/禁用工具。
- `internal/agent/loop_test.go`：多轮工具、迭代上限、坏工具上限、取消。

验证命令：

```powershell
cd src
$env:GOCACHE = Join-Path (Resolve-Path ..).Path ".gocache"
go test ./...
```

结果：全项目测试通过。

启动 smoke：

```powershell
go run ./cmd/onecode
```

当前仓库存在配置文件，程序进入交互式 TUI，命令在 5 秒探测超时后被终止；这说明启动路径能进入交互界面，没有在初始化阶段崩溃。

## 当前限制

- 没有做权限确认或命令审批。
- 没有做上下文压缩。
- 没有持久化 pending plan。
- 没有真实 API 的端到端工具任务验证；核心编排由 mock provider 和 fake tools 覆盖。
- Token usage 取决于 provider 是否在流式响应中提供；不可用时不会伪造为 0。

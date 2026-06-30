# Structured System Prompt Runtime Tasks

## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 新建 | `src/internal/prompt/modules.go` | 定义提示模块、固定模块顺序、稳定系统提示拼接 |
| 新建 | `src/internal/prompt/runtime.go` | Prompt Runtime、BuildOptions、Payload、RequestContext |
| 新建 | `src/internal/prompt/reminder.go` | Environment reminder、Plan Mode reminder、注入频率 |
| 新建 | `src/internal/prompt/modules_test.go` | 模块顺序、格式、可选模块测试 |
| 新建 | `src/internal/prompt/runtime_test.go` | 稳定/动态分离、payload 测试 |
| 新建 | `src/internal/prompt/reminder_test.go` | Plan Mode reminder 轮次测试 |
| 修改 | `src/internal/prompt/prompt.go` | 保留 banner，移除 provider 对全局 SystemPrompt 的依赖 |
| 修改 | `src/internal/llm/provider.go` | 增加 StreamOptions、CacheUsage，扩展 Provider.Stream |
| 修改 | `src/internal/llm/openai.go` | 使用 prompt payload 构造请求，cache usage 不可用时显式表达 |
| 修改 | `src/internal/llm/anthropic.go` | 使用 prompt payload 构造 system blocks，解析 cache usage |
| 修改 | `src/internal/agent/agent.go` | Agent 持有 Prompt Runtime，补充默认 ReminderInterval |
| 修改 | `src/internal/agent/events.go` | UsageEvent 增加 cache usage 字段 |
| 修改 | `src/internal/agent/collector.go` | 转发 cache usage |
| 修改 | `src/internal/agent/loop.go` | 每轮构造 prompt payload 并传入 StreamOptions |
| 修改 | `src/internal/agent/*_test.go` | 更新 mock provider 签名，补充 reminder 注入测试 |
| 修改 | `src/internal/tui/model.go` | usage 展示兼容 cache 字段 |
| 修改 | `src/internal/tools/*.go` | 强化工具描述，不改 schema 和执行逻辑 |
| 修改 | `src/internal/tools/registry_test.go` | 补充关键工具描述规则断言 |
| 新建 | `doc/spec/phase4-prompt-runtime/manual-scenarios.md` | 人工对比场景 |
| 新建 | `doc/spec/phase4-prompt-runtime/task.md` | 本任务拆解 |
| 新建 | `doc/spec/phase4-prompt-runtime/checklist.md` | 验收清单 |

## T1: 实现 Prompt 模块装配

**文件：** `src/internal/prompt/modules.go`、`src/internal/prompt/modules_test.go`  
**依赖：** 无

**步骤：**
1. 定义 `ModuleKind`、`Module`、七个固定模块和三个可选模块 kind。
2. 实现 `DefaultModules()`，按身份、系统约束、任务模式、动作执行、工具使用、语气风格、文本输出返回模块。
3. 实现 `BuildStableSystem(BuildOptions)`，按固定模块加可选模块拼接。
4. 模块之间使用空行分隔。
5. 对空标题、空正文、未知必需模块等情况返回错误。
6. 编写测试验证固定模块顺序、格式、可选模块追加位置、动态信息不出现在稳定主体中。

**验证：** `go test ./internal/prompt -run TestBuildStableSystem`

## T2: 实现 Prompt Runtime 与动态 Reminder

**文件：** `src/internal/prompt/runtime.go`、`src/internal/prompt/reminder.go`、`src/internal/prompt/runtime_test.go`、`src/internal/prompt/reminder_test.go`  
**依赖：** T1

**步骤：**
1. 定义 `Runtime`、`BuildOptions`、`Payload`、`RequestContext`。
2. 定义 `Reminder`、`ReminderKind`。
3. 实现 `NewRuntime` 和 `StableSystem()`。
4. 实现 `BuildPayload(RequestContext)`，每轮返回稳定系统提示和动态 reminders。
5. 实现 environment reminder，包含 cwd、os、date、mode、iteration。
6. 实现 Plan Mode full/compact reminder。
7. 实现 `ShouldInjectFullPlanReminder(iteration, interval)`，默认 interval 为 5。
8. 编写测试验证第 1、6、11 轮完整 reminder，其他轮次 compact reminder，Execute Mode 不注入 Plan reminder。

**验证：** `go test ./internal/prompt`

## T3: 扩展 LLM 抽象

**文件：** `src/internal/llm/provider.go`  
**依赖：** T2

**步骤：**
1. 引入 prompt payload 类型，定义 `StreamOptions`。
2. 扩展 `Provider.Stream` 签名，增加 `opts StreamOptions`。
3. 定义 `CacheUsage`。
4. 在 `Usage` 中增加 `Cache CacheUsage`。
5. 保持 `StreamEvent` 字段结构兼容。
6. 更新注释，说明 provider 不再直接读取全局 system prompt。

**验证：** `go test ./internal/llm` 预期此时可能因调用方未更新失败；完成后续 T4/T5 后全量验证。

## T4: 接入 OpenAI Provider Prompt Payload

**文件：** `src/internal/llm/openai.go`  
**依赖：** T3

**步骤：**
1. 修改 `Stream` 签名接收 `StreamOptions`。
2. 移除 provider 内部直接读取 `prompt.SystemPrompt`。
3. 将 `opts.Prompt.StableSystem` 作为首条 system 消息。
4. 将 `opts.Prompt.Reminders` 按顺序追加为系统级消息，内容保持 `<system-reminder>` 标签。
5. 会话历史和工具定义映射保持原逻辑。
6. OpenAI cache usage 暂按不可用表达，除非当前 SDK 已提供明确字段。
7. 确保普通 usage 仍正常解析。

**验证：** `go test ./internal/llm` 编译通过。

## T5: 接入 Anthropic Provider Prompt Payload 和 Cache Usage

**文件：** `src/internal/llm/anthropic.go`  
**依赖：** T3

**步骤：**
1. 修改 `Stream` 签名接收 `StreamOptions`。
2. 移除 provider 内部直接读取 `prompt.SystemPrompt`。
3. 将 `opts.Prompt.StableSystem` 作为第一个 system text block。
4. 将 reminders 作为后续 system text block。
5. 尽力给稳定 system block 设置 cache control；若 SDK 类型限制复杂，则保持稳定 block 顺序并记录不可用/部分可用。
6. 从 message delta usage 中解析 cache creation/read tokens。
7. 将 cache 字段写入 `llm.Usage.Cache`。
8. 保持工具、tool_result、thinking 逻辑不变。

**验证：** `go test ./internal/llm` 编译通过。

## T6: Agent 接入 Prompt Runtime

**文件：** `src/internal/agent/agent.go`、`src/internal/agent/events.go`、`src/internal/agent/collector.go`、`src/internal/agent/loop.go`、`src/internal/agent/*_test.go`  
**依赖：** T2、T3、T4、T5

**步骤：**
1. 在 `Agent` 中增加 `*prompt.Runtime` 字段。
2. 更新 `New`，支持传入 runtime；必要时保留默认 runtime 创建路径。
3. `RunOptions` 增加 `ReminderInterval`，默认值为 5。
4. 为 `Mode` 增加稳定字符串表示，用于 prompt context。
5. 在每轮 `provider.Stream` 前构造 `prompt.RequestContext`。
6. 调用 `BuildPayload`，通过 `llm.StreamOptions` 传入 provider。
7. 扩展 `UsageEvent`，携带 cache usage。
8. 更新 collector 转发 usage 的逻辑。
9. 更新所有 mock provider 签名。
10. 新增测试验证 Plan Mode reminder 出现在 provider 收到的 payload 中，且 conversation 不包含 reminder。
11. 新增测试验证 Execute Mode 不注入 Plan reminder。

**验证：** `go test ./internal/agent`

## T7: 更新 TUI Usage 展示

**文件：** `src/internal/tui/model.go`  
**依赖：** T6

**步骤：**
1. 更新 `EventUsage` 处理逻辑，读取 cache usage 可用状态。
2. cache usage 可用时，在状态文本中追加简短信息，例如 `cache read X/create Y`。
3. cache usage 不可用时保持原有 token 展示，不显示误导性的 0。
4. 确保 `/plan`、`/do`、ESC 状态逻辑不变。

**验证：** `go test ./...` 编译通过。

## T8: 强化工具描述

**文件：** `src/internal/tools/read_file.go`、`write_file.go`、`edit_file.go`、`bash.go`、`glob.go`、`grep.go`、`registry_test.go`  
**依赖：** 无，可与 T1-T7 并行

**步骤：**
1. 更新 `read_file` 描述，强调读取真实上下文和编辑前读取。
2. 更新 `edit_file` 描述，强调先读文件、精确匹配、适合局部修改。
3. 更新 `write_file` 描述，强调创建/覆盖副作用。
4. 更新 `glob` 描述，强调路径搜索。
5. 更新 `grep` 描述，强调内容搜索。
6. 更新 `bash` 描述，强调有副作用，文件读写搜索优先用专用工具。
7. 添加或扩展测试，断言关键描述词存在。
8. 确认 schema、执行逻辑、返回格式未改。

**验证：** `go test ./internal/tools`

## T9: 编写人工对比场景文档

**文件：** `doc/spec/phase4-prompt-runtime/manual-scenarios.md`  
**依赖：** spec/plan 已确认

**步骤：**
1. 编写普通编辑任务场景：观察是否先读再改。
2. 编写路径搜索场景：观察是否使用 glob。
3. 编写内容搜索场景：观察是否使用 grep。
4. 编写 Plan Mode 只读场景。
5. 编写多轮 Plan Mode reminder 场景。
6. 编写工具错误恢复场景。
7. 编写 cache usage 观察场景。
8. 每个场景包含输入、期望观察点、非目标。

**验证：** 人工 review 文档，确认覆盖 spec 的 F11。

## T10: 全量集成与清理

**文件：** 涉及所有修改文件  
**依赖：** T1-T9

**步骤：**
1. 运行 `go test ./...`。
2. 修复所有编译和测试问题。
3. 运行 `go run ./cmd/onecode` 启动 smoke，确认能进入 TUI。
4. 检查 `rg "SystemPrompt" src/internal`，确认 provider 不再直接依赖全局 SystemPrompt。
5. 检查 `git status --short`，确认改动范围符合 Phase4。
6. 如有必要，补充 implementation notes 的提纲，但不在本任务强制完成。

**验证：** `go test ./...` 通过，启动 smoke 能进入 TUI，无 provider 直接读取 `prompt.SystemPrompt`。

## 执行顺序

```text
T1 -> T2 -> T3 -> T4 -> T5 -> T6 -> T7 -> T10
                  ↘
                   T8 -----------↗
T9 ------------------------------↗
```

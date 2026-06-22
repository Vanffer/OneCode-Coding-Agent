# 工具系统 Tasks

## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 新建 | `internal/tools/tool.go` | Tool 接口、Result 结构 |
| 新建 | `internal/tools/registry.go` | Registry 注册中心 |
| 新建 | `internal/tools/read_file.go` | read_file 工具 |
| 新建 | `internal/tools/write_file.go` | write_file 工具 |
| 新建 | `internal/tools/edit_file.go` | edit_file 工具 |
| 新建 | `internal/tools/bash.go` | bash 工具 |
| 新建 | `internal/tools/glob.go` | glob 工具 |
| 新建 | `internal/tools/grep.go` | grep 工具 |
| 新建 | `internal/agent/agent.go` | Agent 单轮闭环 |
| 修改 | `internal/llm/provider.go` | Message/StreamEvent/ToolDefinition/ToolCall/ToolResult；Provider 接口增加 tools 参数 |
| 修改 | `internal/llm/anthropic.go` | 注入工具定义、解析流式 tool_use、工具结果回灌 |
| 修改 | `internal/llm/openai.go` | 注入工具定义、解析流式 tool_use、工具结果回灌 |
| 修改 | `internal/conversation/conversation.go` | 新增 AddAssistantWithToolCalls、AddToolResult |
| 修改 | `internal/prompt/system.txt` | 增补 Agent 角色与工具使用约定 |
| 修改 | `internal/tui/model.go` | submit 改走 agent.Run；事件泵处理工具事件 |
| 修改 | `internal/tui/styles.go` | 新增工具行样式 |
| 修改 | `cmd/onecode/main.go` | 构造 Registry、注入 tui.New |

## T1: Tool 接口与 Result 结构

**文件：** `internal/tools/tool.go`
**依赖：** 无
**步骤：**
1. 定义 `Result` 结构体（Content string、IsError bool）
2. 定义 `Tool` 接口（Name、Description、Schema、Timeout、Execute）
3. 添加 `time` 导入

**验证：** `go build ./internal/tools/...` 编译通过

## T2: Registry 注册中心

**文件：** `internal/tools/registry.go`
**依赖：** T1
**步骤：**
1. 定义 `Registry` 结构体（order []string、tools map[string]Tool）
2. 实现 `NewRegistry()`、`Register(t Tool)`、`Get(name string)`
3. 实现 `List() []Tool`（按 order 顺序）
4. 实现 `ToToolDefinitions() []llm.ToolDefinition`
5. 实现 `Execute(ctx, name, args)`：查找→超时→panic 捕获

**验证：** 单测：Register 3 个 mock tool → List 顺序一致 → Get 存在/不存在 → Execute 成功/超时/panic

## T3: read_file 工具

**文件：** `internal/tools/read_file.go`
**依赖：** T1
**步骤：**
1. 定义 `readFileArgs` struct（Path string）
2. 实现 Tool 接口所有方法
3. Execute：`os.ReadFile` → 按行分割 → 加行号（`%d\t%s`）→ 截断 2000 行 / 256KB

**验证：** 单测：读存在文件（行号正确）→ 读不存在文件（IsError=true）→ 读超长文件（截断标注）

## T4: write_file 工具

**文件：** `internal/tools/write_file.go`
**依赖：** T1
**步骤：**
1. 定义 `writeFileArgs` struct（Path、Content string）
2. 实现 Tool 接口
3. Execute：`os.MkdirAll` 建父目录 → `os.WriteFile` 覆盖写 → 返回字节数

**验证：** 单测：写新文件（磁盘存在）→ 写嵌套路径（父目录自动创建）→ 覆盖已有文件

## T5: edit_file 工具

**文件：** `internal/tools/edit_file.go`
**依赖：** T1
**步骤：**
1. 定义 `editFileArgs` struct（Path、OldString、NewString string）
2. 实现 Tool 接口
3. Execute：`os.ReadFile` → `strings.Count(content, old)` → 0 处报错 → >1 处报错 → ==1 时 `strings.Replace` 写回

**验证：** 单测：唯一替换成功 → 0 匹配（IsError）→ 多匹配（IsError，含 N 处提示）

## T6: bash 工具

**文件：** `internal/tools/bash.go`
**依赖：** T1
**步骤：**
1. 定义 `bashArgs` struct（Command string）
2. 实现 Tool 接口，Timeout 返回 5 分钟
3. Execute：`runtime.GOOS` 检测 → 构造 `exec.CommandContext` → 捕获 stdout+stderr → 格式化 exit code → 截断 30000 字符
4. 超时返回 IsError=true；非零退出不标 IsError

**验证：** 单测：`echo hello`（stdout 正确）→ 不存在的命令（exit code 非零，非 IsError）→ 超时命令（IsError=true）

## T7: glob 工具

**文件：** `internal/tools/glob.go`
**依赖：** T1
**步骤：**
1. 定义 `globArgs` struct（Pattern string、Path optional string）
2. 实现 Tool 接口
3. Execute：`filepath.WalkDir` + 自实现 `**` 匹配 → 返回匹配路径列表（≤100，排序）

**验证：** 单测：`**/*.go` 匹配正确 → 无匹配返回空说明（非 IsError）→ 限制 100 条

## T8: grep 工具

**文件：** `internal/tools/grep.go`
**依赖：** T1
**步骤：**
1. 定义 `grepArgs` struct（Pattern string、Path optional、Glob optional）
2. 实现 Tool 接口
3. Execute：`regexp.Compile` → `filepath.WalkDir` 过滤文件 → 逐行扫描匹配 → 格式化 `file:line:content` → 截断 100 条
4. 超长行标注 `[line too long, skipped]`

**验证：** 单测：搜已知关键字（结果正确）→ 正则非法（IsError）→ 无命中（非 IsError）

## T9: llm 类型扩展

**文件：** `internal/llm/provider.go`
**依赖：** 无
**步骤：**
1. 新增 `ToolDefinition` struct
2. 新增 `ToolCall` struct
3. 新增 `ToolResult` struct
4. 扩展 `Message`：增加 ToolCalls、ToolResult 字段
5. 扩展 `StreamEvent`：增加 ToolCall 字段
6. 修改 `Provider.Stream` 签名：增加 `tools []ToolDefinition` 参数

**验证：** `go build ./internal/llm/...` 编译通过（适配器暂时注释或传 nil）

## T10: Registry.ToToolDefinitions 实现

**文件：** `internal/tools/registry.go`
**依赖：** T2、T9
**步骤：**
1. 实现 `ToToolDefinitions()`：遍历 order，每个 tool 调用 Name()、Description()、Schema() 构造 `llm.ToolDefinition`

**验证：** 单测：注册 3 个工具 → ToToolDefinitions 返回 3 个定义，顺序一致

## T11: Anthropic 适配器改造

**文件：** `internal/llm/anthropic.go`
**依赖：** T9
**步骤：**
1. Stream 方法接收 tools 参数，转成 `anthropic.ToolParam` 注入请求
2. 流式解析 tool_use：`content_block_start` 记录 ID/Name → `content_block_delta` 拼 JSON → `content_block_stop` 解析并吐出 ToolCall
3. 工具结果回灌：`ToolResult` 转 `anthropic.NewToolResultBlock`
4. thinking 与工具组合：历史含工具时关闭 thinking

**验证：** 手动测试：请求带工具 → 流式解析出 ToolCall → 回灌结果 → 第二轮请求成功

## T12: OpenAI 适配器改造

**文件：** `internal/llm/openai.go`
**依赖：** T9
**步骤：**
1. Stream 方法接收 tools 参数，转成 `openai.ToolParam` 注入请求
2. 流式解析 tool_use：`delta.tool_calls[0]` 收集 ID/Name/Arguments → 拼接完成时解析 JSON 吐出 ToolCall
3. 工具结果回灌：`ToolResult` 转 `openai.ToolMessage`
4. 空参数归一：空 arguments 归一为 "{}"

**验证：** 同 T11，手动测试 OpenAI 协议

## T13: conversation 扩展

**文件：** `internal/conversation/conversation.go`
**依赖：** T9
**步骤：**
1. 新增 `AddAssistantWithToolCalls(toolCalls []llm.ToolCall)`
2. 新增 `AddToolResult(result llm.ToolResult)`
3. `Messages()` 返回时保持角色交替正确

**验证：** 单测：AddUser → AddAssistantWithToolCalls → AddToolResult → Messages 长度和角色正确

## T14: Agent 单轮闭环

**文件：** `internal/agent/agent.go`
**依赖：** T9、T10、T13
**步骤：**
1. 定义 `Agent` struct、`Event`、`ToolEvent`、`Phase`
2. 实现 `New(p llm.Provider, r *tools.Registry)`
3. 实现 `Run(ctx, conv)`：
   - 第一轮 Stream（带工具）→ 收集 Text/ToolCall
   - 有 ToolCall → PhaseStart → Execute → PhaseEnd → 回灌 → 第二轮 Stream（不带工具）
   - 无 ToolCall → 直接结束
4. 空最终答复 → 占位提示

**验证：** 单测（mock provider）：纯文本无工具 → 有工具调用执行并回灌 → 错误不中断

## T15: prompt 扩展

**文件：** `internal/prompt/system.txt`
**依赖：** 无
**步骤：**
1. 增加 Agent 角色说明
2. 增加工具使用约定（何时调用、如何组织参数、失败时重试）
3. 增加行号引用说明

**验证：** 内容审查，确认包含关键约定

## T16: tui 改造 — Agent 接入

**文件：** `internal/tui/model.go`
**依赖：** T14
**步骤：**
1. Model 新增 `agent *agent.Agent` 字段
2. New 函数接收 `*tools.Registry`，构造 Agent
3. submit 改为调用 `agent.Run(ctx, conv)`
4. 新增 `waitForAgentEvent` Cmd
5. Update 处理 `agentEventMsg`：Text 累积、Tool 展示、Done 回到 idle、Err 展示
6. 用 `tea.Sequence` 保证 scrollback 顺序

**验证：** 手动测试：输入触发工具调用的问题 → 工具行展示 → 结果摘要 → 最终回复

## T17: tui 样式 — 工具行

**文件：** `internal/tui/styles.go`
**依赖：** 无
**步骤：**
1. 新增 `toolNameStyle`（● 图标样式）
2. 新增 `toolArgsStyle`（参数预览样式）
3. 新增 `toolResultStyle`（结果摘要样式）
4. 新增 `toolErrorStyle`（错误样式）

**验证：** 编译通过，手动查看样式效果

## T18: main.go 改造

**文件：** `cmd/onecode/main.go`
**依赖：** T1-T16
**步骤：**
1. 构造 `tools.NewRegistry()`
2. 注册 6 个工具
3. 传入 `tui.New(providers, registry)`

**验证：** `go build` 编译通过 → 启动程序 → 输入「读 main.go」→ 触发工具调用

## 执行顺序

```
T1 → T2 → T10
         ↘
T3 T4 T5 T6 T7 T8（可并行，都只依赖 T1）

T9 → T11 T12（可并行，都依赖 T9）
   → T13
         ↘
T14（依赖 T9/T10/T13）

T15（无依赖，可随时）
T17（无依赖，可随时）

T16（依赖 T14）
T18（依赖全部）
```

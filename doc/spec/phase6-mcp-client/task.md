# Phase 6 MCP Client Tasks

## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 修改 | `src/internal/config/config.go` | 扩展 MCP 配置结构、合并加载、校验、环境变量展开 |
| 修改 | `src/internal/config/config_test.go` | 覆盖 MCP 配置加载、合并、覆盖、变量展开和错误脱敏 |
| 新建 | `src/internal/mcp/types.go` | MCP 协议结构、工具结构、公共错误结构 |
| 新建 | `src/internal/mcp/jsonrpc.go` | JSON-RPC 请求、响应、错误和 result 解码 |
| 新建 | `src/internal/mcp/transport.go` | Transport 接口、transport 工厂和公共错误 |
| 新建 | `src/internal/mcp/client.go` | 单 Server MCP 生命周期：initialize、tools/list、tools/call |
| 新建 | `src/internal/mcp/http.go` | Streamable HTTP 请求/响应式 transport |
| 新建 | `src/internal/mcp/stdio.go` | stdio 子进程 transport、pending id 配对、关闭清理 |
| 新建 | `src/internal/mcp/adapter.go` | RemoteTool 适配、工具命名、schema/result 转换、Safety 判定 |
| 新建 | `src/internal/mcp/manager.go` | 多 Server 发现、失败隔离、工具注册、生命周期管理 |
| 新建 | `src/internal/mcp/*_test.go` | MCP 各层单元测试和集成测试 |
| 新建 | `src/internal/mcp/testdata/stdio_server.go` | 测试用假 MCP stdio server |
| 修改 | `src/internal/tools/registry.go` | 如需要，增加工具名存在性检查，避免注册冲突静默丢失 |
| 修改 | `src/internal/tools/registry_test.go` | 覆盖新增 registry 冲突辅助方法 |
| 修改 | `src/internal/agent/scheduler_test.go` | 覆盖 MCP-style 工具 Safety 对 Plan Mode 的影响 |
| 修改 | `src/cmd/onecode/main.go` | 启动期加载合并配置、发现并注册 MCP 工具、退出关闭 Manager |

## T1: 扩展配置结构

**文件：** `src/internal/config/config.go`

**依赖：** 无

**步骤：**

1. 在 `Config` 中增加 `MCPServers map[string]MCPConfig` 字段，yaml key 为 `mcp_servers`。
2. 新增 `MCPConfig`，包含 `type`、`command`、`args`、`env`、`url`、`headers`、`read_only`、`tools`。
3. 新增 `MCPToolConfig`，包含可选 `read_only` 指针字段，用于区分未设置和显式 false。
4. 保持 `ProviderConfig` 和现有 `Load(path)` 的对外语义不变。

**验证：** 运行 `go test ./internal/config -run TestLoadValidConfig`，现有 provider 配置测试通过。

## T2: 实现 MCP 配置校验

**文件：** `src/internal/config/config.go`、`src/internal/config/config_test.go`

**依赖：** T1

**步骤：**

1. 扩展配置校验逻辑，允许没有 `mcp_servers`。
2. 校验 MCP server 名称不能为空。
3. 校验 `type` 只能是 `stdio` 或 `http`。
4. 校验 stdio server 必须提供 `command`。
5. 校验 http server 必须提供 `url`。
6. 为非法 type、缺 command、缺 url 添加测试。

**验证：** 运行 `go test ./internal/config -run TestLoadMCPConfigValidation`，非法配置返回清晰错误。

## T3: 实现用户级和项目级配置合并

**文件：** `src/internal/config/config.go`、`src/internal/config/config_test.go`

**依赖：** T2

**步骤：**

1. 新增 `LoadMerged(userPath, projectPath string) (*Config, error)`。
2. 项目级配置必须存在并提供合法 providers。
3. 用户级配置不存在时忽略。
4. 用户级配置存在但格式错误或校验失败时返回错误。
5. `providers` 使用项目级配置，不合并用户级 providers。
6. `mcp_servers` 先加载用户级，再用项目级同名 server 整体覆盖。
7. 添加测试覆盖用户级缺失、项目级覆盖同名 server、不同名 server 合并。

**验证：** 运行 `go test ./internal/config -run TestLoadMerged`，合并结果符合优先级。

## T4: 实现 MCP 环境变量展开与脱敏错误

**文件：** `src/internal/config/config.go`、`src/internal/config/config_test.go`

**依赖：** T3

**步骤：**

1. 对 MCP stdio `env` 的值支持 `${VAR}` 展开。
2. 对 MCP http `headers` 的值支持 `${VAR}` 展开。
3. 展开失败时返回包含变量名和字段位置的错误。
4. 错误信息不得包含 header 原始值、展开后的敏感值或完整 token。
5. 添加测试覆盖成功展开、缺失变量、错误信息脱敏。

**验证：** 运行 `go test ./internal/config -run TestExpandMCPEnvAndHeaders`，变量展开和脱敏行为符合预期。

## T5: 定义 MCP 协议与 Transport 基础类型

**文件：** `src/internal/mcp/types.go`、`src/internal/mcp/transport.go`

**依赖：** 无

**步骤：**

1. 新建 `internal/mcp` 包。
2. 定义 MCP initialize、tools/list、tools/call 所需的 params/result/content/tool 类型。
3. 定义 `Transport` 接口，包含 `Start`、`Request`、`Close`。
4. 定义可复用的阶段名或错误包装辅助，便于 manager 输出 `config/start/initialize/list_tools/register`。
5. 保持类型只覆盖 Phase 6 tools 能力，不引入 resources、prompts、sampling。

**验证：** 运行 `go test ./internal/mcp`，新包编译通过。

## T6: 实现 JSON-RPC 编解码辅助

**文件：** `src/internal/mcp/jsonrpc.go`、`src/internal/mcp/jsonrpc_test.go`

**依赖：** T5

**步骤：**

1. 定义 `JSONRPCRequest`、`JSONRPCResponse`、`JSONRPCError`。
2. 实现 `NewRequest(id, method, params)`，固定 `jsonrpc` 为 `2.0`。
3. 实现 response 解码，保留 `Result` 为 `json.RawMessage`。
4. 实现 JSON-RPC error 转 Go error，包含 code 和 message。
5. 实现 result 解码辅助，用于 transport 将 response result 解入调用方结构。
6. 添加测试覆盖成功 response、error response、id 保留、非法 JSON。

**验证：** 运行 `go test ./internal/mcp -run TestJSONRPC`，JSON-RPC 行为正确。

## T7: 实现 MCP Client 生命周期

**文件：** `src/internal/mcp/client.go`、`src/internal/mcp/client_test.go`

**依赖：** T5、T6

**步骤：**

1. 实现 `NewClient(name, transport)`。
2. 实现 `Start(ctx)`，转调 transport start。
3. 实现 `Initialize(ctx)`，发送 `initialize`。
4. 实现 `ListTools(ctx)`，发送 `tools/list` 并返回工具列表。
5. 实现 `CallTool(ctx, name, args)`，发送 `tools/call`。
6. 实现 `Close()`，关闭 transport。
7. 使用 fake transport 测试方法名、参数、result 解码和错误传播。

**验证：** 运行 `go test ./internal/mcp -run TestClient`，client 生命周期测试通过。

## T8: 实现 HTTP Transport

**文件：** `src/internal/mcp/http.go`、`src/internal/mcp/http_test.go`

**依赖：** T6

**步骤：**

1. 实现 HTTP transport 构造函数，接收 server name、url、headers 和 http client。
2. `Start(ctx)` 对 HTTP transport 作为轻量 no-op 或基础校验。
3. `Request(ctx, method, params, result)` 构造 JSON-RPC request 并 POST 到配置 url。
4. 注入配置 headers。
5. 校验 HTTP status 和 JSON-RPC response id。
6. 将 JSON-RPC error、HTTP 错误、解码错误转换成可读错误。
7. 使用 `httptest.Server` 覆盖成功请求、header 注入、错误 response、id 不匹配、非 2xx。

**验证：** 运行 `go test ./internal/mcp -run TestHTTPTransport`，HTTP transport 测试通过。

## T9: 添加 stdio 测试 Server

**文件：** `src/internal/mcp/testdata/stdio_server.go`

**依赖：** T6

**步骤：**

1. 新建测试用 Go 程序，从 stdin 按行读取 JSON-RPC request。
2. 对 `initialize` 返回基本 server info。
3. 对 `tools/list` 返回至少一个测试工具。
4. 对 `tools/call` 回显参数或返回固定文本。
5. 支持通过参数或环境变量触发错误响应、延迟响应和提前退出，方便 stdio 测试覆盖异常分支。

**验证：** 在 `src/internal/mcp` 测试中通过 `go run ./testdata/stdio_server.go` 能完成 initialize/list/call；最终验证归入 T10。

## T10: 实现 stdio Transport

**文件：** `src/internal/mcp/stdio.go`、`src/internal/mcp/stdio_test.go`

**依赖：** T6、T9

**步骤：**

1. 实现 stdio transport 构造函数，接收 name、command、args、env。
2. `Start(ctx)` 启动子进程并建立 stdin/stdout。
3. 启动 reader goroutine，持续读取 stdout JSON-RPC response。
4. `Request` 生成 id，写入 stdin，注册 pending，等待对应 id response。
5. ctx 取消时删除 pending 并返回取消错误。
6. 子进程退出、stdout 解码失败或 transport 关闭时唤醒 pending 请求。
7. `Close` 关闭 stdin/stdout，终止或等待子进程，清理 pending。
8. 测试成功请求、并发请求 id 配对、ctx 取消、server 退出、Close 清理。

**验证：** 运行 `go test ./internal/mcp -run TestStdioTransport`，stdio transport 测试通过且不残留测试进程。

## T11: 实现 RemoteTool 适配

**文件：** `src/internal/mcp/adapter.go`、`src/internal/mcp/adapter_test.go`

**依赖：** T7

**步骤：**

1. 实现 MCP 工具注册名构造：`sanitize(server) + "." + sanitize(tool)`。
2. 实现工具名合法化，非法字符替换为 `_`，结果为空时报错。
3. 实现 input schema 标准化：缺失时使用空 object schema，非法 schema 返回错误。
4. 实现 `RemoteTool` 的 `Name`、`Description`、`Schema`、`Timeout`。
5. 实现 `Execute`，调用 `Client.CallTool` 并转换结果。
6. text content 拼接为输出；非 text content 降级为类型描述。
7. MCP `isError=true` 或调用错误时返回 `tools.Result{IsError:true}`。
8. 添加测试覆盖命名、schema、成功结果、MCP 错误结果、transport 错误。

**验证：** 运行 `go test ./internal/mcp -run TestRemoteTool`，适配层行为正确。

## T12: 实现 MCP Safety 判定

**文件：** `src/internal/mcp/adapter.go`、`src/internal/mcp/adapter_test.go`

**依赖：** T11

**步骤：**

1. 实现 `SafetyForMCPTool(serverCfg, remoteToolName)`。
2. 默认返回 `tools.SafetySideEffect`。
3. `server.read_only=true` 时返回 `SafetyReadOnly`。
4. `tools[remoteToolName].read_only` 显式设置时覆盖 server 默认。
5. 确认 MCP annotations 不参与判定。
6. 添加测试覆盖默认 side effect、server read_only、tool override true、tool override false。

**验证：** 运行 `go test ./internal/mcp -run TestSafetyForMCPTool`，Safety 判定符合 spec。

## T13: 增加 Registry 冲突检查能力

**文件：** `src/internal/tools/registry.go`、`src/internal/tools/registry_test.go`

**依赖：** 无

**步骤：**

1. 增加一个不破坏现有调用的工具名存在性方法，例如 `Has(name string) bool`。
2. 保持 `RegisterWithSafety` 现有签名和重复注册跳过语义不变。
3. 为 `Has` 添加测试。
4. 确认现有 Registry 测试仍通过。

**验证：** 运行 `go test ./internal/tools -run TestRegistry`，Registry 测试通过。

## T14: 实现 MCP Manager 发现流程

**文件：** `src/internal/mcp/manager.go`、`src/internal/mcp/manager_test.go`

**依赖：** T7、T8、T10

**步骤：**

1. 实现 `Manager`、`ServerSession`、`DiscoverResult`、`ServerError`。
2. 实现 transport 工厂，根据 `config.MCPConfig.Type` 创建 stdio 或 HTTP transport。
3. `Discover(ctx)` 遍历所有 server，逐个 start、initialize、list tools。
4. 成功 server 写入 sessions。
5. 失败 server 写入 errors，并关闭已启动的 client。
6. 空 tools server 作为成功 session，不注册工具。
7. 测试使用 fake transport 或 fake client 覆盖成功、多 server 隔离、initialize 失败、list_tools 失败、空工具列表。

**验证：** 运行 `go test ./internal/mcp -run TestManagerDiscover`，发现流程和失败隔离正确。

## T15: 实现 MCP 工具注册流程

**文件：** `src/internal/mcp/manager.go`、`src/internal/mcp/manager_test.go`

**依赖：** T11、T12、T13、T14

**步骤：**

1. 实现 `RegisterTools(registry, discoverResult)`。
2. 遍历成功 sessions，将每个 MCP tool 包装为 `RemoteTool`。
3. 使用 `server.tool` 名称注册到 Registry。
4. 使用 `SafetyForMCPTool` 注册正确 Safety。
5. 注册名前检查 Registry 是否已有同名工具，冲突时记录 register error 并跳过该工具。
6. 测试 MCP 工具出现在 `Registry.List` 和 `ToToolDefinitions`。
7. 测试冲突工具被跳过并记录错误。

**验证：** 运行 `go test ./internal/mcp -run TestManagerRegisterTools`，注册流程正确。

## T16: 补充 Agent Plan Mode 的 MCP Safety 测试

**文件：** `src/internal/agent/scheduler_test.go`

**依赖：** T12

**步骤：**

1. 使用 fake tool 注册名模拟 `github.search_issues`。
2. 将该工具以 `tools.SafetySideEffect` 注册，验证 Plan Mode 下被拒绝。
3. 将该工具以 `tools.SafetyReadOnly` 注册，验证 Plan Mode 下允许执行。
4. 确认这不需要 Agent 增加 MCP 专用逻辑，只依赖 Registry Safety。

**验证：** 运行 `go test ./internal/agent -run TestSchedulerMCPToolSafetyInPlanMode`，Plan Mode 行为符合 spec。

## T17: main 启动期接入 MCP Manager

**文件：** `src/cmd/onecode/main.go`

**依赖：** T3、T14、T15

**步骤：**

1. 计算用户级配置路径 `~/.onecode/config.yaml`。
2. 将现有 `config.Load(".onecode/config.yaml")` 改为 `config.LoadMerged(userPath, ".onecode/config.yaml")`。
3. 保持内置 6 个工具注册顺序不变。
4. 在创建 TUI 前创建 MCP Manager。
5. 使用带超时的启动 context 执行 `Discover`。
6. 将发现到的 MCP 工具注册到 Registry。
7. 将 Discover/Register warning 打印到 stderr，继续启动。
8. 确保程序退出时调用 `manager.Close()`。

**验证：** 运行 `go test ./cmd/onecode`，main 包编译通过。

## T18: 验证 MCP 工具进入 LLM 工具定义

**文件：** `src/internal/mcp/manager_test.go` 或 `src/internal/tools/registry_test.go`

**依赖：** T15

**步骤：**

1. 构造包含 description 和 inputSchema 的 MCP tool。
2. 注册到 Registry。
3. 调用 `Registry.ToToolDefinitions()`。
4. 断言工具名为 `server.tool`。
5. 断言 description 和 schema 保留。

**验证：** 运行 `go test ./internal/mcp -run TestMCPToolDefinitions`，LLM 可见定义正确。

## T19: 验证取消和超时传播

**文件：** `src/internal/mcp/stdio_test.go`、`src/internal/mcp/http_test.go`、`src/internal/mcp/adapter_test.go`

**依赖：** T8、T10、T11

**步骤：**

1. HTTP transport 测试 ctx 取消时请求返回取消错误。
2. stdio transport 测试 pending 请求在 ctx 取消后被移除。
3. RemoteTool 测试 client 调用超时或取消时返回 `IsError=true`。
4. 确认错误结果不 panic、不阻塞。

**验证：** 运行 `go test ./internal/mcp -run "Test.*Cancel|Test.*Timeout"`，取消和超时测试通过。

## T20: 运行全量测试和格式化

**文件：** 全项目

**依赖：** T1-T19

**步骤：**

1. 运行 `gofmt` 格式化新增和修改的 Go 文件。
2. 运行 `go test ./internal/config`。
3. 运行 `go test ./internal/mcp`。
4. 运行 `go test ./internal/tools`。
5. 运行 `go test ./internal/agent`。
6. 运行 `go test ./...`。
7. 运行 `git diff --check` 检查空白问题。

**验证：** 所有测试和 diff 检查通过。

## 执行顺序

```text
T1 -> T2 -> T3 -> T4
                \
T5 -> T6 -> T7 -> T8
              \      \
               \      -> T9 -> T10
                \
                 -> T11 -> T12

T13 ----------------------\
T14 -----------------------> T15 -> T18
T12 ----------------------/

T15 -> T16
T15 -> T17
T8/T10/T11 -> T19

T1-T19 -> T20
```

## 阶段验证重点

- 配置层先完成，避免 MCP Manager 后续依赖不稳定配置结构。
- JSON-RPC 和 Client 先用 fake transport 验证，再实现真实 transport。
- stdio 与 HTTP transport 分开验证，避免两个传输的问题互相污染。
- RemoteTool 和 Safety 判定先独立测试，再接入 Manager。
- Agent 不增加 MCP 专用分支，只通过 Registry Safety 验证 Plan Mode。
- main 只做启动编排，不承载 MCP 协议细节。

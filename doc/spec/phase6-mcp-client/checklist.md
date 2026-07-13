# Phase 6 MCP Client Checklist

> 每一项都应通过运行代码、观察输出或检查测试结果验证。Checklist 聚焦系统行为，而不是具体实现细节。

## 配置加载

- [ ] 用户级和项目级配置都可以声明 `mcp_servers`。（验证：运行 `go test ./internal/config -run TestLoadMerged`）
- [ ] 用户级配置不存在时不影响项目级配置加载。（验证：运行 `go test ./internal/config -run TestLoadMergedMissingUserConfig`）
- [ ] 用户级配置存在但格式错误时启动配置加载失败并给出清晰错误。（验证：运行 `go test ./internal/config -run TestLoadMergedBadUserConfig`）
- [ ] 同名 MCP Server 由项目级配置整体覆盖用户级配置。（验证：运行 `go test ./internal/config -run TestLoadMergedMCPServerOverride`）
- [ ] 不同名 MCP Server 会从用户级和项目级合并保留。（验证：运行 `go test ./internal/config -run TestLoadMergedMCPServerUnion`）
- [ ] 项目级 providers 继续作为最终 provider 配置，不被用户级 providers 合并覆盖。（验证：运行 `go test ./internal/config -run TestLoadMergedProvidersFromProject`）
- [ ] stdio MCP Server 缺少 `command` 会被拒绝。（验证：运行 `go test ./internal/config -run TestLoadMCPConfigValidation`）
- [ ] HTTP MCP Server 缺少 `url` 会被拒绝。（验证：运行 `go test ./internal/config -run TestLoadMCPConfigValidation`）
- [ ] MCP `type` 不是 `stdio` 或 `http` 时会被拒绝。（验证：运行 `go test ./internal/config -run TestLoadMCPConfigValidation`）
- [ ] stdio `env` 和 HTTP `headers` 支持 `${VAR}` 展开。（验证：运行 `go test ./internal/config -run TestExpandMCPEnvAndHeaders`）
- [ ] 环境变量缺失时错误中包含变量名和字段位置。（验证：运行 `go test ./internal/config -run TestExpandMCPEnvMissingVar`）
- [ ] 环境变量展开后的敏感值不会出现在错误信息中。（验证：运行 `go test ./internal/config -run TestExpandMCPEnvDoesNotLeakSecret`）

## JSON-RPC 与协议生命周期

- [ ] JSON-RPC request 固定携带 `jsonrpc:"2.0"`、唯一 id、method 和 params。（验证：运行 `go test ./internal/mcp -run TestJSONRPCRequest`）
- [ ] JSON-RPC response 能按 id 与请求关联。（验证：运行 `go test ./internal/mcp -run TestJSONRPCResponseID`）
- [ ] JSON-RPC error response 会转换成可读错误。（验证：运行 `go test ./internal/mcp -run TestJSONRPCErrorResponse`）
- [ ] MCP Client 能发送 `initialize` 并解析初始化结果。（验证：运行 `go test ./internal/mcp -run TestClientInitialize`）
- [ ] MCP Client 能发送 `tools/list` 并解析工具列表。（验证：运行 `go test ./internal/mcp -run TestClientListTools`）
- [ ] MCP Client 能发送 `tools/call` 并解析调用结果。（验证：运行 `go test ./internal/mcp -run TestClientCallTool`）
- [ ] Client 或 Transport 收到协议错误时返回错误，不 panic。（验证：运行 `go test ./internal/mcp -run TestClientProtocolError`）

## stdio Transport

- [ ] stdio transport 可以启动本地 MCP 子进程并完成 initialize/list/call。（验证：运行 `go test ./internal/mcp -run TestStdioTransport`）
- [ ] stdio 子进程能收到配置中的 command、args、env。（验证：运行 `go test ./internal/mcp -run TestStdioTransportEnvAndArgs`）
- [ ] stdio transport 能处理并发请求，并按 JSON-RPC id 分发响应。（验证：运行 `go test ./internal/mcp -run TestStdioTransportConcurrentRequests`）
- [ ] stdio 请求被 context 取消时会删除 pending 请求并返回取消错误。（验证：运行 `go test ./internal/mcp -run TestStdioTransportCancel`）
- [ ] stdio 子进程提前退出时，等待中的请求会收到错误。（验证：运行 `go test ./internal/mcp -run TestStdioTransportProcessExit`）
- [ ] stdio transport Close 后不会残留测试子进程。（验证：运行 `go test ./internal/mcp -run TestStdioTransportClose`，并确认测试不挂起）

## Streamable HTTP Transport

- [ ] HTTP transport 可以通过 POST 发送 JSON-RPC 请求并解析响应。（验证：运行 `go test ./internal/mcp -run TestHTTPTransportRequest`）
- [ ] HTTP transport 会携带配置展开后的 headers。（验证：运行 `go test ./internal/mcp -run TestHTTPTransportHeaders`）
- [ ] HTTP transport 会校验 response id 与 request id 一致。（验证：运行 `go test ./internal/mcp -run TestHTTPTransportResponseIDMismatch`）
- [ ] HTTP 非 2xx 响应会转换成可读错误。（验证：运行 `go test ./internal/mcp -run TestHTTPTransportStatusError`）
- [ ] HTTP JSON-RPC error response 会转换成工具调用错误。（验证：运行 `go test ./internal/mcp -run TestHTTPTransportJSONRPCError`）
- [ ] HTTP 请求被 context 取消时能尽快返回取消错误。（验证：运行 `go test ./internal/mcp -run TestHTTPTransportCancel`）

## MCP 工具适配

- [ ] MCP tool 会注册成 `server.tool` 形式。（验证：运行 `go test ./internal/mcp -run TestRemoteToolName`）
- [ ] server name 和 remote tool name 中的非法字符会被安全替换或拒绝。（验证：运行 `go test ./internal/mcp -run TestRemoteToolNameSanitize`）
- [ ] MCP tool description 会保留到 OneCode tool definition。（验证：运行 `go test ./internal/mcp -run TestMCPToolDefinitions`）
- [ ] MCP inputSchema 会保留到 OneCode tool schema。（验证：运行 `go test ./internal/mcp -run TestMCPToolDefinitions`）
- [ ] 缺失 inputSchema 的 MCP tool 会获得空 object schema。（验证：运行 `go test ./internal/mcp -run TestRemoteToolDefaultSchema`）
- [ ] 非法 schema 的 MCP tool 不会被注册，并产生 register warning。（验证：运行 `go test ./internal/mcp -run TestManagerRegisterInvalidSchema`）
- [ ] MCP text content 会转换成普通 `tools.Result.Content`。（验证：运行 `go test ./internal/mcp -run TestRemoteToolTextResult`）
- [ ] 非 text content 会降级成可读类型描述。（验证：运行 `go test ./internal/mcp -run TestRemoteToolNonTextResult`）
- [ ] MCP `isError=true` 会转换成 `tools.Result{IsError:true}`。（验证：运行 `go test ./internal/mcp -run TestRemoteToolMCPErrorResult`）
- [ ] Transport 或 Client 调用失败会转换成 `tools.Result{IsError:true}`。（验证：运行 `go test ./internal/mcp -run TestRemoteToolCallError`）

## 多 Server 管理

- [ ] MCP Manager 可以发现多个 Server 并分别保存 session。（验证：运行 `go test ./internal/mcp -run TestManagerDiscoverMultipleServers`）
- [ ] 单个 Server 启动失败不影响其他 Server 发现工具。（验证：运行 `go test ./internal/mcp -run TestManagerDiscoverStartFailureIsolation`）
- [ ] 单个 Server initialize 失败不影响其他 Server 发现工具。（验证：运行 `go test ./internal/mcp -run TestManagerDiscoverInitializeFailureIsolation`）
- [ ] 单个 Server tools/list 失败不影响其他 Server 发现工具。（验证：运行 `go test ./internal/mcp -run TestManagerDiscoverListToolsFailureIsolation`）
- [ ] tools/list 返回空列表的 Server 不算致命失败。（验证：运行 `go test ./internal/mcp -run TestManagerDiscoverEmptyTools`）
- [ ] MCP 工具注册冲突时跳过冲突工具并记录错误。（验证：运行 `go test ./internal/mcp -run TestManagerRegisterToolConflict`）
- [ ] Discover/Register warning 不包含展开后的敏感 header 或 env 值。（验证：运行 `go test ./internal/mcp -run TestManagerErrorsDoNotLeakSecrets`）

## 权限与 Plan Mode

- [ ] MCP 工具默认注册为 `SafetySideEffect`。（验证：运行 `go test ./internal/mcp -run TestSafetyForMCPToolDefault`）
- [ ] server-level `read_only: true` 会让该 Server 的 MCP 工具注册为 `SafetyReadOnly`。（验证：运行 `go test ./internal/mcp -run TestSafetyForMCPToolServerReadOnly`）
- [ ] tool-level `read_only` 可以覆盖 server-level 默认值。（验证：运行 `go test ./internal/mcp -run TestSafetyForMCPToolOverride`）
- [ ] MCP annotations 不会自动改变 Safety。（验证：运行 `go test ./internal/mcp -run TestSafetyIgnoresAnnotations`）
- [ ] SideEffect MCP 工具在 Plan Mode 下不可执行。（验证：运行 `go test ./internal/agent -run TestSchedulerMCPToolSafetyInPlanMode`）
- [ ] ReadOnly MCP 工具在 Plan Mode 下可执行。（验证：运行 `go test ./internal/agent -run TestSchedulerMCPToolSafetyInPlanMode`）
- [ ] MCP 工具仍然通过 Phase 5 permission.Manager 做执行前权限判断。（验证：运行 `go test ./internal/agent -run TestSchedulerPermission`，并新增 MCP-style 工具权限测试）

## 启动集成

- [ ] main 使用用户级 + 项目级合并配置启动。（验证：运行 `go test ./cmd/onecode` 编译通过，并检查 main 调用 `config.LoadMerged`）
- [ ] main 在创建 TUI 前完成内置工具和 MCP 工具注册。（验证：使用启动集成测试或人工检查启动日志）
- [ ] MCP Discover/Register 的非致命 warning 打印后程序继续启动。（验证：配置一个失败 MCP Server 和一个可用 Server，观察可用工具仍注册）
- [ ] OneCode 退出时调用 MCP Manager Close。（验证：运行 main 相关测试或通过 mock manager 验证 Close 被调用）
- [ ] MCP 接入不改变 6 个内置工具的注册名。（验证：运行 `go test ./internal/tools ./internal/agent`）
- [ ] MCP 接入不改变 6 个内置工具的 schema 和成功结果格式。（验证：运行 `go test ./internal/tools`）

## 编译与测试

- [ ] 配置模块测试通过。（验证：运行 `go test ./internal/config`）
- [ ] MCP 模块测试通过。（验证：运行 `go test ./internal/mcp`）
- [ ] 工具注册中心测试通过。（验证：运行 `go test ./internal/tools`）
- [ ] Agent 调度测试通过。（验证：运行 `go test ./internal/agent`）
- [ ] 主程序包编译通过。（验证：运行 `go test ./cmd/onecode`）
- [ ] 全项目测试通过。（验证：运行 `go test ./...`）
- [ ] Go 文件已格式化。（验证：运行 `gofmt` 后 `git diff --check` 无输出）

## 端到端场景

- [ ] 场景 1：配置一个测试 stdio MCP Server，启动发现工具后，Registry 中出现 `test.echo`。（验证：运行包含 fake stdio server 的集成测试）
- [ ] 场景 2：Agent 调用 `test.echo`，MCP Server 返回文本，模型收到对应 tool result。（验证：运行 mock provider + MCP RemoteTool 集成测试）
- [ ] 场景 3：配置两个 MCP Server，其中一个启动失败，另一个正常注册工具。（验证：运行 manager 多 Server 集成测试）
- [ ] 场景 4：配置 HTTP MCP Server，headers 使用 `${TEST_TOKEN}`，请求能携带 header，错误输出不泄露 token 值。（验证：运行 httptest 集成测试）
- [ ] 场景 5：Plan Mode 下未显式 read-only 的 MCP 工具不可见或不可执行，显式 read-only 的 MCP 工具可执行。（验证：运行 Agent Plan Mode 集成测试）
- [ ] 场景 6：正在等待的 MCP 工具调用被取消时，Agent 收到 `IsError=true` 工具结果且 loop 不崩溃。（验证：运行取消集成测试）

## 范围边界

- [ ] 未实现 MCP resources。（验证：检查 `internal/mcp` 没有 resources/list 或 resources/read 调用入口）
- [ ] 未实现 MCP prompts。（验证：检查 `internal/mcp` 没有 prompts/list 或 prompts/get 调用入口）
- [ ] 未实现 MCP sampling。（验证：检查 `internal/mcp` 没有 sampling 入口）
- [ ] 未实现 Server 健康检查和自动重连。（验证：检查没有后台 health check/reconnect loop）
- [ ] 未实现 MCP Server 安装、下载、升级或包管理。（验证：检查 main 和 mcp 包没有安装器逻辑）
- [ ] 未实现 OAuth、系统钥匙串或独立密钥管理。（验证：检查配置只支持 `${VAR}` 展开）
- [ ] 未持久化 MCP 请求、响应或权限审计日志。（验证：检查没有 MCP audit log 写入路径）
- [ ] 未把 MCP annotations 当作自动 read-only 或自动放权依据。（验证：运行 `go test ./internal/mcp -run TestSafetyIgnoresAnnotations`）

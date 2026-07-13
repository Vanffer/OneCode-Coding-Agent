# MCP Client Implementation Notes

## 阶段目标回顾

Phase 6 的目标，是把 OneCode 从“只能使用内置工具”推进到“可以作为 MCP Client 接入外部工具生态”。

Phase 5 已经完成了统一权限系统：工具执行前会经过黑名单、沙箱、规则、模式和人在回路确认。Phase 6 在这个基础上新增 MCP 工具来源，但不绕开原有工具中心和权限系统。最终链路是：

```text
配置文件声明 MCP Server
  -> OneCode 启动时连接 Server
  -> MCP initialize
  -> MCP tools/list
  -> 将远端 tool 包装成 OneCode Tool
  -> 注册到 tools.Registry
  -> Agent 像调用内置工具一样调用 MCP 工具
  -> MCP tools/call
  -> tool result 回写给 LLM
```

本阶段只实现 MCP tools 能力，不做 resources、prompts、sampling、自动重连、健康检查、OAuth、审计日志和 MCP Server 安装管理。

安全策略选择偏保守：

```text
MCP 工具默认 SafetySideEffect
只有配置显式 read_only 才是 SafetyReadOnly
Plan Mode 只暴露 read-only MCP 工具
MCP annotations 不参与自动放权
MCP 工具仍走 Phase 5 permission.Manager
```

## 主要改动

### 配置系统

`internal/config` 扩展了配置结构：

```go
type Config struct {
	Providers  []ProviderConfig     `yaml:"providers"`
	MCPServers map[string]MCPConfig `yaml:"mcp_servers"`
}
```

新增 MCP Server 配置：

```go
type MCPConfig struct {
	Type     string
	Command  string
	Args     []string
	Env      map[string]string
	URL      string
	Headers  map[string]string
	ReadOnly bool
	Tools    map[string]MCPToolConfig
}
```

`Load(path)` 保持原有单文件加载语义，继续要求 providers 合法。

新增 `LoadMerged(userPath, projectPath)`：

```text
项目级配置必须存在并提供 providers
用户级配置不存在则忽略
用户级配置存在但格式错误则失败
providers 使用项目级配置
mcp_servers 先读用户级，再用项目级同名 server 整体覆盖
```

配置中的 stdio `env` 和 HTTP `headers` 支持 `${VAR}` 展开。缺失变量时只报变量名和字段路径，不回显原始 header、展开后的 token 或半成品密钥。

### MCP 包

新增 `internal/mcp` 包，包含：

```text
types.go      - MCP initialize/tools/list/tools/call 协议结构
jsonrpc.go    - JSON-RPC 2.0 request/response/error/result 解码
transport.go  - Transport 接口与 transport 工厂
http.go       - 请求/响应式 Streamable HTTP transport
stdio.go      - stdio 子进程 transport、pending id 配对
client.go     - 单 Server MCP Client 生命周期封装
adapter.go    - RemoteTool、server.tool 命名、Safety 判定、结果转换
manager.go    - 多 Server 发现、失败隔离、工具注册、关闭
testdata/stdio_server.go - 测试用假 MCP stdio server
```

核心接口是：

```go
type Transport interface {
	Start(ctx context.Context) error
	Request(ctx context.Context, method string, params interface{}, result interface{}) error
	Close() error
}
```

`Client` 只负责单个 Server 的 MCP 生命周期：

```go
Start
Initialize
ListTools
CallTool
Close
```

`Manager` 负责多个 Server：

```go
Discover
RegisterTools
Close
```

### 工具适配

远端 MCP tool 被包装成 `RemoteTool`，实现现有 `tools.Tool` 接口：

```go
Name()
Description()
Schema()
Timeout()
Execute(ctx, args)
```

注册名采用：

```text
server.tool
```

例如：

```text
server name: github
remote tool: search_issues
OneCode tool: github.search_issues
```

这样 Agent、Registry、Provider 都不需要 MCP 专用分支；它们仍然只看到普通工具。

### 启动接入

`cmd/onecode/main.go` 的启动流程变为：

```text
os.Getwd
config.LoadMerged(~/.onecode/config.yaml, .onecode/config.yaml)
注册 6 个内置工具
mcp.NewManager(cfg.MCPServers)
manager.Discover(startupCtx)
manager.RegisterTools(registry, &discoverResult)
打印 MCP warning
defer manager.Close()
tui.New(...)
```

单个 MCP Server 失败只打印 warning，不阻止 OneCode 启动。

### Registry 和 Agent 测试

`tools.Registry` 增加：

```go
Has(name string) bool
```

用于 MCP 工具注册前检查命名冲突，不改变 `RegisterWithSafety` 原有“重复注册跳过”的行为。

Agent 不增加 MCP 专用逻辑，只新增测试验证：

```text
github.write_issue   SafetySideEffect -> Plan Mode 禁用
github.search_issues SafetyReadOnly   -> Plan Mode 可执行
```

## 最终目录结构

Phase 6 后新增和修改的核心文件如下：

```text
src/
├── cmd/onecode/
│   └── main.go
├── internal/
│   ├── agent/
│   │   └── scheduler_test.go
│   ├── config/
│   │   ├── config.go
│   │   └── config_test.go
│   ├── mcp/
│   │   ├── adapter.go
│   │   ├── adapter_test.go
│   │   ├── client.go
│   │   ├── client_test.go
│   │   ├── http.go
│   │   ├── http_test.go
│   │   ├── jsonrpc.go
│   │   ├── jsonrpc_test.go
│   │   ├── manager.go
│   │   ├── manager_test.go
│   │   ├── stdio.go
│   │   ├── stdio_test.go
│   │   ├── transport.go
│   │   ├── types.go
│   │   └── testdata/
│   │       └── stdio_server.go
│   └── tools/
│       ├── registry.go
│       └── registry_test.go
└── doc/spec/phase6-mcp-client/
    ├── spec.md
    ├── plan.md
    ├── task.md
    ├── checklist.md
    └── implementation-notes.md
```

依赖方向保持为：

```text
config -> 标准库 + yaml
mcp    -> config + tools + 标准库
tools  -> llm
agent  -> tools + permission + llm
cmd    -> config + mcp + tools + tui
```

`mcp` 不依赖 Agent、TUI 或 provider SDK；它只负责协议、连接和工具适配。

## 架构、数据流与状态变化

### 1. 配置合并数据流

启动时配置流如下：

```text
main
  -> userConfigPath()
  -> config.LoadMerged(userPath, ".onecode/config.yaml")
       -> load project config
       -> validate project providers
       -> load user config if exists
       -> validate user MCP config
       -> merge user/project mcp_servers
       -> expand ${VAR} in env/headers
  -> Config{Providers, MCPServers}
```

这里有一个有意保留的边界：

```text
providers 不做用户级/项目级合并
mcp_servers 做用户级/项目级合并
```

原因是 Phase 6 的目标是 MCP，不扩大 provider 配置语义。项目级 providers 仍然是最终 provider 来源。

### 2. 凭据展开数据流

MCP 配置中可以写：

```yaml
mcp_servers:
  github:
    type: http
    url: https://example.com/mcp
    headers:
      Authorization: "Bearer ${GITHUB_TOKEN}"
```

加载时：

```text
"Bearer ${GITHUB_TOKEN}"
  -> os.LookupEnv("GITHUB_TOKEN")
  -> "Bearer actual-token"
  -> 只保存在运行时 Config 内存中
```

如果变量缺失，错误类似：

```text
配置错误: mcp_servers.github.headers.Authorization 引用的环境变量 GITHUB_TOKEN 未设置
```

错误不会包含：

```text
Bearer
actual-token
header 原始完整值
```

这不是完整密钥管理系统，只是避免密钥明文落到配置文件和错误输出里。

### 3. Server 发现数据流

每个 MCP Server 独立发现：

```text
Manager.Discover
  -> 按 server name 排序遍历
  -> NewTransport(name, cfg)
  -> NewClient(name, transport)
  -> client.Start
  -> client.Initialize
  -> client.ListTools
  -> ServerSession{Name, Config, Client, Tools}
```

失败隔离策略：

```text
transport 创建失败 -> ServerError{StageConfig}
start 失败          -> ServerError{StageStart}
initialize 失败     -> ServerError{StageInitialize}
tools/list 失败     -> ServerError{StageListTools}
tools 为空          -> 成功 session，注册 0 个工具
```

`Discover` 不直接注册工具，只返回：

```go
type DiscoverResult struct {
	Sessions []*ServerSession
	Errors   []ServerError
}
```

这样发现和注册可以分开测试，也便于后续把 warning 展示到 TUI。

### 4. 工具注册数据流

`RegisterTools` 把发现到的 MCP 工具接入 Registry：

```text
DiscoverResult.Sessions
  -> session.Tools
  -> RemoteToolName(server, tool)
  -> registry.Has(name) 检查冲突
  -> NewRemoteTool(...)
  -> SafetyForMCPTool(session.Config, remote.Name)
  -> registry.RegisterWithSafety(remoteTool, safety)
```

工具定义流向 LLM 的路径没有变化：

```text
RemoteTool 实现 tools.Tool
  -> Registry.ToToolDefinitions()
  -> []llm.ToolDefinition
  -> provider.Stream(..., tools)
```

因此 OpenAI/Anthropic 适配器无需知道 MCP。

### 5. Agent 调用 MCP 工具的数据流

模型看到工具名：

```text
github.search
```

调用时仍走 Phase 3/5 的普通工具链路：

```text
LLM ToolCall{Name:"github.search", Input:{...}}
  -> Agent Scheduler
  -> registry.Safety("github.search")
  -> Plan Mode safety check
  -> permission.Manager.Resolve
  -> registry.Execute
  -> RemoteTool.Execute
  -> Client.CallTool(remoteName, args)
  -> Transport.Request("tools/call", ...)
  -> tools.Result
  -> llm.ToolResult
  -> Conversation
```

这里有两个关键点：

```text
Plan Mode 不知道 MCP，只看 Safety
Permission 不知道 MCP，只看 tool name + args + Safety
```

MCP 通过工具适配层融入已有系统，而不是开新通道。

### 6. stdio 请求状态变化

stdio transport 是状态最多的部分。内部维护：

```go
cmd      *exec.Cmd
stdin    io.WriteCloser
stdout   io.ReadCloser
nextID   int64
pending  map[int64]chan rpcEnvelope
closed   bool
```

一次请求：

```text
Request
  -> registerPending
       nextID++
       pending[id] = ch
  -> json.Marshal(JSONRPCRequest)
  -> stdin.Write(line)
  -> select:
       response channel -> DecodeResult
       ctx.Done         -> removePending + return ctx error
```

后台 reader：

```text
stdout line
  -> DecodeResponse
  -> deliver(resp)
       pending[resp.ID] -> ch <- resp
       delete pending[resp.ID]
```

Close / 子进程退出 / 解码失败：

```text
failPending(err)
  -> closed = true
  -> pending map 清空
  -> 所有等待中的请求收到错误
```

这满足 JSON-RPC 的“请求 id 与响应 id 配对”要求。

### 7. HTTP 请求状态变化

HTTP transport 状态相对简单：

```go
url
headers
client
nextID
```

每次 `Request`：

```text
id = atomic.AddInt64
NewRequest(id, method, params)
POST url
设置 Content-Type / Accept / headers
读取 response body
DecodeResponse
校验 response.ID == request.ID
DecodeResult
```

本阶段实现的是请求/响应式 Streamable HTTP。没有实现 SSE 长连接、server push、last-event-id 恢复或自动重连。

### 8. Safety 状态变化

MCP Safety 判定集中在：

```go
SafetyForMCPTool(serverCfg, remoteToolName)
```

规则：

```text
默认 side_effect
server.read_only=true -> read_only
tools[tool].read_only 显式设置 -> 覆盖 server 默认
annotations -> 忽略
```

例如：

```yaml
mcp_servers:
  github:
    type: http
    url: https://example.com/mcp
    read_only: true
    tools:
      create_issue:
        read_only: false
```

则：

```text
github.search       -> SafetyReadOnly
github.create_issue -> SafetySideEffect
```

## 关键实现细节

### config.go：Load 与 LoadMerged

`Load(path)` 仍然用于单配置文件：

```text
read file
yaml.Unmarshal
validate providers
validate MCP servers
expand MCP env/headers
```

`LoadMerged(userPath, projectPath)` 用于主程序启动：

```text
project config 必须成功加载
project providers 必须合法
user config 不存在则忽略
user config 存在但错误则失败
MCP servers 合并
```

这里修过一个细节：`loadFile` 会包装 `os.ReadFile` 错误，所以判断用户配置不存在时不能用 `os.IsNotExist(err)`，而要用：

```go
errors.Is(err, os.ErrNotExist)
```

否则包装后的 not exist 会被误判成致命错误。

### jsonrpc.go：通用协议层

JSON-RPC 结构只表达通用协议：

```go
JSONRPCRequest
JSONRPCResponse
JSONRPCError
```

`DecodeResponse` 不直接知道 MCP 业务类型，只保留：

```go
Result json.RawMessage
```

调用方再用 `DecodeResult(resp, &result)` 解成 `InitializeResult`、`ListToolsResult` 或 `CallToolResult`。

这样 JSON-RPC 层和 MCP tool 层解耦。

### client.go：单 Server 生命周期

`Client` 是对单个 Server 的薄封装：

```go
Start      -> transport.Start
Initialize -> transport.Request("initialize")
ListTools  -> transport.Request("tools/list")
CallTool   -> transport.Request("tools/call")
Close      -> transport.Close
```

它不管理多个 Server，也不注册工具。这个职责留给 `Manager`。

### http.go：HTTP transport

HTTP transport 的主要保护：

- 请求带 `Content-Type: application/json`。
- 请求带 `Accept: application/json`。
- 注入配置 headers。
- 响应 body 限制为 16MB。
- 非 2xx 返回错误。
- response id 必须匹配 request id。
- JSON-RPC error 转 Go error。

当前错误信息不打印 header 值，因此不会主动泄露展开后的敏感值。

### stdio.go：stdio transport

stdio transport 使用：

```go
exec.Command(command, args...)
cmd.StdinPipe()
cmd.StdoutPipe()
```

子进程环境：

```text
os.Environ()
+ 配置 env map
```

这意味着配置 env 会覆盖同名环境变量。

读写分离：

- `Request` 负责写 stdin 并等待 pending response。
- `readLoop` 负责持续从 stdout 读 JSON 行。
- `cmd.Wait` goroutine 负责感知进程退出并唤醒 pending。

stdout 用 `bufio.Scanner` 按行读取，buffer 上限调到 16MB，适合当前工具结果体量。未来如果要支持超大或分片消息，可以改成更底层的 reader。

### adapter.go：RemoteTool

`RemoteTool` 做 MCP 到 OneCode 的转换：

```text
MCPTool.Name        -> RemoteTool.remoteName
server.tool         -> RemoteTool.name
MCPTool.Description -> Tool.Description
MCPTool.InputSchema -> Tool.Schema
CallToolResult      -> tools.Result
```

工具名合法化：

```text
字母 / 数字 / _ / - / . 保留
空白 -> _
其他非法字符 -> _
首尾的 _ . - trim
空结果 -> 拒绝注册
```

schema 规则：

```text
缺失 schema -> 空 object schema
type 缺失   -> 补 type=object
type 非 object -> 拒绝注册
```

结果转换：

```text
text content     -> 拼接为输出
non-text content -> [MCP 非文本内容: type]
isError=true     -> tools.Result.IsError=true
调用错误         -> tools.Result{IsError:true}
```

### manager.go：失败隔离

`Manager.Discover` 按 server 名称排序执行。排序不是协议要求，但能让测试和启动 warning 顺序稳定。

失败不会 panic，也不会阻断其他 Server：

```text
bad server -> result.Errors append
good server -> result.Sessions append
```

`RegisterTools` 接收 `*DiscoverResult`，注册时产生的 schema 错误或命名冲突继续追加到 `Errors`。这样调用方只需要统一打印 `DiscoverResult.Errors`。

### main.go：启动编排

主程序启动新增：

```go
mcpManager := mcp.NewManager(cfg.MCPServers)
startupCtx, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
discoverResult := mcpManager.Discover(startupCtx)
cancelStartup()
mcpManager.RegisterTools(registry, &discoverResult)
printMCPWarnings(discoverResult.Errors)
defer mcpManager.Close()
```

10 秒是启动发现阶段的兜底，避免某个 MCP Server 卡住启动。单个工具调用仍然走 `RemoteTool.Timeout()` 和 Registry 注入的 context。

warning 当前打印到 stderr：

```text
Warning: MCP server <name> <stage> failed: <err>
```

后续如果希望 TUI 展示 MCP 可用性，可以把这些 warnings 传入 TUI model。

## 测试覆盖

### config

覆盖点：

- 原 provider 配置仍能加载。
- MCP type 校验。
- stdio 缺 command 报错。
- http 缺 url 报错。
- 用户级缺失可忽略。
- 用户级 bad yaml 会失败。
- 项目级 MCP 覆盖用户级同名 Server。
- 用户级和项目级不同名 Server 合并。
- providers 仍来自项目级。
- env / headers `${VAR}` 展开。
- 缺失 env var 报变量名和字段路径。
- 错误不泄露 token 片段。

### mcp/jsonrpc

覆盖点：

- request 包含 `jsonrpc:"2.0"`、id、method。
- response id 保留。
- JSON-RPC error 转 Go error。
- result 解码到业务结构。
- 非法 JSON 返回错误。

### mcp/client

使用 fake transport 验证：

- `Initialize` 调用 `initialize`。
- `ListTools` 调用 `tools/list`。
- `CallTool` 调用 `tools/call`，并传 remote tool name。
- transport 错误正常向上返回。

### mcp/http

使用 `httptest.Server` 覆盖：

- POST JSON-RPC 请求。
- header 注入。
- response id 不匹配。
- HTTP 非 2xx。
- JSON-RPC error。
- context cancel。

### mcp/stdio

使用 `testdata/stdio_server.go` 覆盖：

- 本地子进程启动。
- initialize / tools/list。
- env 和 args 基础路径。
- 并发请求按 id 配对。
- ctx deadline 后请求取消。
- 子进程提前退出。
- Close 后请求失败且测试不挂起。

### mcp/adapter

覆盖点：

- `server.tool` 命名。
- 非法字符替换。
- 空 schema 默认 object。
- description/schema 进入 LLM tool definition。
- text content 转普通输出。
- non-text content 降级展示。
- MCP isError 转 `IsError=true`。
- transport/call error 转 `IsError=true`。
- Safety 默认 side effect。
- server-level read_only。
- tool-level override。
- annotations 不影响 Safety。

### mcp/manager

覆盖点：

- 多 Server 发现。
- start / initialize / tools/list 失败隔离。
- 空 tools Server 不算失败。
- 注册 MCP 工具到 Registry。
- Safety 注册正确。
- 注册名冲突产生 register error。
- 非法 schema 产生 register error。
- error 不包含密钥值。

### tools / agent / cmd

覆盖点：

- Registry `Has`。
- MCP-style 工具名在 Plan Mode 下按 Safety 工作。
- `cmd/onecode` 编译通过。

验证命令：

```powershell
cd src
$env:GOCACHE = Join-Path (Resolve-Path ..).Path ".gocache"
go test ./...
git diff --check
```

当前验证结果：

```text
go test ./...       通过
git diff --check    通过，仅有 Windows CRLF 提示
```

## 设计取舍

### 1. MCP 工具适配成 Tool，而不是 Agent 直接调用 MCP

如果 Agent 直接理解 MCP，会让 Agent 同时承担工具调度、权限、远端协议和连接状态。Phase 6 选择把 MCP tool 包装成现有 `tools.Tool`，这样：

```text
Agent 不知道 MCP
Provider 不知道 MCP
TUI 不知道 MCP
Permission 不知道 MCP
```

MCP 只是新的工具来源。

### 2. 工具名使用 server.tool

不使用原始 MCP tool name，是为了避免：

- 与内置工具重名。
- 多个 MCP Server 暴露同名工具。
- 权限规则无法区分工具来源。

`server.tool` 对模型也更直观，能看出工具来自哪个 Server。

### 3. 默认 side effect

MCP Server 是外部能力，OneCode 不应默认相信远端工具是只读。即使 MCP tool annotations 写了 `readOnlyHint`，本阶段也只保留信息，不作为自动放权依据。

用户想让某个 Server 或 tool 在 Plan Mode 可用，需要显式配置：

```yaml
read_only: true
tools:
  write_something:
    read_only: false
```

这是安全优先的取舍。

### 4. HTTP 先做请求/响应式

MCP Streamable HTTP 可以涉及更复杂的流式和恢复语义。本阶段只需要支撑：

```text
initialize
tools/list
tools/call
```

因此先实现 POST 请求/响应式 JSON-RPC。SSE、server push、last-event-id、自动重连都留给后续。

### 5. Discover 和 Register 分离

`Discover` 只连接 Server 并拿工具列表。

`RegisterTools` 才写 Registry。

这样好处是：

- 单独测试发现失败隔离。
- 单独测试注册冲突和 schema 错误。
- 调用方能统一拿到 discover/register warnings。

### 6. 用户级配置不存在不是错误

用户级配置是默认能力补充，不应要求每台机器都必须存在。

项目级 `.onecode/config.yaml` 仍然是当前 OneCode 的主配置，因此缺失时继续启动失败。

### 7. 配置 env/header 展开失败是致命错误

如果 `${VAR}` 缺失，继续启动可能导致 MCP Server 以错误凭据运行，失败更隐蔽。因此当前选择配置加载阶段直接失败。

单个 Server 启动失败不致命，但配置本身不可解析或变量缺失是致命。

## 当前限制

- 没有实现 MCP resources、prompts、sampling。
- 没有实现 Streamable HTTP 的 SSE 长连接、恢复和 server push。
- 没有自动重连和健康检查。
- 没有 MCP Server 安装、包管理或版本管理。
- 没有 OAuth、系统钥匙串或独立密钥管理。
- 没有持久化 MCP 请求/响应/权限审计日志。
- 没有把 MCP warnings 接入 TUI 状态区，目前只打印 stderr。
- `RemoteTool.Timeout()` 固定为 30 秒，没有 per-server 或 per-tool timeout 配置。
- stdio 消息按行读取，适合当前测试和常见 JSON-RPC framing；如果未来遇到非逐行输出的 Server，需要调整 reader。
- stdio 子进程继承 `os.Environ()` 后叠加配置 env；还没有做环境变量最小化。
- MCP tool schema 目前要求 object schema；更复杂 schema 支持需要后续扩展。
- Manager 当前顺序发现 Server，没有做并发初始化。
- 注册工具冲突时只是 warning 并跳过，不提供自动重命名策略。
- 还没有真实第三方 MCP Server 的人工 smoke，只用 fake transport、httptest 和测试 stdio server 覆盖。

## 复盘要点

Phase 6 的核心价值是让 OneCode 的工具系统从“固定内置集合”变成“可扩展工具中心”：

```text
内置工具
  -> tools.Registry

MCP 工具
  -> RemoteTool adapter
  -> tools.Registry

Agent / Provider / Permission / TUI
  -> 继续使用 Registry 抽象
```

这个阶段最重要的架构判断是：MCP 不应成为 Agent 的特殊分支，而应成为 Tool 的一种实现。

因此最终系统保持了几个关键不变量：

- 所有工具都通过 Registry 注册。
- 所有工具都通过 Registry.Execute 执行。
- 所有工具都带 Safety。
- Plan Mode 只看 Safety。
- Phase 5 权限系统仍然是执行前统一入口。
- 工具结果仍然是 `tools.Result -> llm.ToolResult -> Conversation`。

这样后续扩展 MCP resources、prompts、采样、健康检查、自动重连或权限增强时，都可以在 `internal/mcp` 和配置层逐步推进，而不会破坏 Agent Loop 的主干。

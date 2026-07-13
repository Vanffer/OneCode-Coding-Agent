# Phase 6 MCP Client Plan

## 架构概览

Phase 6 采用“配置合并 + MCP Manager + Transport 抽象 + Tool Adapter”的架构。MCP 不直接侵入 Agent Loop，也不让 Agent 感知远端协议细节，而是在启动期把远端 MCP 工具包装成 OneCode 现有 `tools.Tool` 接口，再注册到 `tools.Registry`。这样 Agent、LLM Provider、TUI 仍然沿用现有工具链路。

整体分层如下：

```text
cmd/onecode
  读取用户级 + 项目级配置
  注册 6 个内置工具
  创建 MCP Manager
  连接 MCP Servers
  发现 MCP tools
  将 MCP tools 注册进 Registry
  创建 TUI / Agent

config
  加载 ~/.onecode/config.yaml
  加载 <project>/.onecode/config.yaml
  合并 providers 与 mcp_servers
  展开 MCP env / headers 中的 ${VAR}
  保证敏感值只进入运行时内存，不写回配置或错误输出

mcp
  MCP Manager
    管理多个 Server 会话
    负责启动、初始化、列工具、关闭

  MCP Client
    负责单个 Server 的协议流程
    提供 Initialize / ListTools / CallTool

  Transport
    StdioTransport：本地子进程 + stdin/stdout
    HTTPTransport：Streamable HTTP 请求/响应

  JSON-RPC
    统一请求/响应结构
    请求 id 分配
    pending 请求配对
    错误响应转换

  Tool Adapter
    将远端 MCP tool 包装为 OneCode tools.Tool
    工具名注册为 server.tool
    调用时转发到对应 MCP Client

tools.Registry
  继续作为唯一工具中心
  内置工具和 MCP 工具都注册到这里
  Safety 决定 Plan Mode 可见性和权限系统输入

agent
  不新增 MCP 专用逻辑
  仍然通过 Registry.Safety 判断工具是否可见
  仍然通过 Registry.Execute 执行工具
  仍然通过 permission.Manager 做执行前权限判断

permission
  不新增 MCP 特权通道
  MCP 工具默认 SafetySideEffect
  配置显式 read_only 的 MCP 工具注册为 SafetyReadOnly
```

启动后的核心链路是：

```text
OneCode 启动
  -> config 加载并合并用户级 / 项目级配置
  -> 注册内置工具
  -> MCP Manager 按配置连接多个 Server
  -> 每个 Server 独立 initialize
  -> 每个 Server 独立 tools/list
  -> 每个 MCP tool 包装成 RemoteTool
  -> Registry.RegisterWithSafety(remoteTool, safety)
  -> TUI / Agent 使用同一个 Registry
```

Agent 调用 MCP 工具时的链路是：

```text
LLM 返回 tool call: github.search_issues
  -> Agent Scheduler 查 Registry.Safety
  -> Plan Mode / Permission 正常判断
  -> Registry.Execute("github.search_issues", args)
  -> RemoteTool.Execute
  -> MCP Client 发送 tools/call
  -> Transport 收发 JSON-RPC
  -> MCP 结果转换成 tools.Result
  -> Agent 把 tool result 回写给 LLM
```

这个设计刻意保持三个边界：

1. Agent 不知道 MCP，只知道工具。
2. Registry 不知道远端协议，只保存 Tool 实例和 Safety。
3. MCP 包不负责权限审批，只提供工具适配；权限仍由 Phase 5 统一处理。

## 核心数据结构与接口

### 配置结构

```go
type Config struct {
	Providers  []ProviderConfig     `yaml:"providers"`
	MCPServers map[string]MCPConfig `yaml:"mcp_servers"`
}

type MCPConfig struct {
	Type     string                   `yaml:"type"`      // "stdio" | "http"
	Command  string                   `yaml:"command"`   // stdio only
	Args     []string                 `yaml:"args"`      // stdio only
	Env      map[string]string        `yaml:"env"`       // stdio only, supports ${VAR}
	URL      string                   `yaml:"url"`       // http only
	Headers  map[string]string        `yaml:"headers"`   // http only, supports ${VAR}
	ReadOnly bool                     `yaml:"read_only"` // server-level default
	Tools    map[string]MCPToolConfig `yaml:"tools"`
}

type MCPToolConfig struct {
	ReadOnly *bool `yaml:"read_only"` // optional tool-level override
}
```

配置层使用同一个 `config.Config` 承载 providers 和 MCP Server。项目级配置覆盖用户级同名 MCP Server；providers 仍以项目级主配置为准，本阶段不强行改变 provider 合并语义，避免牵动既有启动逻辑。

`ReadOnly` 表示整个 Server 默认安全等级。`Tools[toolName].ReadOnly` 用来覆盖单个远端工具。例如 server 默认 side effect，但 `search` 工具可单独声明 read-only。

### MCP 协议类型

```go
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}
```

这组结构只表达 JSON-RPC 2.0 通用消息。MCP 的 initialize、tools/list、tools/call 再在 result/params 层定义专用结构。

```go
type InitializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ClientInfo       ClientInfo             `json:"clientInfo"`
}

type InitializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo       ServerInfo             `json:"serverInfo"`
}

type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
	Annotations map[string]interface{} `json:"annotations,omitempty"`
}

type ListToolsResult struct {
	Tools []MCPTool `json:"tools"`
}

type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content []MCPContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type MCPContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
```

Phase 6 只需要处理 tool 相关字段。`Annotations` 会被保留但不用于自动放权。

### Transport 接口

```go
type Transport interface {
	Start(ctx context.Context) error
	Request(ctx context.Context, method string, params interface{}, result interface{}) error
	Close() error
}
```

`Transport` 屏蔽 stdio 和 HTTP 差异。上层 `Client` 只关心发一个 MCP method 并拿到 result。

stdio Transport 内部维护：

```go
type pendingRequest struct {
	result chan JSONRPCResponse
}

type StdioTransport struct {
	name      string
	command   string
	args      []string
	env       map[string]string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	nextID    int64
	pending   map[int64]chan JSONRPCResponse
	mu        sync.Mutex
	closeOnce sync.Once
}
```

stdio 是真正需要异步 id 配对的地方：写请求和读响应分离，后台 reader 从 stdout 持续读取 JSON-RPC message，再按 id 投递给等待中的请求。

HTTP Transport 内部维护：

```go
type HTTPTransport struct {
	name    string
	url     string
	headers map[string]string
	client  *http.Client
	nextID  int64
}
```

Phase 6 的 Streamable HTTP 先实现请求/响应式调用。每次 `Request` 发一个 JSON-RPC 请求到配置的 url，解析 JSON-RPC 响应。复杂 SSE 流式恢复、server-initiated notification 和自动重连不在本阶段。

### MCP Client

```go
type Client struct {
	name      string
	transport Transport
	tools     []MCPTool
}

func (c *Client) Initialize(ctx context.Context) error
func (c *Client) ListTools(ctx context.Context) ([]MCPTool, error)
func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (CallToolResult, error)
func (c *Client) Close() error
```

`Client` 是单个 MCP Server 的会话封装。它负责 MCP 生命周期方法，不关心 OneCode Registry，也不做权限判断。

### MCP Manager

```go
type Manager struct {
	servers map[string]*ServerSession
}

type ServerSession struct {
	Name   string
	Config MCPConfig
	Client *Client
	Tools  []MCPTool
	Err    error
}

type DiscoverResult struct {
	Sessions []ServerSession
	Errors   []ServerError
}

type ServerError struct {
	Server string
	Stage  string // config | start | initialize | list_tools | register
	Err    error
}
```

`Manager` 负责多个 Server 的启动、发现、关闭和错误收集。它的原则是“尽量注册能用的，记录失败的”。单个 Server 失败只进入 `Errors`，不让整个启动失败，除非配置文件本身无法解析。

### MCP Tool Adapter

```go
type RemoteTool struct {
	name        string // server.tool
	serverName  string
	remoteName  string
	description string
	schema      map[string]interface{}
	client      *Client
	timeout     time.Duration
}

func (t *RemoteTool) Name() string
func (t *RemoteTool) Description() string
func (t *RemoteTool) Schema() map[string]interface{}
func (t *RemoteTool) Timeout() time.Duration
func (t *RemoteTool) Execute(ctx context.Context, args map[string]interface{}) tools.Result
```

`RemoteTool` 是 MCP 到 OneCode 的适配层。它实现现有 `tools.Tool` 接口，所以 Registry 和 Agent 不需要 MCP 专用逻辑。

`Execute` 内部调用：

```text
RemoteTool.Execute(args)
  -> client.CallTool(remoteName, args)
  -> convert CallToolResult to tools.Result
```

### Safety 判定

```go
func SafetyForMCPTool(serverCfg MCPConfig, remoteToolName string) tools.Safety
```

判定规则：

```text
默认：SafetySideEffect
如果 server.read_only=true：SafetyReadOnly
如果 tools[remoteToolName].read_only 显式设置：用 tool-level 值覆盖 server-level 值
MCP annotations 不参与 Safety 自动判定
```

这样和 spec 的安全默认值一致，也支持用户有意识地放开只读 MCP 工具进入 Plan Mode。

## 模块设计

### config 模块

**职责：**

- 读取项目级配置文件。
- 尝试读取用户级配置文件。
- 合并 MCP Server 配置。
- 校验 MCP 配置字段。
- 展开 MCP env / headers 中的 `${VAR}`。
- 保持 provider 配置的现有校验语义。

**对外接口：**

```go
func Load(path string) (*Config, error)
func LoadMerged(userPath, projectPath string) (*Config, error)
```

`Load` 保持现有行为：读取一个配置文件并校验。这样现有测试和调用语义不被破坏。

`LoadMerged` 是 phase6 新增入口：读取用户级和项目级配置，合并 `MCPServers`，并用项目级 provider 配置作为最终 provider 配置。main 启动时改用 `LoadMerged`。

**合并规则：**

```text
providers:
  使用项目级配置。
  用户级 providers 不参与合并。

mcp_servers:
  先加载用户级。
  再加载项目级。
  同名 server 由项目级整体覆盖用户级。
  不同名 server 合并保留。
```

**校验规则：**

```text
server name 不能为空。
type 必须是 stdio 或 http。
stdio 必须有 command。
http 必须有 url。
stdio 不允许只配置 url。
http 不要求 command / args。
tools 下的 read_only 是可选 bool。
env / headers 的 ${VAR} 必须能从宿主环境展开。
错误信息只包含变量名和字段位置，不包含展开后的值。
```

### mcp/jsonrpc 模块

**职责：**

- 定义 JSON-RPC 2.0 request / response / error。
- 生成递增请求 id。
- 把 JSON-RPC error 转成 Go error。
- 帮助 transport 统一编码和解码消息。

**对外接口：**

```go
func NewRequest(id int64, method string, params interface{}) JSONRPCRequest
func DecodeResponse(data []byte) (JSONRPCResponse, error)
func ErrorFromResponse(resp JSONRPCResponse) error
```

这层不包含 MCP 业务方法，只处理 JSON-RPC 通用结构。

### mcp/transport 模块

**职责：**

- 提供统一 `Transport` 接口。
- 实现 stdio transport。
- 实现 Streamable HTTP transport。
- 负责超时、取消和关闭。

**stdio 设计：**

stdio transport 在 `Start` 时启动子进程，拿到 stdin/stdout。每次 `Request`：

```text
生成请求 id
创建 pending channel
写 JSON-RPC request 到 stdin
等待 reader goroutine 按 id 投递 response
ctx 取消或超时则删除 pending 并返回错误
```

reader goroutine 持续从 stdout 读取一行 JSON 消息。收到 response 后按 id 找 pending；找不到 pending 的消息忽略或记录为协议警告。本阶段不处理 server 主动 request，遇到这类消息可以安全忽略。

关闭时：

```text
关闭 stdin/stdout
cancel reader
kill 或 wait 子进程
清空 pending 并唤醒等待者
```

**HTTP 设计：**

HTTP transport 每次 `Request` 构造 JSON-RPC 请求体，向配置 url 发起 HTTP POST，请求头使用展开后的 headers。响应体按 JSON-RPC response 解析。

本阶段只实现请求/响应式 Streamable HTTP，不实现复杂 SSE 长连接、自动重连、恢复 last-event-id 或 server push。这样能满足 initialize / tools/list / tools/call 的基础能力，同时保持实现边界可控。

### mcp/client 模块

**职责：**

- 封装单个 MCP Server 生命周期。
- 调用 initialize。
- 调用 tools/list。
- 调用 tools/call。
- 将协议错误包装成带阶段信息的错误。

**对外接口：**

```go
func NewClient(name string, transport Transport) *Client
func (c *Client) Start(ctx context.Context) error
func (c *Client) Initialize(ctx context.Context) error
func (c *Client) ListTools(ctx context.Context) ([]MCPTool, error)
func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (CallToolResult, error)
func (c *Client) Close() error
```

`Start` 只启动 transport。`Initialize` 和 `ListTools` 分开，方便测试不同失败阶段。

### mcp/manager 模块

**职责：**

- 根据配置创建多个 client。
- 独立启动每个 Server。
- 执行 initialize 和 tools/list。
- 把成功发现的 tools 注册进 Registry。
- 收集每个 Server 的错误。
- 管理所有 client 的生命周期。

**对外接口：**

```go
func NewManager(cfg map[string]config.MCPConfig) *Manager
func (m *Manager) Discover(ctx context.Context) DiscoverResult
func (m *Manager) RegisterTools(registry *tools.Registry, result DiscoverResult)
func (m *Manager) Close() error
```

`Discover` 不直接修改 Registry，便于测试“发现”和“注册”两个阶段。`RegisterTools` 只注册 `Discover` 成功拿到的 tools。

**失败隔离策略：**

```text
某个 server 配置无效：记录 config error，跳过该 server。
某个 server 启动失败：记录 start error，跳过该 server。
某个 server initialize 失败：记录 initialize error，关闭该 server。
某个 server tools/list 失败：记录 list_tools error，关闭该 server。
某个 server tools 为空：保留 session，但注册 0 个工具，不算致命错误。
某个工具注册名冲突：记录 register error，跳过该工具。
```

### mcp/tool_adapter 模块

**职责：**

- 将 MCP tool 描述转换为 OneCode `tools.Tool`。
- 将 MCP call result 转换为 `tools.Result`。
- 负责工具名、描述、schema 和 timeout。

**注册命名：**

```text
server name: github
remote tool name: search_issues
OneCode tool name: github.search_issues
```

server name 和 remote tool name 应做最小合法化处理：

```text
保留字母、数字、下划线、短横线和点。
空白替换为下划线。
非法字符替换为下划线。
结果为空则拒绝注册。
```

**结果转换：**

```text
MCP content 中的 text 项拼接为工具输出。
非 text content 用简短占位描述保留类型信息。
MCP isError=true -> tools.Result{IsError:true}
协议错误 / transport 错误 / ctx 取消 -> tools.Result{IsError:true}
```

### main 启动接入

**职责：**

- 计算项目根目录。
- 调用 `config.LoadMerged`。
- 注册内置工具。
- 创建 MCP Manager 并发现工具。
- 注册 MCP 工具。
- 把带有内置工具和 MCP 工具的 Registry 传给 TUI。

启动错误处理策略：

```text
项目级配置无法读取或 provider 校验失败：启动失败。
用户级配置不存在：忽略。
用户级配置存在但格式错误：启动失败。
MCP 单个 Server 失败：打印 warning，继续启动。
MCP 工具注册失败：打印 warning，继续启动。
```

这个策略和 spec 的“单个 Server 失败不影响其他工具”一致，同时保留配置文件整体错误的快速失败。

## 模块交互与数据流

### 启动期数据流

```text
main
  -> os.Getwd 得到 projectRoot
  -> config.LoadMerged(userConfigPath, projectConfigPath)
       -> load user config if exists
       -> load project config
       -> validate providers
       -> merge mcp_servers
       -> expand ${VAR} in MCP env / headers
  -> tools.NewRegistry()
  -> register builtin tools
  -> mcp.NewManager(cfg.MCPServers)
  -> manager.Discover(startupCtx)
       -> for each server config
            -> build transport
            -> client.Start
            -> client.Initialize
            -> client.ListTools
            -> collect session/tools/errors
  -> manager.RegisterTools(registry, discoverResult)
       -> for each discovered MCP tool
            -> build RemoteTool(server.tool)
            -> compute SafetyForMCPTool
            -> registry.RegisterWithSafety(remoteTool, safety)
  -> tui.New(cfg.Providers, registry, projectRoot)
```

启动期有两类错误：

```text
致命错误：
  项目级配置缺失或不可解析
  provider 配置无效
  MCP 配置整体格式无效
  环境变量展开失败

非致命错误：
  单个 MCP Server 启动失败
  单个 MCP Server initialize 失败
  单个 MCP Server tools/list 失败
  单个 MCP tool 注册冲突或 schema 无效
```

非致命错误以 warning 形式输出，继续启动。后续可以把 warnings 接入 TUI 状态区，本阶段先允许启动时打印到 stderr。

### MCP Server 发现流程

单个 Server 的发现流程：

```text
Manager.discoverServer(serverName, serverConfig)
  -> buildTransport(serverConfig)
  -> client.Start(ctx)
  -> client.Initialize(ctx)
       JSON-RPC request:
         method = "initialize"
         params = InitializeParams
       JSON-RPC response:
         result = InitializeResult
  -> client.ListTools(ctx)
       JSON-RPC request:
         method = "tools/list"
       JSON-RPC response:
         result = ListToolsResult
  -> return ServerSession{Name, Client, Tools}
```

如果 `tools/list` 返回空列表，该 Server 仍然算发现成功，只是不会注册工具。这样可以区分“协议失败”和“确实没有工具”。

### MCP 工具注册流程

```text
Manager.RegisterTools
  -> 遍历 DiscoverResult.Sessions
  -> 遍历 session.Tools
  -> registryName = sanitize(serverName) + "." + sanitize(tool.Name)
  -> schema = normalizeInputSchema(tool.InputSchema)
  -> description = buildRemoteToolDescription(serverName, tool)
  -> remoteTool = RemoteTool{registryName, remoteName, client, schema}
  -> safety = SafetyForMCPTool(session.Config, tool.Name)
  -> registry.RegisterWithSafety(remoteTool, safety)
```

`normalizeInputSchema` 的原则：

```text
MCP tool 有 inputSchema：原样保留为 map[string]interface{}。
MCP tool 没有 inputSchema：使用空 object schema。
schema 不是 object 或无法作为 JSON Schema 使用：注册失败并记录 warning。
```

MCP 工具注册进入同一个 Registry，所以 LLM Provider 仍通过现有 `Registry.ToToolDefinitions()` 拿工具列表，不需要任何 MCP 专用分支。

### Agent 调用 MCP 工具流程

```text
LLM
  -> tool call: {name:"github.search_issues", input:{...}}

Agent Scheduler
  -> registry.Safety("github.search_issues")
  -> 如果 Plan Mode 且 safety != read_only：返回禁用工具错误
  -> permission.Manager.Resolve(...)
  -> 如果权限拒绝：返回权限拒绝工具结果
  -> registry.Execute("github.search_issues", input)

Registry
  -> 找到 RemoteTool
  -> 用 tool timeout 包一层 context
  -> RemoteTool.Execute(ctx, input)

RemoteTool
  -> client.CallTool(ctx, remoteName, input)
       JSON-RPC request:
         method = "tools/call"
         params = {name: remoteName, arguments: input}
       JSON-RPC response:
         result = CallToolResult
  -> convert CallToolResult to tools.Result

Agent
  -> conv.AddToolResult
  -> 下一轮 LLM 基于工具结果继续
```

这条链路中，MCP 工具天然继承：

```text
Registry timeout
Agent cancellation
Plan Mode safety filtering
Phase 5 permission confirmation
Tool result 回写
Agent loop 继续执行
```

### stdio Transport 请求配对流程

```text
Request(ctx, method, params)
  -> id = nextID()
  -> pending[id] = response channel
  -> encode JSONRPCRequest{id, method, params}
  -> write request line to stdin
  -> wait:
       response from pending channel -> decode result
       ctx.Done -> remove pending, return ctx error
       transport closed -> return closed error
```

后台 reader：

```text
for each stdout line
  -> decode JSONRPCResponse
  -> if response has id:
       find pending[id]
       send response
     else:
       ignore notification
  -> if decode error:
       fail all pending and close transport
```

这样满足“请求带 id，回包按 id 关联”的要求。

### HTTP Transport 请求流程

```text
Request(ctx, method, params)
  -> id = nextID()
  -> encode JSONRPCRequest{id, method, params}
  -> POST configured URL
  -> attach configured headers
  -> read response body
  -> decode JSONRPCResponse
  -> verify response.id == request.id
  -> decode result
```

HTTP 场景虽然多数请求天然一问一答，但仍然保留 JSON-RPC id 校验，避免远端返回错包时悄悄把结果交给错误请求。

### 关闭流程

```text
main / TUI 退出
  -> manager.Close()
       -> close every Client
       -> close every Transport
       -> stdio transport 关闭 stdin/stdout
       -> 等待或终止子进程
       -> 清理 pending 请求
```

如果当前 Agent Run 正在执行 MCP 工具，Run 的 context 取消会先传到 `RemoteTool.Execute`，再传到 `client.CallTool` 和 `transport.Request`。transport 应删除 pending 请求并返回取消错误，最终变成 `IsError=true` 的工具结果。

## 文件组织

```text
src/
├── cmd/onecode/
│   └── main.go
│      - 改用 config.LoadMerged
│      - 注册内置工具后启动 MCP Manager
│      - 注册 MCP RemoteTool
│      - 退出时关闭 MCP Manager
│
├── internal/config/
│   ├── config.go
│   │  - 扩展 Config / MCPConfig / MCPToolConfig
│   │  - 保持 Load 单文件读取语义
│   │  - 新增 LoadMerged
│   │  - MCP 配置校验
│   │  - ${VAR} 展开
│   └── config_test.go
│      - 补充 MCP 配置加载、合并、覆盖、环境变量展开测试
│
├── internal/mcp/
│   ├── types.go
│   │  - MCP 协议结构、工具结构、通用错误类型
│   ├── jsonrpc.go
│   │  - JSON-RPC request / response / error
│   │  - id 生成辅助
│   │  - response/result 解码
│   ├── transport.go
│   │  - Transport 接口
│   │  - transport 工厂
│   ├── stdio.go
│   │  - stdio 子进程启动
│   │  - stdin/stdout JSON-RPC 收发
│   │  - pending id 配对
│   │  - 关闭和子进程清理
│   ├── http.go
│   │  - Streamable HTTP 请求/响应式 transport
│   │  - headers 注入
│   │  - response id 校验
│   ├── client.go
│   │  - MCP initialize / tools/list / tools/call
│   ├── manager.go
│   │  - 多 Server 生命周期管理
│   │  - discover result / server error
│   │  - 注册 RemoteTool 到 Registry
│   ├── adapter.go
│   │  - RemoteTool 实现 tools.Tool
│   │  - MCP result -> tools.Result
│   │  - server.tool 命名
│   │  - SafetyForMCPTool
│   ├── *_test.go
│   │  - jsonrpc、stdio、http、client、manager、adapter 单元测试
│   └── testdata/
│      └── stdio_server.go
│         - 测试用假 MCP stdio server
│
└── internal/tools/
    └── registry.go
       - 如需要，补充冲突检测返回值或保持现有跳过语义
```

文档文件：

```text
doc/spec/phase6-mcp-client/
├── spec.md
├── plan.md
├── task.md
└── checklist.md
```

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| MCP 工具接入位置 | 启动期发现并注册到 `tools.Registry` | Agent 和 LLM Provider 无需感知 MCP，复用现有工具链路 |
| 工具命名 | `server.tool` | 避免内置工具和多个 Server 之间命名冲突，也便于权限规则书写 |
| MCP safety 默认值 | 默认 `SafetySideEffect` | 外部工具默认不可信，安全优先 |
| read-only 判断 | 只信配置显式声明，不信 annotations | MCP annotations 可选且不应作为自动放权依据 |
| Plan Mode 可见性 | 只暴露显式 read-only MCP 工具 | 兼顾规划阶段检索能力和安全边界 |
| 权限集成 | 复用 Phase 5 permission.Manager | 不新增 MCP 特权通道，所有工具执行前统一判断 |
| 配置合并 | 用户级 + 项目级；同名 MCP Server 项目级覆盖用户级 | 符合用户默认 + 项目定制的常见模型 |
| provider 合并 | 仍使用项目级 providers | 避免 phase6 扩大范围，减少对现有 provider 行为的破坏 |
| 凭据处理 | `${VAR}` 展开，敏感值不落盘不回显 | 避免 token 写入配置或错误输出 |
| stdio 实现 | 子进程 + stdin/stdout + reader goroutine + pending map | 符合 MCP stdio 传输，也满足异步 id 配对 |
| HTTP 实现 | 先实现请求/响应式 Streamable HTTP | 覆盖 initialize/list/call 基础能力，避免本阶段引入 SSE 恢复和自动重连复杂度 |
| 单 Server 失败 | 记录 warning，继续启动 | 满足故障隔离，避免一个外部服务拖垮内置工具 |
| MCP result 转换 | text 内容拼接，非 text 内容降级描述 | 保证模型可读，同时不做富媒体展示 |
| 生命周期 | Manager 持有 Clients，main 退出时统一 Close | 避免 stdio 子进程和 pending 请求泄漏 |
| 测试策略 | 假 transport + httptest + 测试 stdio server | 不依赖真实外部 MCP Server，保证稳定可复现 |

## Spec 覆盖关系

| Spec 需求 | 设计归属 |
|-----------|----------|
| F1 配置加载 | config.Config / LoadMerged / MCPConfig |
| F2 stdio 传输 | mcp.StdioTransport |
| F3 Streamable HTTP | mcp.HTTPTransport |
| F4 JSON-RPC | mcp/jsonrpc.go + Transport.Request |
| F5 会话生命周期 | mcp.Client |
| F6 工具发现与注册 | mcp.Manager + RemoteTool |
| F7 工具调用适配 | RemoteTool.Execute + Client.CallTool |
| F8 多 Server 管理 | mcp.Manager / ServerSession |
| F9 权限系统集成 | Registry Safety + Phase 5 permission.Manager |
| F10 启动期接入 | cmd/onecode main.go |
| N1 安全默认值 | SafetyForMCPTool |
| N2 故障隔离 | DiscoverResult / ServerError |
| N3 兼容工具系统 | RemoteTool 实现 tools.Tool |
| N4 可观测错误 | ServerError.Stage + sanitized errors |
| N5 超时取消 | context 贯穿 Registry -> RemoteTool -> Transport |
| N6 资源生命周期 | Manager.Close / Transport.Close |
| N7 可测试性 | fake transport / httptest / fake stdio server |

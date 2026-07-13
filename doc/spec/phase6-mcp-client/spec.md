# Phase 6 MCP Client Spec

## 背景

OneCode 目前已经具备 6 个内置工具、ReAct Agent Loop、结构化 Prompt Runtime 和 Phase 5 权限系统。Agent 可以通过统一工具中心调用内置工具，也能在工具执行前经过权限判断。

但当前工具能力仍然局限在内置工具集合中。用户如果想接入 GitHub、数据库、浏览器、监控平台、文档系统或团队内部服务，需要把这些能力重新写成 OneCode 内置 Tool，扩展成本较高，也难以复用 MCP 生态中已有的 Server。

Phase 6 的目标是让 OneCode 成为 MCP Client：启动时从配置文件读取 MCP Server 列表，连接本地 stdio Server 或远程 Streamable HTTP Server，通过标准 MCP 协议完成初始化、工具发现和工具调用，再把远端工具适配成 OneCode 现有工具接口注册进工具中心。对 Agent 来说，MCP 工具应和内置工具一样可见、可调用、可被权限系统约束。

## 目标

- 支持从用户级和项目级配置读取 MCP Server 列表，并按优先级合并。
- 支持 stdio 和 Streamable HTTP 两种 MCP 传输方式。
- 支持 MCP 基础会话流程：初始化握手、列出工具、调用工具。
- 支持 JSON-RPC 2.0 请求、响应、错误和异步 id 关联。
- 将 MCP Server 暴露的工具包装成 OneCode Tool，并注册到现有 Registry。
- 使用 `server.tool` 形式为 MCP 工具命名，避免和内置工具或其他 Server 工具冲突。
- 多个 MCP Server 独立连接和注册，单个 Server 启动或发现失败不影响其他 Server。
- MCP 工具默认按有副作用工具处理，并进入 Phase 5 权限系统。
- 只有显式声明为 read-only 的 MCP 工具才允许在 Plan Mode 暴露。

## 功能需求

### F1: MCP Server 配置加载

系统应支持从用户级和项目级配置读取 MCP Server 列表。

配置应使用 map 结构声明多个 Server，每个 key 是 Server 名称。项目级配置与用户级配置合并时，同名 Server 以后加载的一层覆盖先加载的一层。

配置应支持 stdio 和 Streamable HTTP 两种 Server 类型。stdio Server 应能声明 command、args、env；HTTP Server 应能声明 url、headers。

配置中的 stdio env 和 HTTP headers 支持 `${VAR}` 环境变量展开；系统不要求也不鼓励把真实密钥写进配置文件，展开后的敏感值不落盘、不回显。

### F2: stdio 传输

系统应能启动本地 MCP Server 子进程，并通过标准输入输出与其交换 JSON-RPC 消息。

stdio 连接应能向子进程传入配置中的 command、args 和 env。子进程退出、启动失败或协议错误时，应只影响对应 Server，不应导致其他 Server 或整个 OneCode 启动失败。

### F3: Streamable HTTP 传输

系统应能连接远程 Streamable HTTP MCP Server，并通过 HTTP 请求/响应与其交换 JSON-RPC 消息。

HTTP 连接应支持配置 url 和 headers。请求应携带展开后的 headers，但错误信息和界面输出不应泄露 header 中的敏感值。

### F4: JSON-RPC 2.0 消息处理

系统应按 JSON-RPC 2.0 组织请求、响应和错误。

客户端发出的请求应带有唯一 id，并能将异步返回的响应按 id 关联回对应请求。收到错误响应时，应转换成可读错误结果，不能造成 Agent 或 TUI 崩溃。

### F5: MCP 会话生命周期

每个 MCP Server 的一次连接会话应至少包含三个能力：

1. 初始化握手。
2. 列出工具。
3. 调用工具。

只有初始化成功并成功列出工具的 Server，才应把其工具注册到工具中心。

### F6: MCP 工具发现与注册

系统应把 MCP Server 返回的工具描述转换成 OneCode 工具定义，并注册到现有工具中心。

MCP 工具注册名应使用 `server.tool` 形式，避免和内置工具或其他 Server 工具冲突。工具描述和参数 schema 应尽量保留 MCP Server 返回的信息，供 LLM 构造合法工具调用。

### F7: MCP 工具调用适配

当 Agent 调用 MCP 工具时，系统应通过对应 Server 连接发送 MCP 工具调用请求，并把 MCP 返回结果转换成 OneCode 工具结果。

MCP 调用成功时，结果应作为普通工具输出回写给模型。MCP 调用失败、超时、连接断开或协议错误时，应作为 `IsError=true` 的工具结果回写给模型。

### F8: 多 Server 连接管理

系统应能同时管理多个 MCP Server 连接。

每个 Server 的连接、工具列表和调用状态应相互隔离。单个 Server 连接失败、初始化失败或调用失败，不应影响其他 Server 的工具发现和调用。

### F9: 与权限系统集成

MCP 工具应进入 Phase 5 的统一权限系统，不应绕过工具执行前的权限判断。

MCP 工具默认按有副作用工具处理。只有配置中显式声明为 read-only 的 MCP 工具，才应按只读工具处理，并允许在 Plan Mode 暴露。

### F10: 启动期接入

OneCode 启动时应根据配置自动发现 MCP Server 工具，并在创建 Agent 和 TUI 前完成可用 MCP 工具注册。

如果部分 MCP Server 发现失败，系统应保留可读错误信息，同时继续注册其他可用工具和内置工具。

## 非功能需求

### N1: 安全默认值

MCP 工具默认应按不可信外部工具处理。系统不应仅凭 MCP Server 返回的工具注解自动放宽权限或 Plan Mode 可见范围。

### N2: 故障隔离

MCP Server 的启动、连接、初始化、工具发现和工具调用失败都应限制在对应 Server 或对应工具调用内。除配置整体不可解析等基础错误外，单个 Server 失败不应阻止 OneCode 使用内置工具或其他 MCP Server 工具。

### N3: 兼容现有工具系统

MCP 工具接入后，Agent、LLM Provider 和工具 Registry 的主要调用语义应保持一致。内置工具的名称、schema、权限行为和调用结果不应因 MCP 接入发生变化。

### N4: 可观测错误

MCP 相关失败应提供可读错误信息，至少能区分配置错误、启动失败、连接失败、初始化失败、工具发现失败、调用失败和协议错误。

错误信息不应泄露展开后的环境变量值、HTTP header 敏感值或完整密钥。

### N5: 超时与取消

MCP 初始化、工具发现和工具调用应支持超时和上下文取消。用户取消当前 Agent Run 时，正在等待的 MCP 工具调用应尽快返回取消错误。

### N6: 资源生命周期

stdio 子进程、HTTP 连接、后台读写协程和响应等待状态应在 OneCode 退出或对应 Server 关闭时释放，避免子进程残留、协程泄漏或无界 pending 请求堆积。

### N7: 可测试性

MCP 客户端、传输层、JSON-RPC id 配对、工具适配、配置合并、环境变量展开和权限安全分类应能通过不依赖真实外部 MCP Server 的测试验证。

## 不做的事

- **MCP resources / prompts / sampling**：本阶段只接入 MCP tools，不实现资源、提示词模板、采样或其他 MCP 能力。
- **Server 健康检查和自动重连**：本阶段不做周期性健康检查、断线自动重连或失败 Server 的后台恢复。
- **MCP Server 安装和包管理**：用户需要自己安装本地 MCP Server 或提供远程 Server 地址，OneCode 不负责下载、安装、升级或管理 Server 包。
- **OAuth 和独立密钥管理**：本阶段只支持环境变量展开，不做 OAuth 授权流程、密钥保险箱、凭据轮换或系统钥匙串集成。
- **复杂权限表达式**：MCP 工具只复用 Phase 5 的工具级权限模型，不新增按 Server 信任等级、MCP 注解、资源类型或远端操作类别判断的复杂权限策略。
- **完全信任 MCP annotations**：MCP Server 返回的工具注解可用于展示或后续扩展，但本阶段不把它作为自动判定 read-only 或自动放权的依据。
- **Bash / 文件沙箱替代机制**：MCP 工具不替代现有内置工具的沙箱、黑名单和 Plan Mode 限制，也不扩展 Phase 5 的路径沙箱覆盖范围。
- **远程 Server 审计日志**：本阶段不持久化 MCP 请求、响应或权限决策的审计流水。
- **复杂流式工具结果展示**：如果 MCP Server 返回多段或结构化内容，本阶段只需要转换成 OneCode 可回写给模型的工具结果文本，不实现富媒体或交互式展示。
- **跨会话连接复用**：MCP Server 连接只在当前 OneCode 进程生命周期内缓存和复用，不做跨进程、跨会话的连接池。

## 验收标准

- AC1: 用户级和项目级配置都能声明 MCP Server；同名 Server 按项目级覆盖用户级合并。
- AC2: stdio MCP Server 可以被启动、初始化、列出工具，并把工具注册为 `server.tool`。
- AC3: Streamable HTTP MCP Server 可以被连接、初始化、列出工具，并把工具注册为 `server.tool`。
- AC4: JSON-RPC 请求带唯一 id；响应能按 id 关联回对应请求；错误响应能转换为可读错误。
- AC5: 初始化成功但工具列表为空的 Server 不影响启动；初始化失败或工具发现失败的 Server 不影响其他 Server。
- AC6: MCP 工具的描述和参数 schema 能转换成 OneCode 工具定义，并出现在 LLM 可见工具列表中。
- AC7: Agent 调用 MCP 工具时，会转发为对应 Server 的 MCP 工具调用，并把成功结果回写给模型。
- AC8: MCP 工具调用失败、超时、连接断开或协议错误时，返回 `IsError=true` 工具结果，而不是导致 Agent Loop 崩溃。
- AC9: MCP 工具默认按有副作用工具处理，会进入 Phase 5 权限判断。
- AC10: 只有配置中显式声明为 read-only 的 MCP 工具会在 Plan Mode 暴露。
- AC11: stdio env 和 HTTP headers 支持 `${VAR}` 展开；缺失变量会产生可读错误；展开后的敏感值不写回配置、不在错误中回显。
- AC12: 多个 MCP Server 同时配置时，单个 Server 失败不影响内置工具和其他 MCP Server 工具注册。
- AC13: 用户取消 Agent Run 时，正在等待的 MCP 工具调用能收到取消并返回错误结果。
- AC14: OneCode 退出或 MCP Manager 关闭时，stdio 子进程和内部等待状态被清理。
- AC15: MCP 接入不改变 6 个内置工具的注册名、schema、权限行为和成功结果格式。
- AC16: 本阶段没有实现 resources、prompts、sampling、自动重连、OAuth、审计日志和 MCP Server 安装管理。

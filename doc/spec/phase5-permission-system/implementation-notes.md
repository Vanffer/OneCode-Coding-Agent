# Permission System Implementation Notes

## 阶段目标回顾

Phase 5 的目标，是给 OneCode 的工具执行链路加上一套分层权限系统。

Phase 3 已经实现 ReAct Agent Loop，Phase 4 又把 Prompt Runtime 结构化了；但在 Phase 5 之前，只要模型能看到某个工具，工具执行前就缺少统一权限判断。尤其是 `bash`、`write_file`、`edit_file` 这类有副作用工具，如果没有执行前拦截，会把风险直接交给模型行为本身。

Phase 5 引入的核心链路是：

```text
LLM 返回 tool call
  -> Agent Scheduler
  -> Plan Mode 先做工具可见性/安全等级限制
  -> permission.Manager.Resolve
       -> 黑名单
       -> 路径沙箱
       -> session/local/project/user 规则
       -> permission mode
       -> 必要时请求用户确认
  -> allow: Registry.Execute
  -> deny: ToolResult{IsError:true}
  -> ask: Agent 发权限事件给 TUI，等待用户选择
```

本阶段有两个重要原则：

```text
权限拒绝是工具错误结果，不是 Agent 系统错误
权限系统不能削弱 Plan Mode，只能在 Execute/Plan 现有工具边界内工作
```

也就是说，模型触发一次权限拒绝后，Agent Loop 不会直接崩溃；模型能看到拒绝原因，并在下一轮尝试更安全的策略。

## 五层防御模型

Phase 5 最终落地为五层防御：

```text
第一层：危险命令黑名单
  内置、不可配置、最高优先级，主要拦 Bash 高危命令。

第二层：路径沙箱
  限制文件相关工具的显式路径或搜索根必须位于项目根内。

第三层：规则引擎
  支持 Tool(pattern): allow/deny，按 session > local > project > user 匹配。

第四层：权限模式
  Strict / Default / Permissive / Bypass 作为规则未命中时的默认策略。

第五层：人在回路
  对 ask 决策暂停工具执行，由 TUI 返回 deny/once/session/forever。
```

这里的硬边界是：

```text
黑名单和路径沙箱不能被 allow 规则、session 规则、mode 或 Bypass 绕过
Bash 不做路径沙箱
永久允许只写入本地级规则，并且只生成精确匹配规则
```

## 主要改动

### 新增 permission 包

新增 `internal/permission` 包：

```text
types.go      - 权限核心类型：Mode、Action、Scope、Request、Target、Decision
target.go     - 从工具调用参数提取权限判断目标
blacklist.go  - 内置危险 Bash 命令黑名单
sandbox.go    - 项目根路径沙箱与符号链接解析
rules.go      - 规则解析、工具名归一、规则匹配、精确规则生成
store.go      - user/project/local YAML 规则加载与 local 规则写入
confirmer.go  - Confirmer 接口与 StaticConfirmer 测试实现
manager.go    - 权限判断主流程与确认结果处理
*_test.go     - 黑名单、沙箱、规则、store、manager 测试
```

核心入口是：

```go
func (m *Manager) Resolve(ctx context.Context, req Request) Decision
```

`Resolve` 比 `Authorize` 多一步：当 `Authorize` 得到 `ActionAsk` 时，会调用 `Confirmer` 等待用户选择，并把确认结果转成最终 `Decision`。

### Agent 接入

`Agent` 增加：

```go
permissionManager   *permission.Manager
permissionResponses chan permission.ConfirmationResponse
```

构造函数改成 options 模式：

```go
agent.New(provider, registry, agent.WithPermissionManager(manager))
```

这样 Phase 4 的 `WithPromptRuntime` 和 Phase 5 的 `WithPermissionManager` 可以共存，不再继续扩展位置参数。

工具调度器 `scheduler.go` 在真正调用 `registry.Execute` 前执行：

```go
decision := a.permissionManager.Resolve(ctx, permission.Request{...})
if decision.Action != permission.ActionAllow {
    return llm.ToolResult{IsError:true, Content:"权限拒绝: ..."}
}
```

### Agent 事件扩展

新增 Agent 事件：

```go
EventPermissionRequest
```

以及：

```go
type PermissionEvent struct {
	Request permission.ConfirmationRequest
}
```

Agent 内部实现了 `eventConfirmer`：

```text
permission.Manager asks
  -> eventConfirmer.Confirm
  -> send EventPermissionRequest
  -> wait permissionResponses channel
  -> return ConfirmationResponse
```

这让权限系统不直接依赖 TUI，仍然通过 Agent 事件流解耦。

### TUI 接入

TUI 新增状态：

```go
statePermissionConfirm
pendingPerm *agent.PermissionEvent
```

收到 `EventPermissionRequest` 后：

```text
TUI 保存 pendingPerm
状态切到 statePermissionConfirm
展示 tool / target / risk / reason / args
等待用户按键
```

按键映射：

```text
d   deny
o   allow once
s   allow session
f   allow forever
esc cancel
```

选择后通过：

```go
agent.RespondPermission(permission.ConfirmationResponse{...})
```

把结果回给 Agent。

### 启动接入

TUI 创建 Agent 时创建权限 Manager：

```go
permission.NewManager(permission.ManagerOptions{
	ProjectRoot: projectRoot,
	Store:       permission.DefaultFileStore(projectRoot),
})
```

`cmd/onecode/main.go` 把当前工作目录作为 project root 传给 TUI。后续 Phase 6 又在 main 中加了 MCP Manager，但 Phase 5 的权限根仍然来自这个启动 cwd。

### 文档与示例

新增：

```text
doc/spec/phase5-permission-system/spec.md
doc/spec/phase5-permission-system/plan.md
doc/spec/phase5-permission-system/task.md
doc/spec/phase5-permission-system/checklist.md
doc/spec/phase5-permission-system/examples.md
```

`examples.md` 说明了权限规则文件层级、规则格式和权限模式。

## 最终目录结构

Phase 5 后，与权限系统相关的核心文件如下：

```text
src/
├── cmd/onecode/
│   └── main.go
└── internal/
    ├── agent/
    │   ├── agent.go
    │   ├── events.go
    │   ├── scheduler.go
    │   ├── loop_test.go
    │   └── scheduler_test.go
    ├── permission/
    │   ├── blacklist.go
    │   ├── blacklist_test.go
    │   ├── confirmer.go
    │   ├── manager.go
    │   ├── manager_test.go
    │   ├── rules.go
    │   ├── rules_test.go
    │   ├── sandbox.go
    │   ├── sandbox_test.go
    │   ├── store.go
    │   ├── store_test.go
    │   ├── target.go
    │   ├── target_test.go
    │   ├── test_helpers_test.go
    │   └── types.go
    └── tui/
        └── model.go
```

依赖方向：

```text
permission -> tools/searchutil + tools.Safety + 标准库
agent      -> permission + tools + llm
tui        -> agent + permission
cmd        -> tui + tools + config
```

`permission` 不依赖 Agent 或 TUI。人在回路通过 `Confirmer` 接口抽象出来，具体 UI 交互由 Agent/TUI 事件流承接。

## 架构、数据流与状态变化

### 1. 权限请求目标提取

Agent 调用 Manager 时传入：

```go
permission.Request{
	ID:      call.ID,
	Tool:    call.Name,
	Args:    call.Input,
	Safety:  safety,
	AgentMode: mode.String(),
}
```

`ExtractTarget` 把不同工具的参数统一成 `Target`：

```text
bash:
  command -> TargetCommand

read_file / write_file / edit_file:
  path -> TargetPath

glob:
  path(default ".") + pattern -> TargetSearch

grep:
  path(default ".") + glob -> TargetSearch
```

规则、黑名单、沙箱都基于这个 Target 工作。这样 Manager 不需要理解每个工具的完整参数语义，只关注主要风险目标。

### 2. Manager 主判断链路

`Authorize` 的顺序是：

```text
ctx.Err
  -> deny builtin

ExtractTarget

Blacklist.Check
  -> deny builtin if matched

checkSandbox
  -> deny builtin if path escape / missing

orderedRuleSets
  -> session
  -> local
  -> project
  -> user

defaultByMode
  -> allow / ask
```

`Resolve` 在此基础上处理 ask：

```text
decision := authorize(...)
if decision.Action != ask:
  return decision

response := confirmer.Confirm(...)
switch response.Choice:
  deny          -> deny
  allow_once    -> allow
  allow_session -> append session exact allow rule
  allow_forever -> append local exact allow rule
```

### 3. Agent 工具执行数据流

权限接入后的工具执行链路：

```text
executeOneTool
  -> EventToolStart
  -> permissionManager.Resolve
       -> allow / deny / ask
  -> deny:
       EventToolResult(IsError=true)
       llm.ToolResult(IsError=true)
  -> allow:
       registry.Execute
       EventToolResult
       llm.ToolResult
```

这里有意把 `EventToolStart` 放在权限判断之前。这样用户在 UI 上能看到模型尝试执行哪个工具，即使后来被权限拒绝，也有一条完整的工具事件。

权限拒绝不会增加 `badTools`。`badTools` 仍用于未知工具和 Plan Mode 禁用工具这类模型工具选择错误。权限拒绝是策略结果，应该回写给模型让它调整，而不是直接计入坏工具上限。

### 4. 人在回路数据流

一次需要确认的工具调用：

```text
Manager.defaultByMode -> ActionAsk
  |
  v
Resolve -> confirmer.Confirm
  |
  v
eventConfirmer.Confirm
  -> send EventPermissionRequest
  -> wait permissionResponses
  |
  v
TUI 收到 EventPermissionRequest
  -> statePermissionConfirm
  -> pendingPerm = request
  -> 用户按 d/o/s/f/esc
  |
  v
TUI answerPermission
  -> agent.RespondPermission
  |
  v
eventConfirmer 收到 response
  -> Manager.Resolve 转成 Decision
```

`eventConfirmer` 内部用 mutex 串行化确认请求。即使一轮有多个工具调用需要确认，也不会同时弹多个权限请求，避免 UI 状态混乱。

### 5. TUI 状态变化

Phase 5 前主要状态：

```text
stateSelecting
stateIdle
stateStreaming
```

新增：

```text
statePermissionConfirm
```

状态转换：

```text
stateStreaming
  -- EventPermissionRequest -->
statePermissionConfirm

statePermissionConfirm
  -- d/o/s/f -->
stateStreaming

statePermissionConfirm
  -- esc -->
stateStreaming + cancelCurrent()
```

`esc` 时 TUI 先回传 `ChoiceDeny`，再取消当前 run。这样等待中的 confirmer 不会一直阻塞。

### 6. Conversation 状态变化

权限拒绝仍然写入 tool result：

```text
assistant(tool_call)
tool_result(IsError=true, Content="权限拒绝: ...")
```

下一轮模型可以看到：

```text
权限拒绝: Strict 模式需要用户确认
权限拒绝: 路径超出项目沙箱
权限拒绝: 危险操作黑名单拦截...
```

这和普通工具执行失败保持一致：失败是模型可见上下文，不是不可恢复异常。

### 7. 规则配置状态

配置来源：

```text
user    ~/.onecode/permissions.yaml
project <project>/.onecode/permissions.yaml
local   <project>/.onecode/permissions.local.yaml
session 当前进程内存
```

加载时：

```text
FileStore.Load
  -> user rule set
  -> project rule set
  -> local rule set
  -> mode: user mode then local mode override
```

判断时：

```text
session > local > project > user > mode
```

注意：project 里的 `mode` 当前不覆盖用户偏好。mode 只从 user/local 取，local 覆盖 user。

### 8. 永久允许状态变化

用户选择 `allow_forever`：

```text
GenerateExactAllowRule(req, target, ScopeLocal)
  -> Store.AppendLocalRule
  -> <project>/.onecode/permissions.local.yaml
```

写入前会校验 local 配置路径必须位于项目 `.onecode` 目录下，避免把永久规则写到项目外。

生成的是精确规则，不会泛化：

```text
Bash(git status): allow
```

不会自动变成：

```text
Bash(git *): allow
```

## 关键实现细节

### types.go：核心类型

权限系统的几个关键 enum：

```go
ModeStrict
ModeDefault
ModePermissive
ModeBypass

ActionAllow
ActionDeny
ActionAsk

ScopeSession
ScopeLocal
ScopeProject
ScopeUser
ScopeMode
ScopeBuiltin
```

`ScopeBuiltin` 表示黑名单、沙箱、ctx 取消这类不可配置硬边界。

`Decision` 同时携带：

```go
Action
Reason
Scope
Rule
Request
```

其中 `Request` 只在 `ActionAsk` 时用于展示给用户确认。

### blacklist.go：不可绕过黑名单

黑名单只处理 `TargetCommand`，也就是 Bash 命令。

当前覆盖：

```text
rm -rf /, rm -rf ~, rm -rf $HOME
Windows rmdir/rd/del 删除系统目录
format / mkfs 格式化磁盘
dd of=/dev/... 写磁盘设备
fork bomb
```

命中后直接返回：

```go
Decision{Action: ActionDeny, Scope: ScopeBuiltin}
```

测试覆盖了即使 `ModeBypass` 加 allow 规则，也不能绕过黑名单。

### sandbox.go：路径沙箱

Sandbox 初始化：

```text
project root
  -> filepath.Abs
  -> filepath.EvalSymlinks
  -> filepath.Clean
```

检查路径：

```text
path
  -> 空值当 "."
  -> 相对路径拼到 sandbox root
  -> filepath.Clean
  -> resolvePathForSandbox
  -> pathWithinRoot
```

已存在路径：

```text
os.Lstat(path)
  -> filepath.EvalSymlinks(path)
  -> clean real path
```

不存在路径：

```text
allowMissingLeaf=false -> 拒绝
allowMissingLeaf=true:
  -> 向上找最近存在父目录
  -> EvalSymlinks(real parent)
  -> 把缺失的 path parts 拼回去
  -> 再判断是否仍在 root 内
```

`write_file` 使用 `AllowMissingLeaf=true`，因为创建新文件时叶子节点可能不存在。

Windows 下 `pathWithinRoot` 会转小写，避免路径大小写差异导致误判。

### rules.go：规则解析和匹配

规则格式：

```text
Tool(pattern): allow
Tool(pattern): deny
```

例如：

```text
Bash(git status): allow
Bash(git push *): deny
ReadFile(**/*.go): allow
```

解析时会：

```text
NormalizeToolName
提取括号内 pattern
校验 action 只能 allow/deny
normalizePattern -> filepath.ToSlash
```

工具名归一：

```text
Bash       -> bash
ReadFile   -> read_file
write-file -> write_file
```

匹配时：

```text
工具名必须相同
从 Target 取 matchValues
无 glob 元字符 -> 精确匹配
有 * ? [      -> searchutil.MatchPattern
```

`TargetSearch` 会同时拿：

```text
target.Value
target.SearchRoot
target.Glob
```

因此 glob/grep 既可以按搜索根匹配，也可以按 glob 过滤模式匹配。

### store.go：规则加载和写入

`FileStore.Load` 读取三层文件：

```text
user    ~/.onecode/permissions.yaml
project <project>/.onecode/permissions.yaml
local   <project>/.onecode/permissions.local.yaml
```

不存在视为空规则；存在但 YAML 错误、mode 错误、rule 错误会返回启动错误。

mode 优先级：

```text
default
  -> user mode
  -> local mode
```

project mode 不覆盖用户偏好。

`AppendLocalRule` 写入本地级规则：

```text
validateLocalPath
MkdirAll(.onecode)
读取已有 permissions.local.yaml
append FormatRule(rule)
yaml.Marshal
os.WriteFile
```

### manager.go：权限网关

`Manager` 内部持有：

```go
mode
rules
session
blacklist
sandbox
store
confirmer
```

所有公开决策方法都用 mutex 包住：

```go
Authorize
Resolve
SetConfirmer
```

这是因为 session rules 和 confirmer 都可能在 Agent/TUI 交互中变化。

默认 confirmer 是：

```go
StaticConfirmer{Choice: ChoiceDeny}
```

这样即使忘记接 UI confirmer，权限系统也会保守拒绝，而不是默认放行。

### scheduler.go：权限和工具调度关系

工具调度顺序：

```text
工具是否存在
Plan Mode 是否允许
只读批次并发 / 副作用串行
executeOneTool
  -> permission
  -> registry.Execute
```

Plan Mode 检查在权限系统之前：

```go
if mode == ModePlan && safety != tools.SafetyReadOnly {
    badToolResult(...)
}
```

所以即使权限规则 allow，Plan Mode 也不能执行 `bash`、`write_file`、`edit_file`。

### eventConfirmer：Agent 和 TUI 的桥

`eventConfirmer` 同时持有：

```go
events chan<- Event
responses <-chan permission.ConfirmationResponse
```

它做两件事：

```text
1. 把 PermissionRequest 发到 Agent 事件流
2. 等待 TUI 通过 Agent.RespondPermission 写回 response
```

如果 ctx 取消，`Confirm` 返回 ctx error，Manager 最终转成 deny decision。

### TUI 权限确认

TUI 展示内容来自：

```go
permission.ConfirmationRequest{
	ID
	Tool
	ArgsPreview
	Target
	Risk
	Reason
}
```

界面展示：

```text
Permission required
tool: bash
target: git status
risk: 命令可能产生文件、进程或系统副作用
reason: Default 模式需要确认有副作用工具

[d] deny  [o] once  [s] session  [f] forever  [esc] cancel
```

用户选择后恢复到 `stateStreaming`，Agent 继续等待后续事件。

## 测试覆盖

### permission/blacklist

覆盖：

- 高危 Bash 命令命中 deny。
- 非 command target 不走黑名单。
- 黑名单返回 `ScopeBuiltin`。

### permission/sandbox

覆盖：

- 项目内路径允许。
- `..` 逃逸拒绝。
- 新文件 missing leaf 在项目内允许。
- 符号链接指向项目外时拒绝。

### permission/rules

覆盖：

- 规则解析成功。
- 非法规则拒绝。
- 工具名大小写和格式归一。
- 精确匹配。
- glob 匹配。
- `GenerateExactAllowRule` 只生成精确规则。

### permission/store

覆盖：

- 配置文件缺失时返回空规则和 default mode。
- user/project/local 三层加载。
- local mode 覆盖 user mode。
- bad rule 返回错误。
- `AppendLocalRule` 写入 local YAML。

### permission/manager

覆盖：

- 黑名单不能被 Bypass 或 allow rule 绕过。
- 规则优先级 local > project > user。
- Strict / Default / Permissive / Bypass 默认策略。
- allow once。
- allow session 后第二次命中 session rule。
- allow forever 写入 local rule。
- 沙箱在规则前拒绝。
- write_file missing leaf 项目内允许。

### agent

覆盖：

- 权限拒绝返回 tool error。
- 权限拒绝时 fake tool 不执行。
- 权限拒绝不计入 bad tool。
- Agent 发出 PermissionRequest 后，TUI/mock 回 allow once，loop 继续下一轮。
- Plan Mode 禁用工具仍优先于权限系统。

### TUI

TUI 权限确认主要通过编译和事件路径覆盖，人工行为包括：

```text
d -> deny
o -> allow once
s -> allow session
f -> allow forever
esc -> deny + cancel current run
```

常用验证命令：

```powershell
cd src
$env:GOCACHE = Join-Path (Resolve-Path ..).Path ".gocache"
go test ./internal/permission
go test ./internal/agent -run TestSchedulerPermission
go test ./...
```

## 设计取舍

### 1. 权限拒绝作为 tool result

如果权限拒绝直接终止 Agent Loop，模型没有机会调整策略。Phase 5 把拒绝作为：

```go
ToolResult{IsError:true}
```

这样模型下一轮可以看到拒绝原因，改用更安全路径或请求用户授权。

### 2. 黑名单和沙箱优先于规则

规则是用户/项目策略，黑名单和沙箱是硬边界。

因此判断顺序固定为：

```text
blacklist -> sandbox -> rules -> mode -> confirmer
```

这能保证 `Bypass` 也不能执行 `rm -rf /` 或读写项目外路径。

### 3. Bash 不做路径沙箱

Bash 命令的文件访问无法可靠静态解析：

```text
变量
重定向
脚本
子进程
alias/function
解释器内部逻辑
```

所以 Phase 5 只对 Bash 做：

```text
黑名单
规则
mode
用户确认
```

路径沙箱只覆盖显式路径工具。

### 4. allow forever 只写 local 精确规则

人在回路里的“永久允许”容易被误点。如果自动泛化成 `git *`，风险过大。

因此：

```text
allow forever -> permissions.local.yaml
allow forever -> exact rule only
```

想写通配规则，需要用户手动编辑配置。

### 5. project mode 不覆盖用户偏好

项目级规则适合团队共享策略，但 mode 是用户对当前机器/会话的信任偏好。

所以 mode 只从 user/local 取，避免项目配置把用户机器切到更宽松或更严格的全局行为。

### 6. TUI 通过 Agent 事件确认

没有让 permission 包直接 import TUI，也没有让 Manager 弹 UI。

路径是：

```text
permission.Manager -> Confirmer interface
Agent eventConfirmer -> EventPermissionRequest
TUI -> RespondPermission
```

这个边界保留了未来替换 UI、CLI、测试 confirmer 的空间。

## 当前限制

- 只覆盖 6 个内置工具；Phase 6 之后 MCP 工具也通过 Registry Safety 进入权限系统，但 Phase 5 本身没有专门处理 MCP。
- Bash 不做路径沙箱。
- 黑名单固定内置，不可配置，也没有外部更新机制。
- 没有网络请求限制。
- 没有 CPU、内存、磁盘、速率配额。
- 没有持久化审计日志。
- 没有权限规则图形化编辑器。
- 没有复杂条件规则，例如按时间、模型、provider、会话上下文判断。
- 没有多项目根或额外目录授权。
- `permissions.local.yaml` 适合本机私有规则；需要注意不要误提交到仓库。
- TUI 权限确认 UI 比较朴素，当前只是按键选择，没有规则详情展开。
- `ArgsPreview` 使用 `fmt.Sprintf("%v", args)`，复杂参数可读性有限。
- `eventConfirmer` 串行确认请求，安全但可能让多个需要确认的工具逐个等待。

## 复盘要点

Phase 5 的核心价值，是把工具执行从：

```text
模型请求工具 -> Registry.Execute
```

升级为：

```text
模型请求工具
  -> Agent Scheduler
  -> permission.Manager
  -> allow / deny / ask
  -> Registry.Execute 或 ToolResult error
```

这个变化确立了 OneCode 后续所有工具能力的安全入口：

- 内置工具走这里。
- Phase 6 MCP 工具注册进 Registry 后也能走这里。
- Plan Mode 仍然优先保护只读边界。
- 人在回路通过统一事件流和 TUI 解耦。
- 权限拒绝变成模型可见反馈，而不是程序崩溃。

最终 Phase 5 给 OneCode 建立了“能干活之前先判断能不能干”的执行前网关。后续要做网络限制、资源配额、审计日志、MCP 权限细分或更强沙箱，都可以继续挂在这个网关之后扩展。

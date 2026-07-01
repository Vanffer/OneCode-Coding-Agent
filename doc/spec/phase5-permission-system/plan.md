# Permission System Plan

## 架构概览

Phase 5 采用“权限网关 + Agent 确认事件”的方案：在工具真正执行前新增一个权限网关，集中处理黑名单、路径沙箱、规则匹配、权限模式和用户确认。Agent Scheduler 不再直接调用 Registry 执行工具，而是先把 tool call 交给权限网关判定；如果允许，再执行工具；如果拒绝，则生成结构化工具错误；如果需要确认，则通过 Agent 事件把请求交给 TUI，等待用户选择后继续。

整体分层如下：

```text
cmd/onecode
  加载 provider 配置
  确定项目根目录
  加载权限规则配置
  创建 permission.Manager
  创建 registry、agent、tui

tui
  渲染权限确认请求
  接收用户选择：拒绝 / 仅本次允许 / 本会话允许 / 永久允许
  把选择回传给 Agent
  保持 ESC 取消当前任务

agent
  继续负责 ReAct loop 和工具调度
  在执行每个工具前调用 permission.Manager
  权限拒绝转成 tool_result 错误，不直接终止 loop
  权限确认通过事件交给 TUI，并等待确认结果

permission
  新增核心权限包
  内置危险黑名单
  实现路径沙箱
  加载用户级 / 项目级 / 本地级规则
  维护会话临时规则
  根据规则优先级和权限模式做决策
  提供确认请求和确认结果处理

tools
  工具本身语义不变
  Registry 继续负责注册、查找、执行
  Registry.Execute 只在权限允许后被调用

config
  继续加载 LLM provider 配置
  Phase 5 可新增 permission 配置加载入口或独立 permission config loader
```

权限判断主链路如下：

```text
LLM 返回 tool calls
  -> Agent Scheduler 遍历 tool call
  -> permission.Manager.Authorize(tool call)
       1. 内置黑名单
       2. 路径沙箱
       3. 会话临时规则
       4. 本地级规则
       5. 项目级规则
       6. 用户级规则
       7. 权限模式默认策略
       8. 需要时请求用户确认
  -> allow: 调用 Registry.Execute
  -> deny: 生成 ToolResult{IsError: true, Content: 权限拒绝原因}
  -> ask: Agent 发出权限确认事件，等待 TUI 回传选择，再继续
```

五层防御对应如下：

```text
第一层：危险黑名单
  不可配置，最高优先级，主要覆盖 Bash 高危命令。

第二层：路径沙箱
  限制 read/write/edit/glob/grep 的显式路径或搜索根必须位于项目根内。

第三层：规则引擎
  加载用户级、项目级、本地级和会话级规则，按优先级匹配工具名和参数模式。

第四层：权限模式
  Strict / Default / Permissive / Bypass 决定规则未命中时的默认行为。

第五层：人在回路
  对需要用户判断的工具调用暂停执行，等用户选择后继续。
```

几个关键设计边界：

- 黑名单和路径沙箱是硬边界，任何规则和模式都不能绕过。
- Bash 不做路径沙箱，因为无法可靠静态解析任意 shell 命令的文件访问。
- 永久允许只写入本地级规则，并且只生成精确匹配规则。
- 本阶段只覆盖 6 个内置工具，不处理 MCP 或外部插件工具。
- 权限拒绝是工具错误结果，不是 Agent 系统错误。
- Plan Mode 的工具可见范围仍然优先于权限系统；权限系统不能让 Plan Mode 执行写类工具。

## 核心数据结构

### permission.Mode

```go
type Mode string

const (
	ModeStrict     Mode = "strict"
	ModeDefault    Mode = "default"
	ModePermissive Mode = "permissive"
	ModeBypass     Mode = "bypass"
)
```

`Mode` 表示权限系统的整体信任档位。它只在黑名单、沙箱和明确规则都没有给出最终结论时生效。

### permission.Action

```go
type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
	ActionAsk   Action = "ask"
)
```

`Action` 表示一次权限判断的结果。

- `allow`：允许执行工具。
- `deny`：拒绝执行工具。
- `ask`：需要用户确认。

规则配置文件中只允许 `allow` 和 `deny`，`ask` 只由权限模式或运行时策略产生。

### permission.Scope

```go
type Scope string

const (
	ScopeSession Scope = "session"
	ScopeLocal   Scope = "local"
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
	ScopeMode    Scope = "mode"
	ScopeBuiltin Scope = "builtin"
)
```

`Scope` 表示某个决策来自哪一层，方便解释、测试和 UI 展示。

### permission.Rule

```go
type Rule struct {
	Tool    string
	Pattern string
	Action  Action
	Scope   Scope
}
```

`Rule` 是解析后的权限规则。

例如：

```text
Bash(git status): allow
```

会解析成：

```go
Rule{
	Tool: "Bash",
	Pattern: "git status",
	Action: ActionAllow,
	Scope: ScopeProject,
}
```

### permission.RuleSet

```go
type RuleSet struct {
	Scope Scope
	Rules []Rule
}
```

`RuleSet` 表示某一层规则文件或会话临时规则集合。

权限判断时按：

```text
session -> local -> project -> user
```

依次检查多个 `RuleSet`。

### permission.Config

```go
type Config struct {
	Mode  Mode
	Rules []RawRule
}
```

`Config` 是 YAML 文件结构。

建议 YAML 格式：

```yaml
mode: default
rules:
  - "Bash(git status): allow"
  - "Bash(rm -rf *): deny"
  - "ReadFile(src/**/*.go): allow"
```

`mode` 只在用户级配置或本地级配置中有意义。项目级配置可以包含 mode 字段，但实现应优先使用更靠近本机的 mode，避免团队规则强行改变用户信任档位。

### permission.RawRule

```go
type RawRule string
```

`RawRule` 是 YAML 中的原始规则字符串。解析阶段会转成 `Rule`。

选择字符串形式而不是结构体 YAML，是为了贴合用户要求的：

```text
工具名(模式): allow
```

### permission.Request

```go
type Request struct {
	ID       string
	Tool     string
	Args     map[string]interface{}
	Safety   tools.Safety
	Mode     agent.Mode
	ProjectRoot string
}
```

`Request` 是一次工具执行前的权限判断输入。

字段含义：

- `ID` 对应 LLM tool call ID。
- `Tool` 是工具名，例如 `bash`、`read_file`。
- `Args` 是模型生成的工具参数。
- `Safety` 是工具安全分类。
- `Mode` 是当前 Agent 模式，用于保留 Plan Mode 禁用逻辑或解释。
- `ProjectRoot` 是启动时确定的单一项目根。

为了避免 `permission` 反向依赖 `agent` 包，实际实现中可以用字符串或本地枚举表示 Agent Mode。

### permission.Target

```go
type Target struct {
	Kind       TargetKind
	Value      string
	Path       string
	Command    string
	Glob       string
	SearchRoot string
}
```

`Target` 是从工具名和参数中提取出来的可匹配对象。

例如：

- Bash：`Command = args["command"]`
- ReadFile/EditFile/WriteFile：`Path = args["path"]`
- Glob：`SearchRoot = args["path"]`，`Glob = args["pattern"]`
- Grep：`SearchRoot = args["path"]`，`Glob = args["glob"]`

`Value` 是规则匹配时使用的主字符串：Bash 使用 command，文件类工具使用 path，搜索类工具使用 search root 或 glob。

### permission.TargetKind

```go
type TargetKind string

const (
	TargetCommand TargetKind = "command"
	TargetPath    TargetKind = "path"
	TargetSearch  TargetKind = "search"
)
```

### permission.Decision

```go
type Decision struct {
	Action  Action
	Reason  string
	Scope   Scope
	Rule    *Rule
	Request ConfirmationRequest
}
```

`Decision` 是权限判断输出。

- `Action=allow`：可以执行工具。
- `Action=deny`：不执行工具，`Reason` 写入工具错误结果。
- `Action=ask`：需要把 `Request` 发给 TUI 进行确认。

### permission.ConfirmationRequest

```go
type ConfirmationRequest struct {
	ID          string
	Tool        string
	ArgsPreview string
	Target      string
	Risk        string
	Reason      string
}
```

`ConfirmationRequest` 是给 TUI 展示的确认请求。

### permission.ConfirmationChoice

```go
type ConfirmationChoice string

const (
	ChoiceDeny         ConfirmationChoice = "deny"
	ChoiceAllowOnce    ConfirmationChoice = "allow_once"
	ChoiceAllowSession ConfirmationChoice = "allow_session"
	ChoiceAllowForever ConfirmationChoice = "allow_forever"
)
```

### permission.ConfirmationResponse

```go
type ConfirmationResponse struct {
	RequestID string
	Choice    ConfirmationChoice
}
```

### permission.Manager

```go
type Manager struct {
	mode        Mode
	projectRoot string
	rules       []RuleSet
	session     RuleSet
	blacklist   Blacklist
	sandbox     Sandbox
	store       Store
	confirmer   Confirmer
}
```

`Manager` 是权限系统主入口，Agent 只和它交互。

### permission.Blacklist

```go
type Blacklist struct {
	patterns []DangerPattern
}
```

`Blacklist` 内置高危正则，不暴露配置入口。

### permission.DangerPattern

```go
type DangerPattern struct {
	Name    string
	Pattern *regexp.Regexp
	Message string
}
```

### permission.Sandbox

```go
type Sandbox struct {
	Root string
}
```

`Sandbox` 负责路径规范化、符号链接解析和根目录边界判断。

### permission.Store

```go
type Store interface {
	Load(ctx context.Context) ([]RuleSet, Mode, error)
	AppendLocalRule(ctx context.Context, rule Rule) error
}
```

`Store` 负责加载用户级、项目级、本地级配置，并在“永久允许”时追加本地级规则。

### permission.Confirmer

```go
type Confirmer interface {
	Confirm(ctx context.Context, req ConfirmationRequest) (ConfirmationResponse, error)
}
```

`Confirmer` 把权限系统和具体 UI 解耦。测试可以用 mock confirmer，TUI 可以实现事件驱动 confirmer。

## 核心接口

### permission.NewManager

```go
func NewManager(opts ManagerOptions) (*Manager, error)
```

创建权限管理器，加载规则、初始化黑名单和沙箱。

### permission.ManagerOptions

```go
type ManagerOptions struct {
	Mode        Mode
	ProjectRoot string
	Store       Store
	Confirmer   Confirmer
}
```

### permission.Authorize

```go
func (m *Manager) Authorize(ctx context.Context, req Request) Decision
```

只做同步决策，不进行用户确认。返回值可能是 `allow`、`deny` 或 `ask`。

### permission.Resolve

```go
func (m *Manager) Resolve(ctx context.Context, req Request) Decision
```

完整权限决策入口：

1. 调用 `Authorize`。
2. 如果是 `ask`，通过 `Confirmer` 请求用户确认。
3. 根据确认结果返回最终 `allow` 或 `deny`。
4. 对本会话允许和永久允许写入规则。

Agent Scheduler 推荐调用 `Resolve`，这样无需自己理解确认流程。

### permission.MatchRule

```go
func MatchRule(rule Rule, req Request, target Target) bool
```

判断规则是否命中本次工具调用。

### permission.ParseRule

```go
func ParseRule(raw string, scope Scope) (Rule, error)
```

解析：

```text
工具名(模式): allow
```

### permission.Sandbox.CheckPath

```go
func (s Sandbox) CheckPath(path string, opts PathCheckOptions) (string, error)
```

检查单个路径是否在沙箱内，返回解析后的绝对路径。

### permission.PathCheckOptions

```go
type PathCheckOptions struct {
	AllowMissingLeaf bool
}
```

`AllowMissingLeaf=true` 用于 `write_file` 创建新文件。

### agent.Event 扩展

```go
const (
	EventPermissionRequest EventType = ...
)
```

```go
type PermissionEvent struct {
	ID          string
	Tool        string
	ArgsPreview string
	Target      string
	Risk        string
	Reason      string
}
```

Agent 需要把确认请求转成事件给 TUI。

### tui Permission 消息

TUI 内部可新增消息类型：

```go
type permissionResponseMsg struct {
	RequestID string
	Choice    permission.ConfirmationChoice
}
```

用于把用户选择回传给权限确认等待点。

## 模块设计

### permission

**职责：**

`permission` 是 Phase 5 新增的核心包，负责所有权限决策。它不依赖 TUI、不依赖具体 LLM provider，也不直接执行工具。

主要职责：

- 内置不可配置危险黑名单。
- 实现项目根路径沙箱。
- 解析权限规则字符串。
- 加载多层规则配置。
- 维护会话临时规则。
- 根据规则优先级和权限模式输出 allow / deny / ask。
- 调用 Confirmer 完成人在回路确认。
- 将本会话允许和永久允许转为规则。
- 为拒绝和确认请求生成可读原因。

**对外接口：**

```go
func NewManager(opts ManagerOptions) (*Manager, error)

func (m *Manager) Resolve(ctx context.Context, req Request) Decision

func (m *Manager) Authorize(ctx context.Context, req Request) Decision

func ParseRule(raw string, scope Scope) (Rule, error)

func MatchRule(rule Rule, req Request, target Target) bool
```

**内部拆分：**

```text
manager.go
  Manager、ManagerOptions、Resolve、Authorize、规则优先级主流程

types.go
  Mode、Action、Scope、Rule、Request、Decision、Confirmation 类型

blacklist.go
  内置危险命令黑名单和匹配逻辑

sandbox.go
  路径规范化、符号链接解析、边界判断

target.go
  从工具名和参数中提取 Target

rules.go
  ParseRule、MatchRule、规则排序和精确规则生成

store.go
  Store 接口、YAML 配置加载、AppendLocalRule

confirmer.go
  Confirmer 接口、NoopConfirmer 或 DenyConfirmer 测试实现

manager_test.go / sandbox_test.go / rules_test.go / blacklist_test.go
  核心权限测试
```

**权限判断顺序：**

```text
Authorize(req):
  target = ExtractTarget(req)
  if blacklist matches target:
      return deny(scope=builtin)
  if target requires sandbox check:
      if sandbox rejects:
          return deny(scope=builtin)
  for ruleset in [session, local, project, user]:
      if matching rule exists:
          return allow/deny(scope=ruleset.scope)
  return defaultByMode(req)
```

`Resolve(req)` 在 `Authorize(req)` 基础上处理 ask：

```text
decision = Authorize(req)
if decision != ask:
    return decision

response = confirmer.Confirm(decision.Request)
switch response.choice:
  deny:
    return deny
  allow_once:
    return allow
  allow_session:
    add exact allow rule to session rules
    return allow
  allow_forever:
    append exact allow rule to local config
    return allow
```

**默认权限模式策略：**

建议初始策略如下：

```text
Strict:
  read_file / glob / grep -> ask
  write_file / edit_file / bash -> ask

Default:
  read_file / glob / grep -> allow
  write_file / edit_file / bash -> ask

Permissive:
  read_file / glob / grep -> allow
  write_file / edit_file -> allow
  bash -> ask

Bypass:
  read_file / glob / grep -> allow
  write_file / edit_file / bash -> allow
```

注意：

- 以上策略只在规则未命中时生效。
- 黑名单和沙箱永远先执行。
- Plan Mode 禁用工具仍由 Agent mode 层处理，不被权限模式改变。

**规则匹配策略：**

工具名采用配置友好名称，建议支持大小写不敏感匹配：

```text
Bash       -> bash
ReadFile   -> read_file
WriteFile  -> write_file
EditFile   -> edit_file
Glob       -> glob
Grep       -> grep
```

模式匹配：

- 若模式不包含 glob 元字符，则先做精确匹配。
- 若模式包含 `*`、`?`、`[`、`]` 或 `**`，则做 glob 匹配。
- Bash 命令匹配完整命令字符串。
- 文件类工具匹配规范化后的项目相对路径。
- Glob/Grep 同时可匹配搜索根和工具自身 glob pattern；本阶段优先使用搜索根作为主 target，保留 glob 字段用于规则匹配补充。
- 人在回路生成规则时只生成精确匹配，不自动生成通配规则。

**沙箱策略：**

- 根目录在启动时确定，解析成绝对真实路径。
- 所有文件工具的路径先转换为绝对路径。
- 已存在路径使用符号链接解析后的真实路径判断。
- 不存在路径使用最近存在父目录解析后拼回目标路径。
- Windows 下比较时使用大小写不敏感路径比较。
- 目录遍历沿用现有搜索工具行为，不额外跟随目录符号链接。
- Bash 不做路径沙箱。

### agent

**职责：**

`agent` 继续负责 ReAct loop 和工具调度，但每次真正执行工具前必须经过权限系统。

主要职责：

- 持有 `*permission.Manager`。
- 在 `executeOneTool` 前构造 `permission.Request`。
- 调用 `permission.Manager.Resolve`。
- 对 `deny` 返回结构化 `llm.ToolResult{IsError:true}`。
- 对 `allow` 调用 `registry.Execute`。
- 对权限确认请求通过事件交给 TUI。
- 保持工具结果顺序与 tool call 一一对应。
- 权限拒绝不计为“未知工具”，但如果连续全是权限拒绝，可以复用 bad tool 上限或新增拒绝上限防止循环。

**对外接口变化：**

```go
func New(p llm.Provider, r *tools.Registry, opts ...Option) *Agent
```

推荐把 Phase4 的可选 prompt runtime 和 Phase5 permission manager 都收敛成 Option：

```go
type Option func(*Agent)

func WithPromptRuntime(runtime *prompt.Runtime) Option
func WithPermissionManager(manager *permission.Manager) Option
```

这样避免构造函数参数不断膨胀，同时保持旧调用兼容。

**事件扩展：**

```go
EventPermissionRequest
```

Agent 发出该事件后，会在 `permission.Confirmer` 的实现中等待用户响应。

一个可行实现是：

- Agent 在每次运行时向 `permission.Manager` 注入事件驱动 confirmer。
- permission.Manager 调用 `confirmer.Confirm(ctx, req)`。
- Agent confirmer 把请求封装成 `EventPermissionRequest` 发给 TUI。
- TUI 渲染确认 UI，并通过 `Agent.RespondPermission` 把用户选择写回 response channel。
- `Confirm` 返回，Agent 继续。

这样 Agent 不需要知道 Bubble Tea 细节。

### tools

**职责：**

`tools` 本身不内置权限逻辑，避免每个工具重复判断。但需要配合权限系统提供稳定的参数约定。

主要职责：

- 保持 6 个工具 schema 和执行逻辑不变。
- Registry 继续提供 `Safety(name)`。
- Registry 继续只在 Agent 允许后执行工具。
- 测试确认权限拒绝时 `Registry.Execute` 没有被调用。

**为什么不把权限放进每个 Tool：**

- 黑名单、规则层级、确认流程是横切逻辑。
- 如果放进 Tool，会让每个工具重复处理权限配置。
- Agent 调度需要知道拒绝结果并回写给模型，放在 Agent/permission 边界更自然。

### tui

**职责：**

TUI 负责人在回路的交互展示和用户选择。

主要职责：

- 渲染权限确认请求。
- 提供四个选择：拒绝、仅本次允许、本会话允许、永久允许。
- 在等待确认时保持界面响应。
- 通过 `Agent.RespondPermission` 把选择传回 Agent confirmer。
- ESC 取消当前任务时，也取消等待中的确认。
- 不直接执行权限判断。
- 不直接修改规则文件。

**交互建议：**

初版可以用简化交互，不必做复杂弹窗：

```text
Permission required

Tool: bash
Target: git status
Reason: Bash command requires confirmation in Default mode.

[d] deny   [o] once   [s] session   [f] forever
```

用户按键后返回选择。

TUI 需要新增状态，例如：

```go
statePermissionConfirm
```

或在 `stateStreaming` 下增加 `pendingPermission` 字段。建议新增状态更清晰。

### config / permission store

**职责：**

配置层负责从固定位置读取权限规则。

建议路径：

```text
用户级:
  ~/.onecode/permissions.yaml

项目级:
  <project>/.onecode/permissions.yaml

本地级:
  <project>/.onecode/permissions.local.yaml
```

本地级适合加入 `.gitignore`，但本阶段不自动修改 `.gitignore`。

YAML 示例：

```yaml
mode: default
rules:
  - "Bash(git status): allow"
  - "Bash(git diff *): allow"
  - "WriteFile(**/*.env): deny"
```

加载规则：

- 不存在：忽略。
- 存在但格式错误：返回错误，启动失败。
- mode 冲突：优先本地级，再用户级；项目级 mode 默认不覆盖用户/本地偏好。
- rules 全部按所在文件 scope 标注。

### cmd/onecode

**职责：**

入口层负责创建权限系统依赖。

启动流程新增：

```text
cwd = os.Getwd()
permissionStore = permission.NewFileStore(userPath, projectPath, localPath)
permissionManager = permission.NewManager({
  ProjectRoot: cwd,
  Store: permissionStore,
  Confirmer: tuiConfirmer 或稍后注入
})
registry = tools.NewRegistry()
agent = agent.New(provider, registry, agent.WithPermissionManager(permissionManager))
```

由于 TUI 需要参与确认，最终 wiring 可能是：

```text
main 创建 registry/provider config
tui.New 内部创建 permission.Manager + agent
```

或者：

```text
main 创建 permission.Manager，但本轮运行的 confirmer 由 Agent 注入
```

推荐由 TUI 组装 permission manager 和 Agent，再由 Agent 在 `Run` 时注入事件 confirmer。main 只负责传入 project root 和配置路径。

## 模块交互

### 启动加载流程

Phase 5 启动时需要在进入 TUI 前准备权限配置路径和项目根。

```text
main
  -> cwd = os.Getwd()
  -> load LLM config
  -> create tools.Registry
  -> tui.New(providers, registry, PermissionBootstrap{ProjectRoot: cwd})
```

TUI 初始化 provider 后创建权限管理器和 Agent：

```text
tui.New / provider selected
  -> store = permission.NewFileStore(userPath, projectPath, localPath)
  -> manager = permission.NewManager({
       Mode: default,
       ProjectRoot: cwd,
       Store: store,
     })
  -> agent.New(provider, registry,
       agent.WithPermissionManager(manager),
     )
```

如果权限配置加载失败：

```text
permission.NewManager -> error
tui.New/provider select -> 显示错误并退出
```

避免在权限配置损坏时进入不安全状态。

### 工具执行权限流程

现有 Agent 调度流程：

```text
LLM tool call
  -> scheduler
  -> registry.Execute
  -> tool result
  -> conversation.AddToolResult
```

Phase 5 后变成：

```text
LLM tool call
  -> scheduler
  -> executeOneTool
  -> build permission.Request
  -> permission.Manager.Resolve
       -> allow / deny / ask
  -> allow: registry.Execute
  -> deny: synthesize permission denied tool result
  -> ask: UI confirmer waits for user, then allow/deny
  -> tool result
  -> conversation.AddToolResult
```

权限拒绝结果示例：

```text
权限拒绝: Bash 命令需要确认，但用户拒绝执行。
```

或者：

```text
权限拒绝: 路径超出项目沙箱: ../secrets.txt
```

该结果以 `ToolResult.IsError=true` 回写给模型。

### 权限判断详细顺序

```text
Resolve(req)
  -> target = ExtractTarget(req)

  -> Blacklist.Check(target)
       if deny: return deny(scope=builtin)

  -> Sandbox.Check(target)
       if deny: return deny(scope=builtin)

  -> RuleEngine.Match(session rules)
       if hit: return rule action(scope=session)

  -> RuleEngine.Match(local rules)
       if hit: return rule action(scope=local)

  -> RuleEngine.Match(project rules)
       if hit: return rule action(scope=project)

  -> RuleEngine.Match(user rules)
       if hit: return rule action(scope=user)

  -> ModePolicy.Decide(mode, req)
       allow / deny / ask

  -> if ask:
       Confirmer.Confirm(req)
       apply choice
```

黑名单和沙箱永远先于规则。
权限模式只在所有明确规则未命中后兜底。

### 黑名单数据流

Bash 请求：

```text
ToolCall{Name: "bash", Input: {"command": "rm -rf /"}}
  -> ExtractTarget => Target{Kind: command, Command: "rm -rf /"}
  -> Blacklist regex match
  -> Decision{Action: deny, Scope: builtin, Reason: "...危险操作..."}
  -> Agent 返回 ToolResult{IsError: true}
```

allow 规则无法绕过：

```yaml
rules:
  - "Bash(rm -rf /): allow"
```

仍然先被黑名单拒绝。

### 路径沙箱数据流

文件读取：

```text
ReadFile("../secret.txt")
  -> ExtractTarget => Target{Kind: path, Path: "../secret.txt"}
  -> Sandbox.CheckPath
       abs path
       eval symlink
       compare with project root
  -> outside root
  -> deny
```

文件创建：

```text
WriteFile("new/dir/file.go")
  -> file 不存在
  -> 找最近存在父目录
  -> 解析父目录真实路径
  -> 拼接剩余路径
  -> 判断仍在 project root 内
```

搜索工具：

```text
Glob(pattern="**/*.go", path="../other")
  -> CheckPath(path)
  -> outside root => deny

Grep(pattern="占位标记", path="src", glob="**/*.go")
  -> CheckPath(path)
  -> Match rules using path and optional glob
```

Bash：

```text
Bash("cat ../secret.txt")
  -> 不走路径沙箱
  -> 由黑名单、规则、模式、用户确认处理
```

### 规则匹配数据流

规则文件：

```yaml
mode: default
rules:
  - "Bash(git *): allow"
  - "WriteFile(**/*.env): deny"
```

解析：

```text
Rule{Tool: "bash", Pattern: "git *", Action: allow, Scope: user}
Rule{Tool: "write_file", Pattern: "**/*.env", Action: deny, Scope: user}
```

匹配：

```text
Bash("git status")
  -> target.Value = "git status"
  -> glob match "git *"
  -> allow

WriteFile(".env")
  -> target.Value = ".env"
  -> glob match "**/*.env" 或 "*.env"
  -> deny
```

对于没有 glob 元字符的规则：

```text
Bash(git status): allow
```

使用精确匹配，避免把它当作前缀。

### 规则层级冲突

示例：

用户级：

```yaml
rules:
  - "Bash(git *): allow"
```

项目级：

```yaml
rules:
  - "Bash(git push *): deny"
```

本地级：

```yaml
rules:
  - "Bash(git push origin dev): allow"
```

本次命令：

```text
git push origin dev
```

结果：

```text
本地级 allow 胜出
项目级 deny 不再生效
用户级 allow 不再继续判断
```

会话级规则由用户确认产生，优先级更高。

### 人在回路确认数据流

权限模式返回 ask：

```text
permission.Manager.Resolve
  -> decision ask
  -> confirmer.Confirm(ctx, request)
```

Agent event confirmer 内部：

```text
Confirm(ctx, req)
  -> emit EventPermissionRequest
  -> wait Agent.RespondPermission response or ctx.Done
```

TUI 收到 request：

```text
agent.EventPermissionRequest
  -> statePermissionConfirm
  -> 展示工具、target、reason、快捷键
  -> 用户按 d/o/s/f
  -> Agent.RespondPermission(ConfirmationResponse)
```

Manager 收到 response：

```text
deny:
  -> deny result

allow_once:
  -> allow current call only

allow_session:
  -> append exact allow rule to session RuleSet
  -> allow current call

allow_forever:
  -> append exact allow rule to local permissions.local.yaml
  -> allow current call
```

### Agent 事件流变化

新增事件：

```text
EventPermissionRequest
```

它不是最终工具结果，只表示“当前工具执行暂停等待用户确认”。

TUI 仍然会收到：

```text
EventToolStart
EventPermissionRequest
EventToolResult
```

推荐顺序：

```text
● bash(command: git status)
Permission required...
用户选择 allow once
  └─ stdout...
```

如果拒绝：

```text
● bash(command: git status)
Permission required...
  └─ ❌ 权限拒绝: 用户拒绝执行 bash
```

### 并发与确认

只读工具当前可以并发执行。加入确认后需要避免多个确认同时弹出导致 UI 混乱。

建议策略：

- 只读工具如果默认 allow，仍然并发。
- 如果某个只读工具需要 ask，则该工具 goroutine 会等待确认。
- Agent event confirmer 内部串行化确认请求，一次只展示一个 pending request。
- 有副作用工具本来就是串行，不会同时出现多个确认。

这样保持 scheduler 结构基本不变，同时避免 UI 同时处理多个确认。

### 取消流程

用户在等待确认时按 ESC：

```text
TUI cancelCurrent()
  -> agent ctx canceled
  -> confirmer.Confirm ctx.Done
  -> permission.Manager.Resolve returns deny/cancel decision
  -> Agent 不执行工具
  -> Agent loop 收到 ctx canceled 后结束当前任务
```

要求：

- 已取消的确认不应继续写入 session/local 规则。
- 用户取消后不应继续执行刚才等待确认的工具。
- TUI 回到 idle。

### 永久允许写入流程

用户选择 forever：

```text
ConfirmationResponse{Choice: allow_forever}
  -> GenerateExactRule(req)
  -> Store.AppendLocalRule(rule)
  -> allow current call
```

生成规则示例：

```text
Bash(git status): allow
ReadFile(src/internal/agent/loop.go): allow
EditFile(src/internal/agent/loop.go): allow
```

不生成：

```text
Bash(git *): allow
ReadFile(src/**/*.go): allow
```

想要通配规则必须用户手写配置文件。

### 启动失败流程

```text
permission config exists but invalid
  -> NewManager returns error
  -> TUI/provider init fails
  -> main prints error or TUI displays error
  -> process exits
```

不存在的配置文件：

```text
permissions.yaml missing
  -> treat as empty rules
  -> continue
```

## 文件组织

```text
src/
├── cmd/
│   └── onecode/
│       └── main.go
└── internal/
    ├── permission/
    │   ├── types.go              — Mode、Action、Scope、Rule、Request、Decision、Confirmation 类型
    │   ├── manager.go            — Manager、Authorize、Resolve、优先级主流程
    │   ├── blacklist.go          — 内置危险命令黑名单
    │   ├── sandbox.go            — 项目根沙箱、路径解析、符号链接处理
    │   ├── target.go             — 从工具名和参数提取命令/路径/搜索 target
    │   ├── rules.go              — ParseRule、MatchRule、精确规则生成
    │   ├── store.go              — YAML 文件加载、AppendLocalRule
    │   ├── confirmer.go          — Confirmer 接口与测试用 confirmer
    │   ├── manager_test.go       — 决策优先级、模式默认策略、确认结果测试
    │   ├── blacklist_test.go     — 黑名单不可绕过测试
    │   ├── sandbox_test.go       — 路径逃逸、符号链接、不存在文件测试
    │   ├── rules_test.go         — 规则解析、glob/精确匹配、层级测试
    │   └── store_test.go         — 配置加载、格式错误、本地写入测试
    ├── agent/
    │   ├── agent.go              — Agent options，注入 permission.Manager
    │   ├── events.go             — EventPermissionRequest、PermissionEvent
    │   ├── scheduler.go          — 工具执行前调用 permission.Manager.Resolve
    │   ├── scheduler_test.go     — 权限拒绝不执行工具、顺序保持、Plan Mode 兼容
    │   └── loop_test.go          — 权限拒绝回写后 loop 继续
    ├── tui/
    │   ├── model.go              — 权限确认 UI 状态、按键处理、permission response wiring
    │   └── styles.go             — 如需要增加权限确认样式
    ├── tools/
    │   └── ...                   — 工具实现保持语义不变
    └── config/
        └── config.go             — 如需要，仅保留 provider 配置；权限配置放 permission.Store
doc/
└── spec/
    └── phase5-permission-system/
        ├── spec.md
        ├── plan.md
        ├── task.md
        └── checklist.md
```

建议新增规则文件路径：

```text
用户级:
  ~/.onecode/permissions.yaml

项目级:
  <project>/.onecode/permissions.yaml

本地级:
  <project>/.onecode/permissions.local.yaml
```

本阶段不自动修改 `.gitignore`，但文档中提醒 `permissions.local.yaml` 适合作为本机私有文件。

## 技术决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 权限入口 | 在 Agent 调度器执行工具前统一判断 | 能覆盖所有内置工具，且拒绝结果可以自然回写给模型 |
| 权限包位置 | 新增 `internal/permission` | 权限是独立横切能力，放在 tools 或 tui 都会耦合过重 |
| 黑名单 | 内置固定正则，不读配置 | 满足不可配置放开的硬边界要求 |
| Bash 路径沙箱 | 不做 | 任意 shell 命令路径静态解析不可靠，避免给用户虚假安全感 |
| 文件沙箱 | 基于启动项目根，解析符号链接后做前缀判断 | 防 `..`、绝对路径、软链接逃逸 |
| 不存在路径 | 解析最近存在父目录，再拼接目标路径判断 | 支持新文件创建，同时防创建路径逃逸 |
| 规则格式 | YAML `rules` 列表里放 `"Tool(pattern): action"` 字符串 | 贴合用户要求，手写简单 |
| 规则匹配 | 无 glob 元字符则精确匹配，有 glob 元字符则 glob 匹配 | 避免 `git status` 意外匹配 `git status --short` |
| 规则优先级 | session > local > project > user > mode | 符合最终确认的优先级，本机/会话规则可覆盖共享规则 |
| 永久允许 | 只写本地级规则，且生成精确匹配 | 避免交互中污染团队共享策略，也避免自动扩大权限 |
| 权限模式 | 固定 Strict / Default / Permissive / Bypass | 满足四档信任等级，不做自定义档位 |
| Mode 冲突 | 本地级 mode 优先，其次用户级；项目级 mode 不强制覆盖 | 权限偏好更像个人/本机信任设置，不应由项目默认强推 |
| 人在回路 | 用 `Confirmer` 接口解耦 permission 和 TUI | 权限核心可测试，UI 可替换 |
| TUI 确认 UI | 新增确认状态或 pendingPermission 字段，快捷键选择 | 初版简单可靠，不做复杂编辑器 |
| 并发确认 | Agent event confirmer 串行化确认请求 | 避免并发只读工具同时弹多个确认造成混乱 |
| 权限拒绝 | 返回 tool result error，不直接终止 loop | 给模型调整策略机会，符合 ReAct 语义 |
| Plan Mode | 仍由 Agent mode 限制工具可见范围和禁用工具 | 权限系统不提升 Plan Mode 权限 |
| 配置加载失败 | 存在但格式错误则启动失败 | 不静默忽略安全配置错误 |
| 测试策略 | permission 包单测 + agent mock 集成 + TUI 编译测试 | 核心安全逻辑不依赖真实 LLM |

## Plan 覆盖检查

| Spec 需求 | 设计归属 |
|-----------|----------|
| F1 统一执行前权限判断 | `agent/scheduler.go` + `permission.Manager.Resolve` |
| F2 危险黑名单 | `permission/blacklist.go` |
| F3 路径沙箱 | `permission/sandbox.go` |
| F4 沙箱覆盖范围 | `permission/target.go` + `sandbox.go` |
| F5 可配置规则 | `permission/rules.go` |
| F6 规则配置层级 | `permission/store.go` + `manager.go` |
| F7 多档权限模式 | `permission.Mode` + mode policy |
| F8 人在回路确认 | `permission.Confirmer` + TUI confirmation state |
| F9 永久允许写入位置 | `Store.AppendLocalRule` |
| F10 拒绝不终止 Agent Loop | `agent/scheduler.go` synthetic tool result |
| F11 TUI 权限交互 | `tui/model.go` + Agent permission event |
| F12 Plan Mode 兼容 | Agent mode check 仍先保留 |
| F13 工具调度兼容 | scheduler 权限接入与测试 |
| F14 规则加载失败 | `permission/store.go` |
| F15 决策可测试 | permission/agent/tui 测试 |

当前设计覆盖 spec 的所有功能需求。主要风险在 TUI 与 Agent goroutine 的确认通道协调，因此实现时应优先把 `Confirmer` 做成可测试的小接口，再接入 Bubble Tea。

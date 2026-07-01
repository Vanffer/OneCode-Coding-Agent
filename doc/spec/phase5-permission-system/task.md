# Permission System Tasks

## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 新建 | `src/internal/permission/types.go` | 权限核心类型 |
| 新建 | `src/internal/permission/rules.go` | 规则解析、工具名归一、规则匹配、精确规则生成 |
| 新建 | `src/internal/permission/target.go` | 从工具调用参数提取命令、路径、搜索目标 |
| 新建 | `src/internal/permission/blacklist.go` | 内置危险命令黑名单 |
| 新建 | `src/internal/permission/sandbox.go` | 项目根路径沙箱 |
| 新建 | `src/internal/permission/store.go` | 多层 YAML 规则加载、本地规则写入 |
| 新建 | `src/internal/permission/confirmer.go` | Confirmer 接口和测试用 confirmer |
| 新建 | `src/internal/permission/manager.go` | Manager、Authorize、Resolve、模式默认策略 |
| 新建 | `src/internal/permission/*_test.go` | 权限核心测试 |
| 修改 | `src/internal/agent/agent.go` | Agent Option、注入 permission.Manager |
| 修改 | `src/internal/agent/events.go` | 权限确认事件类型和结构 |
| 修改 | `src/internal/agent/scheduler.go` | 工具执行前调用权限系统 |
| 修改 | `src/internal/agent/*_test.go` | 更新构造和权限集成测试 |
| 修改 | `src/internal/tui/model.go` | 权限确认 UI 状态、按键和 confirmer wiring |
| 修改 | `src/internal/tui/styles.go` | 如需要增加权限确认样式 |
| 修改 | `src/cmd/onecode/main.go` | 传入项目根，适配 TUI 权限启动参数 |
| 新建 | `doc/spec/phase5-permission-system/task.md` | 本任务拆解 |
| 新建 | `doc/spec/phase5-permission-system/checklist.md` | 验收清单 |

## T1: 定义权限核心类型

**文件：** `src/internal/permission/types.go`  
**依赖：** 无

**步骤：**
1. 定义 `Mode`、`Action`、`Scope`。
2. 定义 `Rule`、`RuleSet`、`Config`、`RawRule`。
3. 定义 `Request`，避免依赖 `agent` 包，Agent mode 用字符串表达。
4. 定义 `Target`、`TargetKind`。
5. 定义 `Decision`。
6. 定义 `ConfirmationRequest`、`ConfirmationChoice`、`ConfirmationResponse`。
7. 为常量添加简洁注释。

**验证：** `go test ./internal/permission` 编译通过。

## T2: 实现规则解析与匹配

**文件：** `src/internal/permission/rules.go`、`src/internal/permission/rules_test.go`  
**依赖：** T1

**步骤：**
1. 实现工具名归一化：`Bash` -> `bash`，`ReadFile` -> `read_file` 等。
2. 实现 `ParseRule(raw, scope)`，解析 `Tool(pattern): allow/deny`。
3. 拒绝无括号、空工具名、空模式、非法 action。
4. 实现 glob 元字符判断。
5. 实现精确匹配和 glob 匹配。
6. 实现 `MatchRule(rule, req, target)`。
7. 实现 `GenerateExactAllowRule(req, target, scope)`，用于 session/forever。
8. 编写测试覆盖解析成功、解析失败、大小写工具名、精确匹配、glob 匹配、Bash 命令匹配、文件路径匹配。

**验证：** `go test ./internal/permission -run TestParseRule` 和 `go test ./internal/permission -run TestMatchRule`

## T3: 实现工具目标提取

**文件：** `src/internal/permission/target.go`、`src/internal/permission/target_test.go`  
**依赖：** T1

**步骤：**
1. 实现 `ExtractTarget(req)`。
2. Bash 从 `command` 提取命令。
3. read_file/write_file/edit_file 从 `path` 提取路径。
4. glob 从 `path` 和 `pattern` 提取搜索根与 glob；path 缺省为 `.`。
5. grep 从 `path`、`pattern`、`glob` 提取搜索根与 glob；path 缺省为 `.`。
6. 对未知工具返回可匹配但风险较高的 target。
7. 编写测试覆盖 6 个内置工具。

**验证：** `go test ./internal/permission -run TestExtractTarget`

## T4: 实现危险命令黑名单

**文件：** `src/internal/permission/blacklist.go`、`src/internal/permission/blacklist_test.go`  
**依赖：** T1、T3

**步骤：**
1. 定义内置 `DangerPattern` 列表。
2. 覆盖典型高危命令：`rm -rf /`、Windows 删除系统目录、格式化磁盘、磁盘擦除、fork bomb 等。
3. 仅对 Bash command target 应用黑名单。
4. 返回可读拒绝原因，不暴露完整正则。
5. 测试黑名单命中。
6. 测试 allow 规则无法绕过黑名单时在 manager 中补充覆盖。

**验证：** `go test ./internal/permission -run TestBlacklist`

## T5: 实现路径沙箱

**文件：** `src/internal/permission/sandbox.go`、`src/internal/permission/sandbox_test.go`  
**依赖：** T1、T3

**步骤：**
1. 定义 `Sandbox`、`PathCheckOptions`。
2. 初始化时解析项目根绝对路径和符号链接。
3. 实现 `CheckPath(path, opts)`。
4. 已存在路径使用真实路径判断。
5. 不存在 leaf 且 `AllowMissingLeaf=true` 时，解析最近存在父目录后拼接目标路径判断。
6. Windows 下做大小写不敏感比较。
7. 防止 `..`、绝对路径、符号链接逃逸。
8. 编写临时目录测试：项目内文件、`..` 逃逸、绝对路径逃逸、符号链接逃逸、不存在新文件路径。

**验证：** `go test ./internal/permission -run TestSandbox`

## T6: 实现权限配置 Store

**文件：** `src/internal/permission/store.go`、`src/internal/permission/store_test.go`  
**依赖：** T1、T2

**步骤：**
1. 定义 `Store` 接口。
2. 实现 `FileStore`，持有 user/project/local 三个路径。
3. 实现不存在文件忽略。
4. 实现 YAML 解析 `mode` 和 `rules`。
5. 按 user/project/local 加载并打上 scope。
6. mode 选择优先 local，其次 user；项目级 mode 不覆盖用户偏好。
7. 配置存在但 YAML 错误或规则错误时返回清晰错误。
8. 实现 `AppendLocalRule`，创建父目录，追加精确规则到本地级 YAML。
9. 写入前校验目标 local 配置路径位于项目 `.onecode` 目录内。
10. 编写测试覆盖缺失文件、合法加载、格式错误、mode 优先级、本地规则追加。

**验证：** `go test ./internal/permission -run TestFileStore`

## T7: 实现 Confirmer 与 Manager

**文件：** `src/internal/permission/confirmer.go`、`src/internal/permission/manager.go`、`src/internal/permission/manager_test.go`  
**依赖：** T1-T6

**步骤：**
1. 定义 `Confirmer` 接口。
2. 实现测试用 `StaticConfirmer` 或 `DenyConfirmer`。
3. 实现 `NewManager`，加载 store、初始化 blacklist/sandbox/session rules。
4. 实现 `Authorize` 主流程：blacklist -> sandbox -> session/local/project/user rules -> mode policy。
5. 实现 `Resolve`：处理 ask 和用户确认结果。
6. `allow_session` 写入 session exact allow rule。
7. `allow_forever` 调用 `AppendLocalRule` 写入 local exact allow rule。
8. 实现四档 mode policy。
9. 编写测试覆盖黑名单优先级、沙箱优先级、规则层级、四档默认策略、确认四种选择、永久只生成精确匹配。

**验证：** `go test ./internal/permission`

## T8: Agent 接入权限系统

**文件：** `src/internal/agent/agent.go`、`src/internal/agent/events.go`、`src/internal/agent/scheduler.go`、`src/internal/agent/scheduler_test.go`、`src/internal/agent/loop_test.go`  
**依赖：** T7

**步骤：**
1. 将 Agent 构造改为 options 模式，同时保持旧调用兼容。
2. 增加 `WithPromptRuntime` 和 `WithPermissionManager`。
3. 增加 `EventPermissionRequest` 和 `PermissionEvent`。
4. 在 `executeOneTool` 中先发送 ToolStart，再调用 permission manager。
5. 权限 allow 时调用 `registry.Execute`。
6. 权限 deny 时不调用 tool，返回 `ToolResult{IsError:true}`。
7. 权限 ask 由 manager/confirmer 完成，Agent 只接收最终 decision。
8. 确认 `ModePlan` 禁用工具逻辑仍先于权限执行。
9. 测试权限拒绝时 fake tool 未执行。
10. 测试权限拒绝回写后 Agent Loop 继续。
11. 测试 Plan Mode 下即使权限允许写工具仍被禁用。
12. 测试结果顺序和 tool call ID 保持对应。

**验证：** `go test ./internal/agent`

## T9: TUI 接入权限确认

**文件：** `src/internal/tui/model.go`、`src/internal/tui/styles.go`  
**依赖：** T7、T8

**步骤：**
1. Agent 实现事件驱动 confirmer，内部向事件流发送权限请求并等待响应 channel。
2. TUI 初始化 provider/agent 时创建 permission manager，并由 Agent 在每次运行时注入事件 confirmer。
3. 增加权限确认状态或 pendingPermission 字段。
4. 收到权限请求时展示确认 UI。
5. 支持按键：`d` 拒绝，`o` 本次允许，`s` 本会话允许，`f` 永久允许。
6. 用户选择后通过 `Agent.RespondPermission` 写回 confirmation response。
7. ESC 取消时取消当前 agent context，并让 pending confirmation 结束。
8. 保持普通 streaming、/plan、/do 流程不变。
9. 如 TUI 自动测试成本高，至少保证编译通过，并用 Agent 事件流测试覆盖核心交互对象。

**验证：** `go test ./...` 编译通过；启动 smoke 能进入 TUI。

## T10: main 传入项目根和权限启动参数

**文件：** `src/cmd/onecode/main.go`、必要时 `src/internal/tui/model.go` 构造函数  
**依赖：** T9

**步骤：**
1. main 中确定启动 cwd 作为 project root。
2. TUI 构造参数增加 project root 或 permission bootstrap。
3. 确保 provider 选择路径和单 provider 自动初始化路径都能创建 permission manager。
4. 权限配置加载失败时显示错误并退出。
5. 保持原启动 banner 和 provider 配置行为。

**验证：** `go test ./...` 编译通过；`go run ./cmd/onecode` smoke。

## T11: 文档与示例补充

**文件：** `doc/spec/phase5-permission-system/task.md`、必要时新增 `doc/spec/phase5-permission-system/examples.md`  
**依赖：** spec/plan 已确认

**步骤：**
1. 写入 task.md。
2. 如需要，补充权限 YAML 示例。
3. 明确 `permissions.local.yaml` 适合作为本机私有配置。
4. 明确永久允许只生成精确规则。
5. 明确 Bash 不做路径沙箱。

**验证：** 人工 review 文档内容和 spec 边界一致。

## T12: 全量验证与清理

**文件：** 全部相关文件  
**依赖：** T1-T11

**步骤：**
1. 运行 `go test ./...`。
2. 修复所有测试和编译问题。
3. 运行 `go run ./cmd/onecode` 启动 smoke。
4. 检查 `src/internal/permission` 中没有遗留待办或占位标记。
5. 检查 `git status --short`。
6. 确认没有生成不应提交的本地权限规则文件。
7. 清理测试缓存或临时文件。

**验证：** 全量测试通过，启动 smoke 成功进入 TUI，工作区无意外生成文件。

## 执行顺序

```text
T1 -> T2 -> T3 -> T4 -> T5 -> T6 -> T7
                                      ↓
                                    T8 -> T9 -> T10 -> T12
                                      ↘
                                       T11 --------↗
```

# Permission System Checklist

> 每一项通过运行代码、测试或观察行为验证，聚焦系统行为。

## 实现完整性

- [ ] 6 个内置工具在执行前都会进入统一权限判断。（验证：运行 `go test ./internal/agent -run TestPermission`）
- [ ] 危险 Bash 命令会被内置黑名单拦截，且无法通过 allow 规则或 Bypass 模式放行。（验证：运行 `go test ./internal/permission -run TestBlacklist`）
- [ ] read_file、write_file、edit_file、glob、grep 的显式路径或搜索根无法逃逸项目根。（验证：运行 `go test ./internal/permission -run TestSandbox`）
- [ ] 不存在的新文件路径会通过最近存在父目录进行沙箱判断。（验证：运行 `go test ./internal/permission -run TestSandboxMissingLeaf`）
- [ ] Bash 不走路径沙箱，只受黑名单、规则、模式和确认流程控制。（验证：运行权限 manager 测试）
- [ ] 权限规则支持 `Tool(pattern): allow/deny` 格式，并能区分精确匹配和 glob 匹配。（验证：运行 `go test ./internal/permission -run TestParseRule` 和 `TestMatchRule`）
- [ ] 规则优先级为 session > local > project > user > mode。（验证：运行 `go test ./internal/permission -run TestRulePrecedence`）
- [ ] Strict、Default、Permissive、Bypass 四档模式按设计给出默认决策，且不能绕过黑名单和沙箱。（验证：运行 `go test ./internal/permission -run TestModePolicy`）
- [ ] 人在回路支持 deny、allow once、allow session、allow forever 四种选择。（验证：运行 `go test ./internal/permission -run TestResolveConfirmation`）
- [ ] allow session 写入会话临时规则；allow forever 写入本地级规则文件并且只生成精确匹配规则。（验证：运行 `go test ./internal/permission -run TestConfirmationRules`）

## 集成

- [ ] 权限拒绝时工具本体不会被调用。（验证：使用 fake tool 运行 agent scheduler 测试）
- [ ] 权限拒绝会生成 `IsError=true` 的 tool result，并回写给模型。（验证：运行 agent loop 权限拒绝测试）
- [ ] 单次权限拒绝不会直接终止 Agent Loop，模型仍可继续下一轮。（验证：使用 mock provider 运行 loop 测试）
- [ ] Plan Mode 下即使规则允许写类工具，也不能执行 write_file、edit_file 或 bash。（验证：运行 Plan Mode 权限兼容测试）
- [ ] 只读工具并发、有副作用工具串行的现有调度行为不被破坏。（验证：运行现有 scheduler 测试）
- [ ] tool result 与原始 tool call ID 保持一一对应。（验证：运行 scheduler 结果顺序测试）
- [ ] TUI 能展示权限确认请求，并把用户选择回传给确认器。（验证：编译测试 + mock/UI confirmer 测试）
- [ ] 等待权限确认时 ESC 会取消当前任务，不会继续执行待确认工具。（验证：mock confirmer 或人工 TUI 场景）
- [ ] 权限配置文件不存在时正常启动；存在但格式错误时启动失败并给出清晰错误。（验证：运行 FileStore 测试）
- [ ] 单 provider 自动初始化和多 provider 选择后初始化都能创建 permission manager。（验证：运行全量测试和启动 smoke）

## 回归

- [ ] 现有 6 个工具的参数 schema 和成功结果格式没有改变。（验证：运行 `go test ./internal/tools`）
- [ ] Phase 3 Agent Loop 测试仍然通过。（验证：运行 `go test ./internal/agent`）
- [ ] Phase 4 Prompt Runtime 测试仍然通过。（验证：运行 `go test ./internal/prompt`）
- [ ] 普通 Execute 模式仍能执行被允许的工具任务。（验证：mock provider 或人工 smoke）
- [ ] `/plan` 和 `/do` 流程仍然可用。（验证：运行全量测试，必要时人工 TUI smoke）
- [ ] ESC 取消非权限确认中的普通任务仍然可用。（验证：运行现有取消测试）

## 编译与测试

- [ ] 权限包单元测试通过。（验证：在 `src` 下运行 `go test ./internal/permission`）
- [ ] Agent 集成测试通过。（验证：运行 `go test ./internal/agent`）
- [ ] Tools 测试通过。（验证：运行 `go test ./internal/tools`）
- [ ] 全项目测试通过。（验证：运行 `go test ./...`）
- [ ] 启动 smoke 能进入 TUI。（验证：运行 `go run ./cmd/onecode`，观察进入交互界面）
- [ ] 没有生成不应提交的本地权限配置文件。（验证：运行 `git status --short`）
- [ ] 没有遗留待办或占位标记。（验证：检查 `src/internal/permission`）

## 端到端场景

- [ ] 场景 1：执行 `Bash(rm -rf /)`，即使存在 allow 规则或 Bypass 模式也被黑名单拒绝。（验证：mock provider 请求该工具，观察 tool result 为权限拒绝）
- [ ] 场景 2：执行 `ReadFile(../secret.txt)`，被路径沙箱拒绝，Agent Loop 不崩溃。（验证：mock provider 或人工工具调用）
- [ ] 场景 3：执行 `WriteFile(new/file.go)`，目标在项目内时沙箱允许继续进入规则/模式判断。（验证：临时目录测试）
- [ ] 场景 4：Default 模式下执行 `bash git status`，未命中规则时触发用户确认。（验证：TUI 或 mock confirmer）
- [ ] 场景 5：用户选择本会话允许后，相同工具调用第二次不再询问。（验证：mock confirmer 计数）
- [ ] 场景 6：用户选择永久允许后，本地级 `permissions.local.yaml` 追加精确规则，不生成通配规则。（验证：检查临时配置文件）
- [ ] 场景 7：项目级 deny 与本地级 allow 冲突时，本地级规则胜出。（验证：FileStore + Manager 测试）
- [ ] 场景 8：Plan Mode 请求写工具，即使权限规则 allow，也仍被 Plan Mode 禁用。（验证：Agent scheduler 测试）
- [ ] 场景 9：等待权限确认时用户按 ESC，当前任务取消，工具不执行。（验证：mock confirmer 或人工 TUI 场景）

## 范围边界

- [ ] 未实现网络请求限制、资源配额、速率限制。（验证：检查代码入口）
- [ ] 未实现持久化审计日志。（验证：检查没有审计存储或日志流水）
- [ ] 黑名单不可配置、不可关闭。（验证：检查没有黑名单配置入口）
- [ ] 只覆盖 6 个内置工具，不覆盖 MCP 或外部插件工具。（验证：检查权限 target 和 registry 接入）
- [ ] Bash 不做路径沙箱。（验证：权限测试覆盖）
- [ ] 不支持多项目根或运行时追加目录授权。（验证：ManagerOptions 只接收单一 ProjectRoot）
- [ ] 不提供规则图形化编辑器。（验证：TUI 只提供确认选择）

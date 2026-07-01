# Phase 5 权限配置示例

权限规则文件按优先级分三层读取：

- 用户级：`~/.onecode/permissions.yaml`
- 项目共享级：`<project>/.onecode/permissions.yaml`
- 项目本地级：`<project>/.onecode/permissions.local.yaml`

判断时的规则优先级是：

```text
session > local > project > user > mode
```

`permissions.local.yaml` 适合放本机私有偏好，例如常用命令自动放行。人在回路里选择 `forever` 时，只会向这个文件追加精确匹配的 allow 规则，不会自动生成 `git *` 这类通配规则。

## 基本格式

```yaml
mode: default
rules:
  - "ReadFile(**/*.go): allow"
  - "Glob(**/*.go): allow"
  - "Grep(**/*.go): allow"
  - "Bash(git status): allow"
  - "Bash(git push *): deny"
  - "EditFile(.env): deny"
```

规则格式固定为：

```text
Tool(pattern): allow|deny
```

`pattern` 没有通配符时按精确匹配处理；包含 `*`、`?` 或 `**` 时按 glob 匹配处理。

## 权限模式

```yaml
mode: strict
```

支持四档：

- `strict`：未命中规则时都询问。
- `default`：只读工具默认允许，有副作用工具询问。
- `permissive`：除 Bash 外默认允许，Bash 仍询问。
- `bypass`：未命中规则时默认允许。

黑名单和路径沙箱不受 `mode` 影响。即使是 `bypass`，危险 Bash 命令和项目根外路径也会被拒绝。

## 常见写法

只允许查看 Go 文件：

```yaml
rules:
  - "ReadFile(**/*.go): allow"
  - "Glob(**/*.go): allow"
  - "Grep(**/*.go): allow"
```

允许常见 Git 只读命令：

```yaml
rules:
  - "Bash(git status): allow"
  - "Bash(git diff): allow"
  - "Bash(git log *): allow"
```

拒绝敏感文件：

```yaml
rules:
  - "ReadFile(.env): deny"
  - "ReadFile(**/.env): deny"
  - "EditFile(**/*.key): deny"
```

Bash 不做路径沙箱，不能靠路径规则限制命令访问文件。Bash 的风险由内置黑名单、显式规则、权限模式和用户确认共同控制。

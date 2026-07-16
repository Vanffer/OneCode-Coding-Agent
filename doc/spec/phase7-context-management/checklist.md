# 上下文管理能力 Checklist

> 每一项通过运行代码或观察行为来验证，聚焦系统行为。

## 实现完整性

- [ ] 单个超大工具结果会被保存到当前项目 `.onecode/context/tool-results/`，对话中只保留预览、路径和重读提示。（验证：运行 ToolResultBounder 单测）
- [ ] 同一轮多个工具结果合计超阈值时，系统优先存盘较大的结果，直到保留内容回到阈值内。（验证：运行 batch bounder 单测）
- [ ] 用户原始消息不会被轻量处理替换或存盘。（验证：运行用户消息保护单测）
- [ ] 压缩摘要包含固定结构。（验证：运行 SummaryBuilder 单测）
- [ ] 压缩后消息包含 summary boundary、近期文件索引和近期原文消息。（验证：运行 Compactor 单测）
- [ ] 压缩请求不传工具，并拒绝模型返回 tool call。（验证：运行 ProviderCompressor 单测）
- [ ] 自动压缩连续失败 3 次后熔断。（验证：运行 Preflight 熔断单测）
- [ ] context-too-long 后只执行一次紧急压缩和一次重试。（验证：运行 Agent 紧急压缩单测）
- [ ] 上下文窗口按 local > provider > inferred > default 解析。（验证：运行 WindowResolver 单测）
- [ ] `.onecode/context/.gitignore` 自动补齐规则且不覆盖用户已有内容。（验证：运行 ProjectStore 单测）

## TUI 与用户可见行为

- [ ] TUI 状态栏常驻展示上下文使用量、窗口上限和百分比。（验证：运行 TUI 状态栏测试）
- [ ] 窗口大小来自推断或默认值时，状态栏展示估算标记。（验证：运行 TUI 状态栏测试）
- [ ] 任意压缩开始、完成、失败、紧急重试和熔断时，TUI 展示提示。（验证：运行 EventContext 处理测试）
- [ ] `/compact` 会触发手动压缩，并在完成后继续使用压缩后的上下文。（验证：运行 TUI 命令测试 + Agent compact 测试）
- [ ] `/context` 能展示窗口来源、使用量和当前状态。（验证：运行 TUI 命令测试）
- [ ] `/context window` 能保存当前项目本地窗口设置，重启后仍生效。（验证：运行 ProjectStore + TUI 命令测试）

## 集成

- [ ] Agent 每次正常请求模型前都会调用 conversation preflight。（验证：运行 Agent loop 测试）
- [ ] 模型返回真实 usage 后，Conversation 会更新 usage anchor。（验证：运行 Agent loop usage 测试）
- [ ] 工具执行语义不变，工具仍返回原始结果；轻量处理只影响后续 LLM 可见消息。（验证：运行 scheduler/loop 现有测试 + bounder 测试）
- [ ] 压缩失败会返回清晰错误，不会无限重试。（验证：运行 Preflight 和 Agent 错误路径测试）
- [ ] 项目本地产物只写入当前项目 `.onecode/context` 下。（验证：运行 ProjectStore 路径校验测试）

## 编译与测试

- [ ] config 测试通过。（验证：`cd src && go test ./internal/config`）
- [ ] conversation 测试通过。（验证：`cd src && go test ./internal/conversation`）
- [ ] agent 测试通过。（验证：`cd src && go test ./internal/agent`）
- [ ] tui 测试通过。（验证：`cd src && go test ./internal/tui`）
- [ ] 全项目测试通过。（验证：`cd src && go test ./...`）

## 端到端场景

- [ ] 场景 1：用户让 Agent 读取一个超大文件后继续提问；系统将大工具结果存盘，TUI 显示上下文用量，模型可根据路径重新读取细节。（验证：本地手动运行 OneCode，观察工具结果提示和 `.onecode/context/tool-results/`）
- [ ] 场景 2：长对话接近窗口上限时，系统自动压缩，TUI 显示压缩开始和完成，后续请求继续使用摘要和近期原文。（验证：用测试 provider 构造高 usage，观察事件和消息历史）
- [ ] 场景 3：用户执行 `/compact`，系统生成结构化摘要并保留最近消息，TUI 显示完成提示。（验证：本地手动运行或 TUI 命令测试）
- [ ] 场景 4：用户通过 `/context window` 修改窗口大小，退出重启后当前项目继续使用该值。（验证：检查 `.onecode/context/local.yaml` 并重新启动）
- [ ] 场景 5：provider 第一次请求返回上下文过长，系统紧急压缩并只重试一次；二次失败时展示错误。（验证：Agent 测试 provider 模拟错误）

# 工具系统 Checklist

> 每一项通过运行代码或观察行为来验证，聚焦系统行为。

## 实现完整性

- [ ] **[tools/tool.go]** Tool 接口和 Result 结构定义正确（验证：`go build ./internal/tools/...`）
- [ ] **[tools/registry.go]** Registry 注册、查找、List 顺序、Execute 超时/panic 捕获（验证：单测覆盖）
- [ ] **[tools/read_file.go]** 读文件带行号、文件不存在返回 IsError、超长截断（验证：单测三种场景）
- [ ] **[tools/write_file.go]** 写文件成功、父目录自动创建、覆盖写（验证：单测 + 检查磁盘）
- [ ] **[tools/edit_file.go]** 唯一替换成功、0 匹配报错、多匹配报错含 N 处提示（验证：单测三种场景）
- [ ] **[tools/bash.go]** 执行命令返回 stdout/stderr/exit_code、超时返回 IsError、非零退出不标 IsError（验证：单测三种场景）
- [ ] **[tools/glob.go]** glob 匹配返回路径列表、无匹配返回空说明（验证：单测）
- [ ] **[tools/grep.go]** 正则搜索返回 file:line:content、正则非法返回 IsError、无命中返回空说明（验证：单测）
- [ ] **[llm/provider.go]** ToolDefinition/ToolCall/ToolResult 定义正确、Message/StreamEvent 扩展、Provider.Stream 签名变更（验证：`go build ./internal/llm/...`）
- [ ] **[llm/anthropic.go]** 注入工具定义、流式 tool_use 解析、工具结果回灌（验证：手动测试 Anthropic 协议）
- [ ] **[llm/openai.go]** 注入工具定义、流式 tool_use 解析、工具结果回灌、空参数归一（验证：手动测试 OpenAI 协议）
- [ ] **[conversation/conversation.go]** AddAssistantWithToolCalls、AddToolResult 正确（验证：单测）
- [ ] **[agent/agent.go]** Agent.New、Run 单轮闭环、Event/ToolEvent 正确（验证：单测 mock provider）
- [ ] **[prompt/system.txt]** 包含 Agent 角色说明和工具使用约定（验证：内容审查）
- [ ] **[tui/model.go]** submit 走 agent.Run、事件泵处理工具事件（验证：手动测试）
- [ ] **[tui/styles.go]** 工具行样式定义（验证：编译通过）
- [ ] **[main.go]** Registry 构造和注入（验证：`go build`）

## 集成

- [ ] **[AC1]** 注册中心能列出全部已注册工具并按名查找；导出的工具定义随请求发送（验证：单测 + 抓取请求体含 6 个工具定义）
- [ ] **[AC8]** 一次需要用工具的提问下，能从流式回复中拼接出完整的工具名与 JSON 参数（验证：问「读 X 文件」，观察解析出的工具调用正确）
- [ ] **[AC9]** 端到端：问「读 X 文件并总结」→ 模型调用读文件 → 结果回灌 → 模型据此给出最终文本总结（验证：跑通整条链路，答复体现文件内容）
- [ ] **[AC10]** 本章只执行一轮工具调用；第一轮结果回灌后若模型仍请求工具，不再发起新一轮（验证：给需连续两步工具的任务，观察在一轮工具后停止）
- [ ] **[AC11]** 分别用 Anthropic 与 OpenAI 配置跑同一组工具任务，触发、展示、结果回灌、错误反馈行为一致（验证：两种协议各跑一次相同任务）

## 编译与测试

- [ ] **项目编译无错误** `go build ./...`
- [ ] **所有单元测试通过** `go test ./...`
- [ ] **无 vet 警告** `go vet ./...`

## 端到端场景

- [ ] **[AC2]** 读文件：问「读 internal/llm/provider.go 的内容」→ 工具行展示 `● read_file(...)` → 返回带行号的文件内容 → 模型据此回复
- [ ] **[AC3]** 写文件：问「创建一个 test.txt 写入 hello」→ 工具行展示 `● write_file(...)` → 文件落盘 → 模型确认
- [ ] **[AC4]** 改文件：问「把 test.txt 里的 hello 改成 world」→ 工具行展示 `● edit_file(...)` → 替换成功 → 模型确认
- [ ] **[AC5]** 执行命令：问「运行 go version」→ 工具行展示 `● bash(...)` → 返回版本信息 → 模型据此回复
- [ ] **[AC6]** 搜索：问「找所有 .go 文件」→ 工具行展示 `● glob(...)` → 返回文件列表 → 模型据此回复
- [ ] **[AC7]** 内容搜索：问「搜索哪里用到了 strings.Builder」→ 工具行展示 `● grep(...)` → 返回匹配位置 → 模型据此回复
- [ ] **[AC12]** 工具行展示：工具调用以 `● 工具名(关键参数)` 展示，结果过长时截断，纳入 scrollback（验证：跑一次工具任务后回滚查看）
- [ ] **[AC13]** 错误处理：故意触发各类工具失败（不存在文件、命令非零退出、改文件匹配不到），均以结构化结果回灌且 UI 可区分提示，程序不崩溃、会话可继续（验证：逐个触发后再正常发一条）
- [ ] **[AC14]** 向后兼容：无工具调用的纯文本对话行为与改动前一致（验证：不触发工具的普通聊天正常）
- [ ] **[AC15]** 结果体量控制：超大文件/超长命令输出/海量搜索结果被截断处理，不撑爆界面或上下文（验证：读大文件、跑长输出命令观察截断）
- [ ] **[AC16]** 密钥安全：API 密钥不出现在对话区或任何用户可见输出中（验证：检查所有输出路径）

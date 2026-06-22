# OneCode Coding Agent - 基础对话功能 Tasks

## 文件清单

| 操作 | 文件 | 职责 |
|------|------|------|
| 新建 | `go.mod` | Go 模块定义 |
| 新建 | `cmd/onecode/main.go` | 入口：config.Load → banner → tui.New → Run |
| 新建 | `internal/config/config.go` | ProviderConfig、Config、Load、校验 |
| 新建 | `internal/llm/provider.go` | Provider 接口、Message、StreamEvent 定义 |
| 新建 | `internal/llm/factory.go` | New 工厂函数 |
| 新建 | `internal/llm/anthropic.go` | Anthropic 适配器 |
| 新建 | `internal/llm/openai.go` | OpenAI 适配器 |
| 新建 | `internal/conversation/conversation.go` | Conversation 结构体 |
| 新建 | `internal/prompt/prompt.go` | SystemPrompt、DogBanner、RenderBanner |
| 新建 | `internal/prompt/system.txt` | 内置 system prompt（embed） |
| 新建 | `internal/prompt/banner.txt` | ASCII 柴犬（embed） |
| 新建 | `internal/tui/model.go` | Model 定义、状态机、New、Run |
| 新建 | `internal/tui/update.go` | Update 逻辑、消息处理 |
| 新建 | `internal/tui/view.go` | View 渲染 |
| 新建 | `internal/tui/provider_select.go` | Provider 选择列表组件 |
| 新建 | `internal/tui/input.go` | textarea 封装 |
| 新建 | `internal/tui/conversation.go` | 对话区渲染（glamour） |
| 新建 | `internal/tui/status.go` | 状态栏 |
| 新建 | `internal/tui/timer.go` | spinner + 计时 |
| 新建 | `.onecode/config.yaml` | 配置文件 |
| 新建 | `README.md` | 项目说明 |

## T1: 项目初始化

**文件：** `go.mod`
**依赖：** 无
**步骤：**
1. 在 `basic-chat/` 目录下执行 `go mod init onecode`
2. 创建目录结构：`cmd/onecode/`、`internal/config/`、`internal/llm/`、`internal/conversation/`、`internal/prompt/`、`internal/tui/`

**验证：** `go mod tidy` 通过，目录结构存在

---

## T2: 配置模块

**文件：** `internal/config/config.go`
**依赖：** T1
**步骤：**
1. 定义 `ProviderConfig` 结构体（Name、Protocol、BaseURL、APIKey、Model、Thinking）
2. 定义 `Config` 结构体（Providers []ProviderConfig）
3. 实现 `Load(path string) (*Config, error)`：
   - 读取 YAML 文件
   - 校验：列表非空；每项 name/protocol/api_key/model 非空；protocol ∈ {anthropic, openai}
   - 返回可读错误（指明哪个 provider 的哪个字段）

**验证：** 编写测试用例，验证正常配置加载和各种错误情况的报错信息

---

## T3: Prompt 模块

**文件：** `internal/prompt/prompt.go`、`internal/prompt/system.txt`、`internal/prompt/banner.txt`
**依赖：** T1
**步骤：**
1. 创建 `banner.txt`：ASCII art 柴犬图案
2. 创建 `system.txt`：内置 system prompt
3. 实现 `prompt.go`：
   - `SystemPrompt` 常量（embed system.txt）
   - `DogBanner` 常量（embed banner.txt）
   - `RenderBanner(version, cwd string) string`

**验证：** 调用 `RenderBanner("0.1.0", "/home/user")` 输出包含柴犬、版本号、工作目录

---

## T4: LLM 类型定义

**文件：** `internal/llm/provider.go`
**依赖：** T1
**步骤：**
1. 定义 `Message` 结构体（Role string、Content string）
2. 定义 `StreamEvent` 结构体（Text string、Done bool、Err error）
3. 定义 `Provider` 接口：
   - `Name() string`
   - `Model() string`
   - `Stream(ctx context.Context, msgs []Message) <-chan StreamEvent`

**验证：** 编译通过

---

## T5: Conversation 模块

**文件：** `internal/conversation/conversation.go`
**依赖：** T4
**步骤：**
1. 定义 `Conversation` 结构体（messages []llm.Message）
2. 实现 `AddUser(text string)`
3. 实现 `AddAssistant(text string)`
4. 实现 `Messages() []llm.Message`

**验证：** 编写测试用例，验证 AddUser/AddAssistant 后 Messages() 返回正确的顺序

---

## T6: Anthropic 适配器

**文件：** `internal/llm/anthropic.go`
**依赖：** T3、T4、T5
**步骤：**
1. 定义 `anthropicProvider` 结构体（cfg、client、name、model）
2. 实现 `Name() string`、`Model() string`
3. 实现 `Stream(ctx, msgs) <-chan StreamEvent`：
   - 把 []Message 转为 SDK 的 MessageParam
   - 注入 System=内置 prompt
   - 按 cfg.Thinking 设 ThinkingConfig
   - 内部起 goroutine：NewStreaming 迭代
   - 取 TextDelta → StreamEvent.Text
   - 遇 ThinkingDelta 丢弃
   - 结束发 Done，错误发 Err
   - base_url 非空时 option.WithBaseURL 覆盖
   - ctx.Done() 时停止；channel 在结束/出错后关闭

**验证：** 使用 mock API 或真实 API 测试流式输出，验证 Text/Done/Err 事件正确发送

---

## T7: OpenAI 适配器

**文件：** `internal/llm/openai.go`
**依赖：** T3、T4、T5
**步骤：**
1. 定义 `openaiProvider` 结构体（cfg、client、name、model）
2. 实现 `Name() string`、`Model() string`
3. 实现 `Stream(ctx, msgs) <-chan StreamEvent`：
   - 把 []Message 转为 ChatCompletionMessageParamUnion
   - 首条插入 SystemMessage(内置 prompt)
   - 内部起 goroutine：NewStreaming 迭代
   - 取 Choices[0].Delta.Content → StreamEvent.Text
   - 结束/出错按 stream.Err() 发 Done/Err
   - base_url 非空时 WithBaseURL 覆盖
   - ctx.Done() 时停止；channel 在结束/出错后关闭

**验证：** 使用 mock API 或真实 API 测试流式输出，验证 Text/Done/Err 事件正确发送

---

## T8: LLM 工厂函数

**文件：** `internal/llm/factory.go`
**依赖：** T6、T7
**步骤：**
1. 实现 `New(cfg config.ProviderConfig) (Provider, error)`：
   - 按 cfg.Protocol 分发：anthropic → newAnthropicProvider，openai → newOpenAIProvider
   - 未知协议返回错误

**验证：** 传入 anthropic/openai 配置返回对应 Provider，传入未知协议返回错误

---

## T9: TUI Model 定义

**文件：** `internal/tui/model.go`
**依赖：** T4、T5、T8
**步骤：**
1. 定义 `sessionState` 类型和常量（stateSelecting、stateIdle、stateStreaming）
2. 定义 `Model` 结构体（state、textarea、spinner、list、renderer、providers、provider、conv、events、curReply、turnStart、width、height）
3. 实现 `New(providers []config.ProviderConfig) Model`
4. 实现 `Run() error`（创建 tea.Program 并运行）
5. 实现 Init() tea.Cmd

**验证：** 编译通过

---

## T10: TUI Input 组件

**文件：** `internal/tui/input.go`
**依赖：** T9
**步骤：**
1. 封装 textarea.Model
2. 配置样式（提示符 ❯、占位文字 "Send a message..."）
3. 支持 Alt+Enter 换行、Enter 提交

**验证：** 编译通过

---

## T11: TUI Status 组件

**文件：** `internal/tui/status.go`
**依赖：** T9
**步骤：**
1. 实现状态栏渲染
2. 左侧显示 provider.Name()
3. 右侧显示 provider.Model()
4. 中间显示状态（就绪/输出中...）

**验证：** 编译通过

---

## T12: TUI Timer 组件

**文件：** `internal/tui/timer.go`
**依赖：** T9
**步骤：**
1. 封装 spinner.Model
2. 实现计时逻辑：turnStart + spinner tick 计算 elapsed
3. 显示格式："Imagining… (Ns)" 或 "Done. N.Ns"

**验证：** 编译通过

---

## T13: TUI Conversation 渲染

**文件：** `internal/tui/conversation.go`
**依赖：** T9
**步骤：**
1. 初始化 glamour.TermRenderer（WordWrap=width）
2. 实现渲染函数：将 Markdown 文本转为终端美化输出

**验证：** 编译通过

---

## T14: TUI Provider 选择列表

**文件：** `internal/tui/provider_select.go`
**依赖：** T9
**步骤：**
1. 封装 list.Model
2. 实现列表项（显示 provider name + model）
3. 支持方向键导航、Enter 确认

**验证：** 编译通过

---

## T15: TUI Update 逻辑

**文件：** `internal/tui/update.go`
**依赖：** T10、T11、T12、T13、T14
**步骤：**
1. 实现 `streamMsg` 类型定义
2. 实现 `waitForEvent(ch) tea.Cmd`
3. 实现 `Update(msg, model) (tea.Model, tea.Cmd)`：
   - 窗口大小变化处理
   - stateSelecting：方向键移动、Enter 选定 → llm.New → stateIdle
   - stateIdle：Enter 提交 → conv.AddUser → provider.Stream → stateStreaming；/exit → tea.Quit
   - stateStreaming：spinner.TickMsg → 更新计时；streamMsg(Text) → curReply 追加；streamMsg(Done) → glamour 渲染 → tea.Println → conv.AddAssistant → stateIdle；streamMsg(Err) → tea.Println 错误 → stateIdle

**验证：** 编译通过

---

## T16: TUI View 渲染

**文件：** `internal/tui/view.go`
**依赖：** T15
**步骤：**
1. 实现 `View() string`：
   - stateSelecting：渲染 provider 选择列表
   - stateIdle：渲染输入框 + 状态栏
   - stateStreaming：渲染输入框 + 底部动态区（curReply）+ spinner 计时 + 状态栏

**验证：** 编译通过

---

## T17: 主入口

**文件：** `cmd/onecode/main.go`
**依赖：** T2、T3、T9
**步骤：**
1. 定义 version 常量
2. 调用 config.Load 加载配置（失败打印可读错误、非零退出）
3. 调用 prompt.RenderBanner 打印启动横幅
4. 调用 tui.New(providers) 创建 Model
5. 调用 tui.Run() 启动 TUI

**验证：** `go build ./cmd/onecode/` 编译通过

---

## T18: 配置文件

**文件：** `.onecode/config.yaml`
**依赖：** T1
**步骤：**
1. 创建示例配置文件，包含一个 anthropic provider 和一个 openai provider
2. api_key 留空，提示用户填入

**验证：** 文件存在，YAML 格式正确

---

## T19: README

**文件：** `README.md`
**依赖：** T17
**步骤：**
1. 项目简介
2. 安装与构建
3. 配置说明
4. 使用说明

**验证：** 文件存在

## 执行顺序

```
T1 → T2 → T3 → T4 → T5
                     ↘
T6 → T7 → T8 → T9 → T10
                  ↘
T11 → T12 → T13 → T14 → T15 → T16 → T17 → T18 → T19
```

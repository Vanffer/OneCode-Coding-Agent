package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"onecode/internal/agent"
	"onecode/internal/config"
	"onecode/internal/conversation"
	"onecode/internal/llm"
	"onecode/internal/tools"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
)

// sessionState 表示 TUI 的会话状态
type sessionState int

const (
	stateSelecting sessionState = iota // 多 provider 时的选择界面
	stateIdle                          // 等待用户输入
	stateStreaming                     // 等待/接收模型流（spinner+计时）
)

// Model 是 TUI 的主模型
type Model struct {
	state           sessionState
	textarea        textarea.Model
	spinner         spinner.Model
	selectIndex     int // provider 选择索引
	renderer        *glamour.TermRenderer
	providers       []config.ProviderConfig
	provider        llm.Provider
	agent           *agent.Agent
	registry        *tools.Registry
	conv            *conversation.Conversation
	agentEvents     <-chan agent.Event // 当前 agent 事件
	curReply        *strings.Builder   // 本轮 assistant 增量缓冲
	pendingChars    int                // 待渲染字符数（用于批量更新）
	pendingMarkdown string             // 待打印的 markdown
	turnStart       time.Time          // 计时起点
	width, height   int
	ready           bool  // 界面是否已初始化
	err             error // 错误信息
}

// agentEventMsg 包装 agent.Event 用于 tea.Msg
type agentEventMsg agent.Event

// tickMsg 定时触发的消息，用于批量更新
type tickMsg time.Time

// doneMsg 流式完成后的延迟消息
type doneMsg struct{}

// New 创建新的 TUI 模型
func New(providers []config.ProviderConfig, registry *tools.Registry) Model {
	// 创建 textarea
	ta := textarea.New()
	ta.Placeholder = "Send a message..."
	ta.Focus()
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	// 创建 spinner
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	// 创建 glamour renderer
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithWordWrap(80),
	)

	// 如果只有一个 provider，直接使用它；否则进入选择状态
	var provider llm.Provider
	var ag *agent.Agent
	state := stateSelecting
	if len(providers) == 1 {
		p, err := llm.New(providers[0])
		if err == nil {
			provider = p
			ag = agent.New(provider, registry)
			state = stateIdle
		}
	}

	return Model{
		state:     state,
		textarea:  ta,
		spinner:   s,
		renderer:  renderer,
		providers: providers,
		provider:  provider,
		agent:     ag,
		registry:  registry,
		conv:      conversation.New(),
		curReply:  &strings.Builder{},
		width:     80,
		height:    24,
	}
}

// Init 初始化模型
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
	)
}

// renderTick 启动定时渲染（每 50ms 触发一次，用于批量更新）
func renderTick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// waitForAgentEvent 读取一个 agent 事件并返回 agentEventMsg
func waitForAgentEvent(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return agentEventMsg{Done: true}
		}
		return agentEventMsg(event)
	}
}

// Update 处理消息
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textarea.SetWidth(msg.Width - 4)
		if m.renderer != nil {
			m.renderer, _ = glamour.NewTermRenderer(
				glamour.WithWordWrap(msg.Width - 4),
			)
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}

		// 处理 provider 选择
		if m.state == stateSelecting {
			switch msg.String() {
			case "up", "k":
				if m.selectIndex > 0 {
					m.selectIndex--
				}
				return m, nil
			case "down", "j":
				if m.selectIndex < len(m.providers)-1 {
					m.selectIndex++
				}
				return m, nil
			case "enter":
				// 选择 provider
				selected := m.providers[m.selectIndex]
				provider, err := llm.New(selected)
				if err != nil {
					m.err = err
					return m, tea.Quit
				}
				m.provider = provider
				m.agent = agent.New(provider, m.registry)
				m.state = stateIdle
				return m, nil
			}
			return m, nil
		}

		// 处理 idle 状态的输入
		if m.state == stateIdle {
			switch msg.String() {
			case "enter":
				input := strings.TrimSpace(m.textarea.Value())
				if input == "" {
					return m, nil
				}

				// 检查退出命令
				if input == "/exit" {
					return m, tea.Quit
				}

				// 检查 agent 是否已初始化
				if m.agent == nil {
					m.err = fmt.Errorf("no provider selected")
					return m, tea.Quit
				}

				// 添加用户消息
				m.conv.AddUser(input)

				// 提交用户消息到 scrollback
				cmds = append(cmds, tea.Println(fmt.Sprintf("\n❯ %s\n", input)))

				// 清空输入框
				m.textarea.Reset()

				// 启动 agent 单轮闭环
				ctx := context.Background()
				m.agentEvents = m.agent.Run(ctx, m.conv)
				m.curReply.Reset()
				m.turnStart = time.Now()
				m.state = stateStreaming

				// 启动事件监听、spinner 和定时渲染
				cmds = append(cmds, waitForAgentEvent(m.agentEvents), m.spinner.Tick, renderTick())
				return m, tea.Batch(cmds...)
			}
		}

		// 处理流式状态的按键
		if m.state == stateStreaming {
			// 流式状态下不处理输入
			return m, nil
		}

		// 处理 textarea 输入
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)

	case tea.PasteMsg:
		// 粘贴内容传递给 textarea
		if m.state == stateIdle {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			cmds = append(cmds, cmd)
		}

	case spinner.TickMsg:
		if m.state == stateStreaming {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case tickMsg:
		// 定时渲染：流式状态下每 50ms 强制重绘一次
		if m.state == stateStreaming && m.pendingChars > 0 {
			m.pendingChars = 0
		}
		if m.state == stateStreaming {
			cmds = append(cmds, renderTick())
		}

	case agentEventMsg:
		// 处理 agent 事件
		switch {
		case msg.Err != nil:
			// 错误处理
			m.err = msg.Err
			cmds = append(cmds, tea.Println(fmt.Sprintf("\n❌ Error: %s\n", msg.Err.Error())))
			m.state = stateIdle
			m.textarea.Focus()
			return m, tea.Batch(cmds...)

		case msg.Text != "":
			// 文本增量
			m.curReply.WriteString(msg.Text)
			m.pendingChars++
			cmds = append(cmds, waitForAgentEvent(m.agentEvents))

		case msg.Tool != nil:
			// 工具事件
			switch msg.Tool.Phase {
			case agent.PhaseStart:
				// 工具开始：展示工具行
				toolLine := fmt.Sprintf("\n● %s(%s)\n", msg.Tool.Name, msg.Tool.Args)
				cmds = append(cmds, tea.Println(toolLine))
			case agent.PhaseEnd:
				// 工具结束：展示结果摘要
				resultLine := fmt.Sprintf("  └─ %s\n", msg.Tool.Result)
				if msg.Tool.IsError {
					resultLine = fmt.Sprintf("  └─ ❌ %s\n", msg.Tool.Result)
				}
				cmds = append(cmds, tea.Println(resultLine))
			}
			cmds = append(cmds, waitForAgentEvent(m.agentEvents))

		case msg.Done:
			// 流式完成
			m.pendingChars = 0
			if m.curReply.Len() > 0 {
				rendered, err := m.renderer.Render(m.curReply.String())
				if err != nil {
					rendered = m.curReply.String()
				}
				elapsed := time.Since(m.turnStart).Seconds()
				m.pendingMarkdown = fmt.Sprintf("\n%s\n⏱  %.1fs\n", rendered, elapsed)
			}
			// 延迟 50ms 后打印 markdown
			return m, tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
				return doneMsg{}
			})
		}

	case doneMsg:
		// 打印延迟的 markdown
		if m.pendingMarkdown != "" {
			cmds = append(cmds, tea.Println(m.pendingMarkdown))
			m.pendingMarkdown = ""
		}
		m.state = stateIdle
		m.textarea.Focus()
		return m, tea.Batch(cmds...)
	}

	return m, tea.Batch(cmds...)
}

// View 渲染视图
func (m Model) View() tea.View {
	if m.err != nil {
		return tea.NewView(fmt.Sprintf("Error: %s\n", m.err.Error()))
	}

	switch m.state {
	case stateSelecting:
		return tea.NewView(m.viewSelecting())
	case stateIdle:
		return tea.NewView(m.viewIdle())
	case stateStreaming:
		return tea.NewView(m.viewStreaming())
	default:
		return tea.NewView("")
	}
}

// viewSelecting 渲染 provider 选择界面
func (m Model) viewSelecting() string {
	var s strings.Builder
	s.WriteString("\n  Select a provider:\n\n")

	for i, p := range m.providers {
		cursor := "  "
		if i == m.selectIndex {
			cursor = "❯ "
		}
		s.WriteString(fmt.Sprintf("%s%s (%s)\n", cursor, p.Name, p.Model))
	}

	s.WriteString("\n  ↑/↓: navigate  enter: select  ctrl+c: quit\n")
	return s.String()
}

// viewIdle 渲染空闲状态
func (m Model) viewIdle() string {
	var s strings.Builder

	// 状态栏
	s.WriteString(statusBar(m.provider, "Ready", m.width))
	s.WriteString("\n")

	// 输入框
	s.WriteString(fmt.Sprintf("❯ %s\n", m.textarea.View()))

	return s.String()
}

// viewStreaming 渲染流式输出状态
func (m Model) viewStreaming() string {
	var s strings.Builder

	// 状态栏
	elapsed := time.Since(m.turnStart).Seconds()
	status := fmt.Sprintf("%s %.1fs", m.spinner.View(), elapsed)
	s.WriteString(statusBar(m.provider, status, m.width))
	s.WriteString("\n")

	// 显示当前流式内容（原始文本，不渲染 markdown）
	if m.curReply.Len() > 0 {
		s.WriteString("\n")
		s.WriteString(m.curReply.String())
		s.WriteString("\n")
	}

	// 输入框（禁用状态）
	s.WriteString("❯ ...\n")

	return s.String()
}

// statusBar 渲染状态栏
func statusBar(provider llm.Provider, status string, width int) string {
	if provider == nil {
		return ""
	}
	left := fmt.Sprintf(" %s ", provider.Name())
	right := fmt.Sprintf(" %s ", provider.Model())
	middle := fmt.Sprintf(" %s ", status)

	// 计算填充
	padding := width - len(left) - len(right) - len(middle)
	if padding < 0 {
		padding = 0
	}

	return fmt.Sprintf("%s%s%s%s%s",
		statusBarLeftStyle.Render(left),
		statusBarMiddleStyle.Render(middle),
		statusBarRightStyle.Render(strings.Repeat(" ", padding)),
		statusBarRightStyle.Render(right),
		"",
	)
}

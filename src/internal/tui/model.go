package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"onecode/internal/agent"
	"onecode/internal/config"
	"onecode/internal/conversation"
	"onecode/internal/llm"
	"onecode/internal/memory"
	"onecode/internal/permission"
	"onecode/internal/prompt"
	"onecode/internal/tools"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// sessionState 表示 TUI 的会话状态
type sessionState int

const (
	stateSelecting          sessionState = iota // 多 provider 时的选择界面
	stateIdle                                   // 等待用户输入
	stateStreaming                              // 等待/接收模型流（spinner+计时）
	statePermissionConfirm                      // 等待用户确认工具权限
	stateContextWindowInput                     // 输入上下文窗口大小
	stateSessionLoading                         // 扫描或恢复历史会话
	stateSessionPicker                          // 选择要恢复的历史会话
)

const mainInputPlaceholder = "Send a message..."

// Model 是 TUI 的主模型
type Model struct {
	state              sessionState
	textarea           textarea.Model
	spinner            spinner.Model
	selectIndex        int // provider 选择索引
	renderer           *glamour.TermRenderer
	providers          []config.ProviderConfig
	provider           llm.Provider
	agent              *agent.Agent
	registry           *tools.Registry
	projectRoot        string
	conv               *conversation.Conversation
	agentEvents        <-chan agent.Event // 当前 agent 事件
	cancelCurrent      context.CancelFunc // 当前 agent run 的取消函数
	pendingPerm        *agent.PermissionEvent
	permissionFeedback string
	permSelectIndex    int
	pendingPlan        *PendingPlan // /plan 生成、/do 消费的待执行计划
	currentMode        agent.Mode   // 当前运行模式
	progressStatus     string       // Agent 进度状态栏文案
	contextUsage       conversation.UsageEstimate
	contextWindow      conversation.WindowInfo
	contextStatus      string
	instructionLoader  *memory.InstructionLoader
	instructions       memory.InstructionSet
	sessionStore       *memory.SessionStore
	journal            *memory.SessionJournal
	noteStore          *memory.NoteStore
	memoryWorker       *memory.Worker
	sessionID          string
	pendingResumeGap   string
	turnMessages       []llm.Message
	resumeSessions     []memory.SessionInfo
	resumeSelectIndex  int
	now                func() time.Time
	curReply           *strings.Builder // 本轮 assistant 增量缓冲
	pendingMarkdown    string           // 待打印的 markdown
	turnStart          time.Time        // 计时起点
	width, height      int
	ready              bool  // 界面是否已初始化
	err                error // 错误信息
}

// PendingPlan 保存 /plan 生成的待执行计划，仅保留在当前进程内存中。
type PendingPlan struct {
	Content   string
	CreatedAt time.Time
	Consumed  bool
}

// MemoryDependencies groups concrete persistent-memory services without
// introducing another manager abstraction.
type MemoryDependencies struct {
	InstructionLoader *memory.InstructionLoader
	Instructions      memory.InstructionSet
	Sessions          *memory.SessionStore
	Notes             *memory.NoteStore
	Worker            *memory.Worker
}

// agentEventMsg 包装 agent.Event 用于 tea.Msg
type agentEventMsg agent.Event

// doneMsg 流式完成后的延迟消息
type doneMsg struct{}

type memoryErrorMsg struct {
	err    error
	closed bool
}

type sessionListMsg struct {
	sessions []memory.SessionInfo
	err      error
}

type sessionRestoreMsg struct {
	result  memory.RestoreResult
	journal *memory.SessionJournal
	err     error
}

// New 创建新的 TUI 模型
func New(providers []config.ProviderConfig, registry *tools.Registry, projectRoot string, memoryDeps MemoryDependencies) Model {
	// 创建 textarea
	ta := textarea.New()
	ta.Placeholder = mainInputPlaceholder
	ta.Focus()
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	// 创建 spinner
	s := spinner.New()
	s.Spinner = spinner.Dot

	// 创建 glamour renderer
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithWordWrap(80),
	)

	// 如果只有一个 provider，直接使用它；否则进入选择状态
	var provider llm.Provider
	var ag *agent.Agent
	var initErr error
	state := stateSelecting
	if len(providers) == 1 {
		p, err := llm.New(providers[0])
		if err == nil {
			provider = p
			ag, initErr = newAgent(provider, registry, projectRoot, providers[0].ContextWindow)
			if initErr == nil {
				state = stateIdle
			}
		} else {
			initErr = err
		}
	}

	conv := conversation.New(conversation.WithContextOptions(conversation.ContextOptions{
		ProjectRoot: projectRoot,
	}))
	contextState := conv.ContextState()

	return Model{
		state:             state,
		textarea:          ta,
		spinner:           s,
		renderer:          renderer,
		providers:         providers,
		provider:          provider,
		agent:             ag,
		registry:          registry,
		projectRoot:       projectRoot,
		conv:              conv,
		contextUsage:      contextState.Usage,
		contextWindow:     contextState.Window,
		instructionLoader: memoryDeps.InstructionLoader,
		instructions:      memoryDeps.Instructions,
		sessionStore:      memoryDeps.Sessions,
		noteStore:         memoryDeps.Notes,
		memoryWorker:      memoryDeps.Worker,
		curReply:          &strings.Builder{},
		width:             80,
		height:            24,
		err:               initErr,
	}
}

func newAgent(provider llm.Provider, registry *tools.Registry, projectRoot string, providerWindow int) (*agent.Agent, error) {
	if projectRoot == "" {
		projectRoot = "."
	}
	manager, err := permission.NewManager(permission.ManagerOptions{
		ProjectRoot: projectRoot,
		Store:       permission.DefaultFileStore(projectRoot),
	})
	if err != nil {
		return nil, err
	}
	return agent.New(provider, registry,
		agent.WithPermissionManager(manager),
		agent.WithContextOptions(conversation.ContextOptions{
			ProjectRoot:    projectRoot,
			ProviderName:   provider.Name(),
			ModelName:      provider.Model(),
			ProviderWindow: providerWindow,
		}),
	), nil
}

// Init 初始化模型
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink}
	if m.memoryWorker != nil {
		cmds = append(cmds, waitForMemoryError(m.memoryWorker.Errors()))
	}
	return tea.Batch(cmds...)
}

// waitForAgentEvent 读取一个 agent 事件并返回 agentEventMsg
func waitForAgentEvent(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return agentEventMsg{
				Type: agent.EventDone,
				Done: &agent.DoneEvent{Reason: agent.StopModelDone},
			}
		}
		return agentEventMsg(event)
	}
}

func waitForMemoryError(ch <-chan error) tea.Cmd {
	return func() tea.Msg {
		err, ok := <-ch
		return memoryErrorMsg{err: err, closed: !ok}
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
				ag, err := newAgent(provider, m.registry, m.projectRoot, selected.ContextWindow)
				if err != nil {
					m.err = err
					return m, tea.Quit
				}
				m.agent = ag
				m.state = stateIdle
				return m, nil
			}
			return m, nil
		}

		if m.state == stateSessionPicker {
			switch msg.String() {
			case "up", "k":
				m.moveSessionSelection(-1)
				return m, nil
			case "down", "j":
				m.moveSessionSelection(1)
				return m, nil
			case "enter":
				return m.startSelectedSessionRestore()
			case "esc":
				m.state = stateIdle
				m.textarea.Focus()
				return m, nil
			}
			return m, nil
		}

		if m.state == stateSessionLoading {
			return m, nil
		}

		if m.state == statePermissionConfirm {
			switch msg.String() {
			case "up", "k", "shift+tab":
				m.movePermissionSelection(-1)
				return m, nil
			case "down", "j", "tab":
				m.movePermissionSelection(1)
				return m, nil
			case "enter", " ":
				next, cmd := m.confirmSelectedPermission()
				return next, cmd
			case "1":
				next, cmd := m.answerPermission(permission.ChoiceAllowOnce)
				return next, cmd
			case "2":
				next, cmd := m.answerPermission(permission.ChoiceAllowSession)
				return next, cmd
			case "3":
				next, cmd := m.answerPermission(permission.ChoiceAllowForever)
				return next, cmd
			case "4":
				next, cmd := m.answerPermission(permission.ChoiceDeny)
				return next, cmd
			case "5":
				next, cmd := m.cancelPermissionRun()
				return next, cmd
			case "d":
				next, cmd := m.answerPermission(permission.ChoiceDeny)
				return next, cmd
			case "o":
				next, cmd := m.answerPermission(permission.ChoiceAllowOnce)
				return next, cmd
			case "s":
				next, cmd := m.answerPermission(permission.ChoiceAllowSession)
				return next, cmd
			case "f":
				next, cmd := m.answerPermission(permission.ChoiceAllowForever)
				return next, cmd
			case "esc":
				next, cmd := m.cancelPermissionRun()
				return next, cmd
			}
			return m, nil
		}

		if m.state == stateContextWindowInput {
			switch msg.String() {
			case "enter":
				next, cmd := m.applyContextWindowInput()
				return next, cmd
			case "esc":
				m.state = stateIdle
				m.textarea.Reset()
				m.textarea.Placeholder = mainInputPlaceholder
				m.textarea.Focus()
				return m, nil
			}
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
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

				// 提交用户消息到 scrollback
				cmds = append(cmds, tea.Println(fmt.Sprintf("\n❯ %s\n", input)))

				// 清空输入框
				m.textarea.Reset()

				if next, cmd, handled := m.handleSlashCommand(input); handled {
					m = next
					cmds = append(cmds, cmd)
					return m, tea.Batch(cmds...)
				}

				next, cmd := m.startAgentRun(input, agent.ModeExecute, agent.RunOptions{Mode: agent.ModeExecute})
				m = next
				cmds = append(cmds, cmd)
				return m, tea.Batch(cmds...)
			}
		}

		// 处理流式状态的按键
		if m.state == stateStreaming {
			if msg.String() == "esc" {
				next, cmd := m.cancelAgentRun()
				return next, cmd
			}
			return m, nil
		}

		// 处理 textarea 输入
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)

	case tea.PasteMsg:
		// 粘贴内容传递给 textarea
		if m.state == stateIdle || m.state == stateContextWindowInput {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			cmds = append(cmds, cmd)
		}

	case spinner.TickMsg:
		if isActiveRunState(m.state) {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case agentEventMsg:
		// 处理 agent 事件
		switch msg.Type {
		case agent.EventPermissionRequest:
			if msg.Permission != nil {
				m.pendingPerm = msg.Permission
				m.permissionFeedback = ""
				m.permSelectIndex = 0
				m.progressStatus = "Waiting for permission"
				m.state = statePermissionConfirm
			}
			return m, tea.Batch(cmds...)

		case agent.EventError:
			// 错误处理
			if msg.Err != nil {
				cmds = append(cmds, tea.Println(fmt.Sprintf("\n❌ Error: %s\n", msg.Err.Error())))
			}
			if m.pendingPlan != nil && m.pendingPlan.Consumed {
				m.pendingPlan = nil
			}
			m.cancelCurrent = nil
			m.pendingPerm = nil
			m.permissionFeedback = ""
			m.turnMessages = nil
			m.state = stateIdle
			m.textarea.Focus()
			return m, tea.Batch(cmds...)

		case agent.EventText:
			// 文本增量
			m.curReply.WriteString(msg.Text)
			cmds = append(cmds, waitForAgentEvent(m.agentEvents))

		case agent.EventToolStart:
			// 工具事件
			if msg.Tool != nil {
				toolLine := fmt.Sprintf("\n● %s(%s)\n", msg.Tool.Name, msg.Tool.Args)
				cmds = append(cmds, tea.Println(toolLine))
			}
			cmds = append(cmds, waitForAgentEvent(m.agentEvents))

		case agent.EventToolResult:
			if msg.Tool != nil {
				resultLine := fmt.Sprintf("  └─ %s\n", msg.Tool.Result)
				if msg.Tool.IsError {
					resultLine = fmt.Sprintf("  └─ ❌ %s\n", msg.Tool.Result)
				}
				cmds = append(cmds, tea.Println(resultLine))
			}
			cmds = append(cmds, waitForAgentEvent(m.agentEvents))

		case agent.EventProgress:
			if msg.Progress != nil {
				m.progressStatus = msg.Progress.Message
			}
			if m.state == statePermissionConfirm && m.pendingPerm == nil {
				m.permissionFeedback = ""
				m.state = stateStreaming
			}
			cmds = append(cmds, waitForAgentEvent(m.agentEvents))

		case agent.EventUsage:
			// 用量事件目前只更新状态，不额外打印；不可用时不显示伪造数字。
			if msg.Usage != nil && msg.Usage.Available {
				m.progressStatus = fmt.Sprintf("tokens %d", msg.Usage.TotalTokens)
				if msg.Usage.CacheAvailable {
					m.progressStatus = fmt.Sprintf(
						"tokens %d cache read %d/create %d",
						msg.Usage.TotalTokens,
						msg.Usage.CacheReadInputTokens,
						msg.Usage.CacheCreationInputTokens,
					)
				}
			}
			cmds = append(cmds, waitForAgentEvent(m.agentEvents))

		case agent.EventContext:
			if msg.Context != nil {
				m.contextUsage = msg.Context.Usage
				state := m.conv.ContextState()
				m.contextWindow = state.Window
				m.contextStatus = msg.Context.Message
				if shouldPrintContextEvent(msg.Context.Kind) && msg.Context.Message != "" {
					cmds = append(cmds, tea.Println(fmt.Sprintf("\n%s\n", msg.Context.Message)))
				}
			}
			cmds = append(cmds, waitForAgentEvent(m.agentEvents))

		case agent.EventConversation:
			if msg.Conversation != nil {
				if err := m.persistConversationEvent(msg.Conversation); err != nil {
					cmds = append(cmds, tea.Println(fmt.Sprintf("\nWarning: %s\n", err)))
				}
			}
			cmds = append(cmds, waitForAgentEvent(m.agentEvents))

		case agent.EventCancelled:
			m.progressStatus = "Cancelled"
			cmds = append(cmds, tea.Println("\n任务已取消"))
			cmds = append(cmds, waitForAgentEvent(m.agentEvents))

		case agent.EventDone:
			// 流式完成
			if m.currentMode == agent.ModePlan && msg.Done != nil && msg.Done.Reason == agent.StopModelDone && m.curReply.Len() > 0 {
				m.pendingPlan = &PendingPlan{
					Content:   m.curReply.String(),
					CreatedAt: time.Now(),
				}
			}
			if m.pendingPlan != nil && m.pendingPlan.Consumed {
				m.pendingPlan = nil
			}
			if msg.Done != nil && msg.Done.Reason == agent.StopModelDone && len(m.turnMessages) > 0 && m.memoryWorker != nil && m.provider != nil {
				m.memoryWorker.Enqueue(m.provider, memory.TurnCandidate{
					SessionID: m.sessionID,
					Messages:  append([]llm.Message(nil), m.turnMessages...),
					StoppedAt: m.clock(),
				})
			}
			m.turnMessages = nil
			m.cancelCurrent = nil
			m.pendingPerm = nil
			m.permissionFeedback = ""
			m.progressStatus = ""
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

	case memoryErrorMsg:
		if msg.err != nil {
			cmds = append(cmds, tea.Println(fmt.Sprintf("\nWarning: 自动记忆更新失败: %s\n", msg.err)))
		}
		if !msg.closed && m.memoryWorker != nil {
			cmds = append(cmds, waitForMemoryError(m.memoryWorker.Errors()))
		}

	case sessionListMsg:
		if msg.err != nil {
			m.state = stateIdle
			m.textarea.Focus()
			return m, tea.Println(fmt.Sprintf("\n恢复会话列表失败: %s\n", msg.err))
		}
		if len(msg.sessions) == 0 {
			m.state = stateIdle
			m.textarea.Focus()
			return m, tea.Println("\n没有可恢复的历史会话。")
		}
		m.resumeSessions = msg.sessions
		m.resumeSelectIndex = 0
		m.state = stateSessionPicker
		return m, nil

	case sessionRestoreMsg:
		if msg.err != nil {
			m.state = stateIdle
			m.textarea.Focus()
			return m, tea.Println(fmt.Sprintf("\n恢复会话失败: %s\n", msg.err))
		}
		return m.applyRestoredSession(msg)

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

func (m Model) handleSlashCommand(input string) (Model, tea.Cmd, bool) {
	if !strings.HasPrefix(input, "/") {
		return m, nil, false
	}

	switch {
	case input == "/exit":
		return m, tea.Quit, true

	case input == "/compact":
		next, cmd := m.startCompactRun()
		return next, cmd, true

	case input == "/continue":
		next, cmd := m.startContinueSession()
		return next, cmd, true

	case input == "/resume":
		next, cmd := m.startSessionList()
		return next, cmd, true

	case input == "/context":
		return m, tea.Println("\n" + m.contextInfo() + "\n"), true

	case strings.HasPrefix(input, "/context window"):
		value := strings.TrimSpace(strings.TrimPrefix(input, "/context window"))
		if value != "" {
			next, cmd := m.setContextWindow(value)
			return next, cmd, true
		}
		m.state = stateContextWindowInput
		m.textarea.Reset()
		m.textarea.Placeholder = "Context window tokens, e.g. 200000"
		m.textarea.Focus()
		return m, nil, true

	case strings.HasPrefix(input, "/plan"):
		target := strings.TrimSpace(strings.TrimPrefix(input, "/plan"))
		if target == "" {
			return m, tea.Println("\n请输入要规划的目标，例如 /plan 优化 grep 工具"), true
		}
		planPrompt := "Plan mode: inspect the codebase using read-only tools only, then produce an implementation plan. Do not modify files or run side-effect tools.\n\nGoal:\n" + target
		next, cmd := m.startAgentRun(planPrompt, agent.ModePlan, agent.RunOptions{Mode: agent.ModePlan})
		return next, cmd, true

	case input == "/do":
		if m.pendingPlan == nil || m.pendingPlan.Content == "" {
			return m, tea.Println("\n没有待执行计划，请先使用 /plan 生成计划。"), true
		}
		doPrompt := "Execute the pending plan below. Use tools as needed, report actual changes and verification results when complete.\n\n" + m.pendingPlan.Content
		m.pendingPlan.Consumed = true
		next, cmd := m.startAgentRun(doPrompt, agent.ModeExecute, agent.RunOptions{Mode: agent.ModeExecute})
		return next, cmd, true
	}

	return m, tea.Println(fmt.Sprintf("\n未知命令: %s", input)), true
}

func (m Model) startAgentRun(input string, mode agent.Mode, opts agent.RunOptions) (Model, tea.Cmd) {
	var cmds []tea.Cmd
	if m.journal == nil && m.sessionStore != nil {
		journal, err := m.sessionStore.Create()
		if err != nil {
			cmds = append(cmds, tea.Println(fmt.Sprintf("\nWarning: 创建会话存档失败: %s\n", err)))
		} else {
			m.journal = journal
			m.sessionID = journal.ID()
		}
	}
	userMessage := llm.Message{Role: "user", Content: input}
	m.conv.AddUser(input)
	m.turnMessages = []llm.Message{userMessage}
	if m.journal != nil {
		if err := m.journal.AppendMessage(userMessage); err != nil {
			cmds = append(cmds, tea.Println(fmt.Sprintf("\nWarning: 保存用户消息失败: %s\n", err)))
			m.disableJournal()
		}
	}

	memoryIndex := ""
	if m.noteStore != nil {
		loaded, err := m.noteStore.LoadIndexes()
		if err != nil {
			cmds = append(cmds, tea.Println(fmt.Sprintf("\nWarning: 读取长期记忆失败: %s\n", err)))
		} else {
			memoryIndex = loaded
		}
	}
	opts.PromptContext = prompt.SessionPromptContext{
		Instructions: m.instructions.Content,
		MemoryIndex:  memoryIndex,
		ResumeGap:    m.pendingResumeGap,
	}
	m.pendingResumeGap = ""
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelCurrent = cancel
	m.currentMode = mode
	m.pendingPerm = nil
	m.permissionFeedback = ""
	m.progressStatus = ""
	m.agentEvents = m.agent.Run(ctx, m.conv, opts)
	m.curReply.Reset()
	m.turnStart = time.Now()
	m.state = stateStreaming

	cmds = append(cmds, waitForAgentEvent(m.agentEvents), m.spinner.Tick)
	return m, tea.Batch(cmds...)
}

func (m Model) startCompactRun() (Model, tea.Cmd) {
	if m.agent == nil {
		m.err = fmt.Errorf("no provider selected")
		return m, tea.Quit
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelCurrent = cancel
	m.currentMode = agent.ModeExecute
	m.progressStatus = "正在压缩上下文"
	m.agentEvents = m.agent.Compact(ctx, m.conv, conversation.CompactModeManual)
	m.curReply.Reset()
	m.turnMessages = nil
	m.turnStart = time.Now()
	m.state = stateStreaming
	return m, tea.Batch(waitForAgentEvent(m.agentEvents), m.spinner.Tick)
}

func (m Model) applyContextWindowInput() (Model, tea.Cmd) {
	value := strings.TrimSpace(m.textarea.Value())
	m.textarea.Reset()
	m.textarea.Placeholder = mainInputPlaceholder
	m.textarea.Focus()
	return m.setContextWindow(value)
}

func (m Model) setContextWindow(value string) (Model, tea.Cmd) {
	limit, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || limit <= 0 {
		m.state = stateIdle
		return m, tea.Println("\n请输入正整数上下文窗口大小，例如 /context window 200000")
	}
	window, err := m.conv.SetContextWindow(context.Background(), limit)
	if err != nil {
		m.state = stateIdle
		return m, tea.Println(fmt.Sprintf("\n设置上下文窗口失败: %s", err.Error()))
	}
	state := m.conv.ContextState()
	m.contextWindow = window
	m.contextUsage = state.Usage
	m.contextStatus = "上下文窗口已更新"
	m.state = stateIdle
	return m, tea.Println(fmt.Sprintf("\n上下文窗口已设置为 %s", formatTokens(limit)))
}

func (m Model) cancelAgentRun() (Model, tea.Cmd) {
	if m.cancelCurrent != nil {
		m.cancelCurrent()
		m.progressStatus = "Cancelling"
	}
	return m, nil
}

func (m Model) answerPermission(choice permission.ConfirmationChoice) (Model, tea.Cmd) {
	if m.agent != nil && m.pendingPerm != nil {
		request := m.pendingPerm.Request
		m.agent.RespondPermission(permission.ConfirmationResponse{
			RequestID: request.ID,
			Choice:    choice,
		})
		m.permissionFeedback = permissionFeedback(choice, request)
	}
	m.pendingPerm = nil
	m.state = statePermissionConfirm
	return m, waitForAgentEvent(m.agentEvents)
}

func (m Model) cancelPermissionRun() (Model, tea.Cmd) {
	if m.agent != nil && m.pendingPerm != nil {
		m.agent.RespondPermission(permission.ConfirmationResponse{
			RequestID: m.pendingPerm.Request.ID,
			Choice:    permission.ChoiceDeny,
		})
	}
	m.pendingPerm = nil
	m.permissionFeedback = ""
	if m.cancelCurrent != nil {
		m.cancelCurrent()
		m.progressStatus = "Cancelling"
	}
	m.state = stateStreaming
	return m, waitForAgentEvent(m.agentEvents)
}

func (m *Model) movePermissionSelection(delta int) {
	options := permissionOptions()
	if len(options) == 0 {
		m.permSelectIndex = 0
		return
	}
	m.permSelectIndex = (m.permSelectIndex + delta + len(options)) % len(options)
}

func (m Model) confirmSelectedPermission() (Model, tea.Cmd) {
	options := permissionOptions()
	if len(options) == 0 {
		return m, nil
	}
	if m.permSelectIndex < 0 || m.permSelectIndex >= len(options) {
		m.permSelectIndex = 0
	}
	selected := options[m.permSelectIndex]
	if selected.cancel {
		return m.cancelPermissionRun()
	}
	return m.answerPermission(selected.choice)
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
	case statePermissionConfirm:
		return tea.NewView(m.viewPermissionConfirm())
	case stateContextWindowInput:
		return tea.NewView(m.viewContextWindowInput())
	case stateSessionLoading:
		return tea.NewView(m.viewSessionLoading())
	case stateSessionPicker:
		return tea.NewView(m.viewSessionPicker())
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
	s.WriteString(statusBar(m.provider, "Ready", m.contextDisplay(), m.width))
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
	if m.progressStatus != "" {
		status = fmt.Sprintf("%s %s %.1fs", m.spinner.View(), m.progressStatus, elapsed)
	}
	s.WriteString(statusBar(m.provider, status, m.contextDisplay(), m.width))
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

func (m Model) viewPermissionConfirm() string {
	var s strings.Builder

	elapsed := time.Since(m.turnStart).Seconds()
	statusText := "Permission"
	if m.pendingPerm != nil && m.pendingPerm.Request.BatchTotal > 1 {
		statusText = fmt.Sprintf("Permission %d/%d", m.pendingPerm.Request.BatchIndex, m.pendingPerm.Request.BatchTotal)
	}
	status := fmt.Sprintf("%s %s %.1fs", m.spinner.View(), statusText, elapsed)
	s.WriteString(statusBar(m.provider, status, m.contextDisplay(), m.width))
	s.WriteString("\n")

	if m.curReply.Len() > 0 {
		s.WriteString("\n")
		s.WriteString(m.curReply.String())
		s.WriteString("\n")
	}

	if m.pendingPerm != nil {
		req := m.pendingPerm.Request
		s.WriteString("\n")
		title := "Permission required"
		if req.BatchTotal > 1 {
			title = fmt.Sprintf("Permission required (tool call %d of %d)", req.BatchIndex, req.BatchTotal)
		}
		s.WriteString(permissionTitleStyle.Render(title))
		s.WriteString("\n")
		s.WriteString("-------------------\n")
		s.WriteString(fmt.Sprintf("Tool: %s\n", req.Tool))
		if req.Target != "" {
			s.WriteString("Target:\n")
			s.WriteString(fmt.Sprintf("  %s\n", req.Target))
		}
		if req.Risk != "" {
			s.WriteString(fmt.Sprintf("Risk: %s\n", req.Risk))
		}
		if req.Reason != "" {
			s.WriteString(fmt.Sprintf("Reason: %s\n", req.Reason))
		}
		s.WriteString("\n")
		s.WriteString("Choose an option:\n")
		for i, option := range permissionOptions() {
			s.WriteString(permissionOptionLine(option, i == m.permSelectIndex))
			s.WriteString("\n")
		}
		s.WriteString("\n")
		s.WriteString(permissionHintStyle.Render("Use ↑/↓ to select, Enter to confirm. Shortcuts: o once, s session, f forever, d deny, esc cancel"))
		s.WriteString("\n")
	} else if m.permissionFeedback != "" {
		s.WriteString("\n")
		s.WriteString(permissionTitleStyle.Render("Permission recorded"))
		s.WriteString("\n-------------------\n")
		s.WriteString(m.permissionFeedback)
		s.WriteString("\n")
	}

	return s.String()
}

func permissionFeedback(choice permission.ConfirmationChoice, request permission.ConfirmationRequest) string {
	position := ""
	if request.BatchTotal > 1 {
		position = fmt.Sprintf("Tool call %d of %d: ", request.BatchIndex, request.BatchTotal)
	}
	switch choice {
	case permission.ChoiceAllowOnce:
		return fmt.Sprintf("%sAllowed once. Running %s...", position, request.Tool)
	case permission.ChoiceAllowSession:
		return fmt.Sprintf("%sAllowed for this session. Running %s...", position, request.Tool)
	case permission.ChoiceAllowForever:
		return fmt.Sprintf("%sAllowed permanently. Running %s...", position, request.Tool)
	default:
		return fmt.Sprintf("%sDenied. Checking remaining tool calls...", position)
	}
}

func (m Model) viewContextWindowInput() string {
	var s strings.Builder
	s.WriteString(statusBar(m.provider, "Set context window", m.contextDisplay(), m.width))
	s.WriteString("\n")
	s.WriteString("❯ ")
	s.WriteString(m.textarea.View())
	s.WriteString("\n")
	return s.String()
}

func isActiveRunState(state sessionState) bool {
	return state == stateStreaming || state == statePermissionConfirm || state == stateSessionLoading
}

func (m *Model) persistConversationEvent(change *agent.ConversationEvent) error {
	if change == nil {
		return nil
	}
	switch change.Kind {
	case agent.ConversationAppend:
		if change.Message == nil {
			return fmt.Errorf("Conversation append 事件缺少消息")
		}
		m.turnMessages = append(m.turnMessages, *change.Message)
		if m.journal != nil {
			if err := m.journal.AppendMessage(*change.Message); err != nil {
				m.disableJournal()
				return fmt.Errorf("保存会话消息失败: %w", err)
			}
		}
	case agent.ConversationSnapshot:
		if m.journal != nil {
			if err := m.journal.AppendSnapshot(change.Messages); err != nil {
				m.disableJournal()
				return fmt.Errorf("保存会话快照失败: %w", err)
			}
		}
	default:
		return fmt.Errorf("未知 Conversation 事件: %d", change.Kind)
	}
	return nil
}

func (m *Model) disableJournal() {
	if m.journal != nil {
		_ = m.journal.Close()
	}
	m.journal = nil
}

func (m Model) startContinueSession() (Model, tea.Cmd) {
	if m.sessionStore == nil {
		return m, tea.Println("\n会话存档未配置。")
	}
	m.state = stateSessionLoading
	m.progressStatus = "正在恢复最近会话"
	m.turnStart = m.clock()
	return m, tea.Batch(loadLatestSession(m.sessionStore), m.spinner.Tick)
}

func (m Model) startSessionList() (Model, tea.Cmd) {
	if m.sessionStore == nil {
		return m, tea.Println("\n会话存档未配置。")
	}
	m.state = stateSessionLoading
	m.progressStatus = "正在扫描历史会话"
	m.turnStart = m.clock()
	return m, tea.Batch(listSessions(m.sessionStore), m.spinner.Tick)
}

func listSessions(store *memory.SessionStore) tea.Cmd {
	return func() tea.Msg {
		sessions, err := store.List()
		return sessionListMsg{sessions: sessions, err: err}
	}
}

func loadLatestSession(store *memory.SessionStore) tea.Cmd {
	return func() tea.Msg {
		result, err := store.Latest()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				err = fmt.Errorf("没有可继续的最近会话")
			}
			return sessionRestoreMsg{err: err}
		}
		journal, err := store.Open(result.Info.ID)
		return sessionRestoreMsg{result: result, journal: journal, err: err}
	}
}

func loadSession(store *memory.SessionStore, id string) tea.Cmd {
	return func() tea.Msg {
		result, err := store.Load(id)
		if err != nil {
			return sessionRestoreMsg{err: err}
		}
		journal, err := store.Open(id)
		return sessionRestoreMsg{result: result, journal: journal, err: err}
	}
}

func (m Model) applyRestoredSession(msg sessionRestoreMsg) (tea.Model, tea.Cmd) {
	if msg.journal == nil || len(msg.result.Messages) == 0 {
		if msg.journal != nil {
			_ = msg.journal.Close()
		}
		m.state = stateIdle
		m.textarea.Focus()
		return m, tea.Println("\n恢复会话失败: 会话没有有效消息。")
	}
	if m.journal != nil {
		_ = m.journal.Close()
	}
	m.conv.Restore(msg.result.Messages)
	m.journal = msg.journal
	m.sessionID = msg.result.Info.ID
	m.pendingPlan = nil
	m.turnMessages = nil
	m.resumeSessions = nil
	m.resumeSelectIndex = 0
	if m.instructionLoader != nil {
		m.instructions = m.instructionLoader.Load()
	}
	state := m.conv.ContextState()
	m.contextUsage = state.Usage
	m.contextWindow = state.Window
	m.contextStatus = "历史会话已恢复"
	gap := m.clock().Sub(msg.result.LastActiveAt)
	if gap > 24*time.Hour {
		m.pendingResumeGap = fmt.Sprintf(
			"This session was last active %s ago. Re-check repository state, git status, dependencies, and external facts before relying on old assumptions.",
			formatDuration(gap),
		)
	} else {
		m.pendingResumeGap = ""
	}
	m.state = stateIdle
	m.progressStatus = ""
	m.textarea.Focus()

	var output strings.Builder
	output.WriteString("\n已恢复会话 ")
	output.WriteString(msg.result.Info.ID)
	output.WriteString("\n")
	for _, warning := range msg.result.Warnings {
		output.WriteString("Warning: ")
		if warning.Line > 0 {
			output.WriteString(fmt.Sprintf("line %d: ", warning.Line))
		}
		output.WriteString(warning.Message)
		output.WriteString("\n")
	}
	for _, warning := range m.instructions.Warnings {
		output.WriteString("Warning: ")
		output.WriteString(formatInstructionWarning(warning))
		output.WriteString("\n")
	}
	output.WriteString(formatRecoveredTranscript(msg.result.Messages))
	return m, tea.Println(output.String())
}

// Close releases the final session journal after Bubble Tea exits.
func (m Model) Close() error {
	if m.journal == nil {
		return nil
	}
	return m.journal.Close()
}

func (m Model) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func formatInstructionWarning(warning memory.LoadWarning) string {
	location := warning.Path
	if warning.Line > 0 {
		location = fmt.Sprintf("%s:%d", location, warning.Line)
	}
	return fmt.Sprintf("%s: %s", location, warning.Message)
}

func formatRecoveredTranscript(messages []llm.Message) string {
	var out strings.Builder
	for _, message := range messages {
		switch message.Role {
		case "user":
			out.WriteString("\n❯ ")
			out.WriteString(truncateRunes(message.Content, 2000))
			out.WriteString("\n")
		case "assistant":
			if strings.TrimSpace(message.Content) != "" {
				out.WriteString("\n")
				out.WriteString(truncateRunes(message.Content, 4000))
				out.WriteString("\n")
			}
			for _, call := range message.ToolCalls {
				out.WriteString(fmt.Sprintf("\n● %s(...)\n", call.Name))
			}
		case "tool":
			if message.ToolResult != nil {
				out.WriteString("  └─ ")
				out.WriteString(truncateRunes(strings.ReplaceAll(message.ToolResult.Content, "\n", " "), 300))
				out.WriteString("\n")
			}
		}
	}
	return out.String()
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func formatDuration(value time.Duration) string {
	hours := int(value.Hours())
	if hours < 48 {
		return fmt.Sprintf("%d hours", hours)
	}
	return fmt.Sprintf("%d days", hours/24)
}

type permissionOption struct {
	number string
	label  string
	choice permission.ConfirmationChoice
	cancel bool
}

func permissionOptions() []permissionOption {
	return []permissionOption{
		{number: "1", label: "Allow once", choice: permission.ChoiceAllowOnce},
		{number: "2", label: "Allow for this session", choice: permission.ChoiceAllowSession},
		{number: "3", label: "Allow forever for this exact request", choice: permission.ChoiceAllowForever},
		{number: "4", label: "Deny", choice: permission.ChoiceDeny},
		{number: "5", label: "Cancel this run", cancel: true},
	}
}

func permissionOptionLine(option permissionOption, selected bool) string {
	line := fmt.Sprintf("%s. %s", option.number, option.label)
	if selected {
		return permissionSelectedStyle.Render("> " + line)
	}
	return permissionOptionStyle.Render("  " + line)
}

func (m Model) contextDisplay() string {
	state := m.conv.ContextState()
	window := m.contextWindow
	if window.Limit <= 0 {
		window = state.Window
	}
	usage := m.contextUsage
	if usage.Limit <= 0 {
		usage = state.Usage
	}
	limit := usage.Limit
	if limit <= 0 {
		limit = window.Limit
	}
	used := usage.Used
	percent := usage.Percent
	if percent == 0 && limit > 0 && used > 0 {
		percent = used * 100 / limit
	}
	approx := usage.Estimated || window.Source == conversation.WindowSourceInferred || window.Source == conversation.WindowSourceDefault
	prefix := ""
	if approx {
		prefix = "~"
	}
	return fmt.Sprintf("Context %s%s / %s · %d%%", prefix, formatTokens(used), formatTokens(limit), percent)
}

func (m Model) contextInfo() string {
	state := m.conv.ContextState()
	window := m.contextWindow
	if window.Limit <= 0 {
		window = state.Window
	}
	return fmt.Sprintf(
		"Context: %s\nWindow: %s (%s)\nStatus: %s\nUse /context window 200000 to set a project-local window.",
		m.contextDisplay(),
		formatTokens(window.Limit),
		windowSourceText(window.Source),
		emptyStatus(m.contextStatus),
	)
}

func formatTokens(value int) string {
	if value >= 1000 {
		return fmt.Sprintf("%dk", value/1000)
	}
	return fmt.Sprintf("%d", value)
}

func windowSourceText(source conversation.WindowSource) string {
	switch source {
	case conversation.WindowSourceLocal:
		return "local"
	case conversation.WindowSourceProvider:
		return "provider"
	case conversation.WindowSourceInferred:
		return "estimated"
	default:
		return "default"
	}
}

func emptyStatus(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Ready"
	}
	return value
}

func shouldPrintContextEvent(kind agent.ContextEventKind) bool {
	return kind != agent.ContextUsageUpdated
}

// statusBar 渲染状态栏
func statusBar(provider llm.Provider, status string, contextText string, width int) string {
	if provider == nil {
		return ""
	}
	left := fmt.Sprintf(" %s ", provider.Name())
	right := fmt.Sprintf(" %s ", provider.Model())
	middle := fmt.Sprintf(" %s ", status)
	contextPart := ""
	if strings.TrimSpace(contextText) != "" {
		contextPart = fmt.Sprintf(" %s ", contextText)
	}

	// ANSI 转义、中文和 ASCII 的字节数不同，必须按终端显示宽度计算。
	padding := width - lipgloss.Width(left) - lipgloss.Width(right) - lipgloss.Width(middle) - lipgloss.Width(contextPart)
	if padding < 0 {
		padding = 0
	}

	return fmt.Sprintf("%s%s%s%s%s",
		statusBarLeftStyle.Render(left),
		statusBarMiddleStyle.Render(middle),
		statusBarRightStyle.Render(strings.Repeat(" ", padding)),
		statusBarRightStyle.Render(contextPart),
		statusBarRightStyle.Render(right),
	)
}

package agent

import (
	"onecode/internal/conversation"
	"onecode/internal/permission"
)

// Mode 表示 Agent 本次运行处于哪种工具开放策略。
type Mode int

const (
	ModeExecute Mode = iota
	ModePlan
)

// RunOptions 控制一次 Agent loop 的运行模式和停止上限。
type RunOptions struct {
	Mode                   Mode
	MaxIterations          int
	MaxConsecutiveBadTools int
	ReminderInterval       int
}

// String returns the prompt-runtime mode name.
func (m Mode) String() string {
	if m == ModePlan {
		return "plan"
	}
	return "execute"
}

// EventType 标识 Agent 对外事件类型。
type EventType int

const (
	EventText EventType = iota
	EventToolStart
	EventToolResult
	EventUsage
	EventProgress
	EventDone
	EventError
	EventCancelled
	EventPermissionRequest
	EventContext
)

// Event 是 Agent 和 TUI 之间唯一的异步通信载体。
type Event struct {
	Type       EventType
	Text       string
	Tool       *ToolEvent
	Usage      *UsageEvent
	Progress   *ProgressEvent
	Done       *DoneEvent
	Permission *PermissionEvent
	Context    *ContextEvent
	Err        error
}

// ToolEvent 描述一次工具调用开始或结束时的 UI 摘要。
type ToolEvent struct {
	ID      string
	Name    string
	Args    string
	Result  string
	IsError bool
}

// PermissionEvent asks the UI to resolve one pending tool permission request.
type PermissionEvent struct {
	Request permission.ConfirmationRequest
}

// UsageEvent 表示 token 用量更新。
type UsageEvent struct {
	InputTokens              int
	OutputTokens             int
	TotalTokens              int
	Available                bool
	CacheAvailable           bool
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

// ContextEvent 表示上下文管理状态变化。
type ContextEvent struct {
	Kind    ContextEventKind
	Message string
	Usage   conversation.UsageEstimate
}

// ContextEventKind 标识上下文管理事件类型。
type ContextEventKind int

const (
	ContextUsageUpdated ContextEventKind = iota
	ContextToolResultStored
	ContextCompactStarted
	ContextCompactCompleted
	ContextCompactFailed
	ContextCompactFuseTripped
	ContextEmergencyRetry
)

// ProgressStatus 表示 Agent loop 当前阶段。
type ProgressStatus int

const (
	ProgressRequestingModel ProgressStatus = iota
	ProgressCollectingStream
	ProgressExecutingTools
	ProgressContinuing
	ProgressCompleted
	ProgressCancelling
)

// ProgressEvent 表示一次 loop 的阶段变化。
type ProgressEvent struct {
	Iteration int
	Status    ProgressStatus
	Message   string
}

// StopReason 表示 Agent loop 为什么停止。
type StopReason int

const (
	StopModelDone StopReason = iota
	StopMaxIterations
	StopCancelled
	StopBadToolLimit
	StopStreamError
	StopToolError
)

// DoneEvent 表示本次 Agent loop 已经结束。
type DoneEvent struct {
	Reason     StopReason
	Iterations int
}

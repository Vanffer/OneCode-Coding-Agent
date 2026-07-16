package agent

import (
	"context"

	"onecode/internal/conversation"
	"onecode/internal/llm"
	"onecode/internal/permission"
	"onecode/internal/prompt"
	"onecode/internal/tools"
)

const (
	defaultMaxIterations          = 20
	defaultMaxConsecutiveBadTools = 3
	defaultReminderInterval       = 5
)

// Agent 持有 provider 与工具注册中心，执行 ReAct agent loop。
type Agent struct {
	provider            llm.Provider
	registry            *tools.Registry
	promptRuntime       *prompt.Runtime
	permissionManager   *permission.Manager
	permissionResponses chan permission.ConfirmationResponse
	contextOptions      conversation.ContextOptions
}

// Option customizes an Agent.
type Option func(*Agent)

func WithPromptRuntime(runtime *prompt.Runtime) Option {
	return func(a *Agent) {
		if runtime != nil {
			a.promptRuntime = runtime
		}
	}
}

func WithPermissionManager(manager *permission.Manager) Option {
	return func(a *Agent) {
		a.permissionManager = manager
	}
}

func WithContextOptions(opts conversation.ContextOptions) Option {
	return func(a *Agent) {
		a.contextOptions = opts
	}
}

// New 创建 Agent。
func New(p llm.Provider, r *tools.Registry, opts ...Option) *Agent {
	a := &Agent{
		provider:            p,
		registry:            r,
		promptRuntime:       defaultPromptRuntime(),
		permissionResponses: make(chan permission.ConfirmationResponse, 16),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return a
}

func (a *Agent) conversationContextOptions() conversation.ContextOptions {
	opts := a.contextOptions
	if a.provider != nil {
		if opts.ProviderName == "" {
			opts.ProviderName = a.provider.Name()
		}
		if opts.ModelName == "" {
			opts.ModelName = a.provider.Model()
		}
	}
	return opts
}

func (a *Agent) RespondPermission(response permission.ConfirmationResponse) {
	select {
	case a.permissionResponses <- response:
	default:
	}
}

// Run 执行一次 Agent loop，返回事件 channel。
// conv 在 Run 内被修改（追加 assistant/tool 消息），调用方无需额外写回。
func (a *Agent) Run(ctx context.Context, conv *conversation.Conversation, opts RunOptions) <-chan Event {
	events := make(chan Event, 16)
	opts = normalizeRunOptions(opts)

	go func() {
		defer close(events)
		if a.permissionManager != nil {
			a.permissionManager.SetConfirmer(&eventConfirmer{
				events:    events,
				responses: a.permissionResponses,
			})
		}
		a.runLoop(ctx, conv, opts, events)
	}()

	return events
}

func (a *Agent) Compact(ctx context.Context, conv *conversation.Conversation, mode conversation.CompactMode) <-chan Event {
	events := make(chan Event, 16)
	go func() {
		defer close(events)
		result, err := conv.Compact(ctx, providerCompressor{
			provider: a.provider,
		}, mode, a.conversationContextOptions())
		sendContextStatuses(ctx, events, result.Statuses)
		if err != nil {
			sendEvent(ctx, events, Event{Type: EventError, Err: err})
			sendEvent(ctx, events, Event{Type: EventDone, Done: &DoneEvent{Reason: StopStreamError}})
			return
		}
		sendEvent(ctx, events, Event{Type: EventDone, Done: &DoneEvent{Reason: StopModelDone}})
	}()
	return events
}

func normalizeRunOptions(opts RunOptions) RunOptions {
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = defaultMaxIterations
	}
	if opts.MaxConsecutiveBadTools <= 0 {
		opts.MaxConsecutiveBadTools = defaultMaxConsecutiveBadTools
	}
	if opts.ReminderInterval <= 0 {
		opts.ReminderInterval = defaultReminderInterval
	}
	return opts
}

func defaultPromptRuntime() *prompt.Runtime {
	runtime, err := prompt.NewRuntime(prompt.BuildOptions{})
	if err != nil {
		panic(err)
	}
	return runtime
}

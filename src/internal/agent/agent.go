package agent

import (
	"context"

	"onecode/internal/conversation"
	"onecode/internal/llm"
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
	provider      llm.Provider
	registry      *tools.Registry
	promptRuntime *prompt.Runtime
}

// New 创建 Agent。
func New(p llm.Provider, r *tools.Registry, runtimes ...*prompt.Runtime) *Agent {
	pr := defaultPromptRuntime()
	if len(runtimes) > 0 && runtimes[0] != nil {
		pr = runtimes[0]
	}
	return &Agent{
		provider:      p,
		registry:      r,
		promptRuntime: pr,
	}
}

// Run 执行一次 Agent loop，返回事件 channel。
// conv 在 Run 内被修改（追加 assistant/tool 消息），调用方无需额外写回。
func (a *Agent) Run(ctx context.Context, conv *conversation.Conversation, opts RunOptions) <-chan Event {
	events := make(chan Event, 16)
	opts = normalizeRunOptions(opts)

	go func() {
		defer close(events)
		a.runLoop(ctx, conv, opts, events)
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

package prompt

import "time"

const defaultReminderInterval = 5

// BuildOptions controls stable prompt assembly.
type BuildOptions struct {
	OptionalModules []Module
}

// Runtime owns the stable system prompt and creates per-request prompt payloads.
type Runtime struct {
	stable string
}

// RequestContext contains dynamic data for one model request.
type RequestContext struct {
	Mode             string
	Iteration        int
	CWD              string
	OS               string
	Arch             string
	Shell            string
	GitStatus        string
	Now              time.Time
	ReminderInterval int
}

// Payload is the provider-facing prompt data for one model request.
type Payload struct {
	StableSystem string
	Reminders    []Reminder
}

// NewRuntime creates a prompt runtime with a cache-friendly stable system body.
func NewRuntime(opts BuildOptions) (*Runtime, error) {
	stable, err := BuildStableSystem(opts)
	if err != nil {
		return nil, err
	}
	return &Runtime{stable: stable}, nil
}

// StableSystem returns the stable system prompt body.
func (r *Runtime) StableSystem() string {
	if r == nil {
		return ""
	}
	return r.stable
}

// BuildPayload creates stable and dynamic prompt content for one model request.
func (r *Runtime) BuildPayload(ctx RequestContext) Payload {
	payload := Payload{StableSystem: r.StableSystem()}
	payload.Reminders = append(payload.Reminders, buildEnvironmentReminder(ctx))
	if ctx.Mode == "plan" {
		payload.Reminders = append(payload.Reminders, buildPlanModeReminder(ctx))
	}
	return payload
}

func normalizeReminderInterval(interval int) int {
	if interval <= 0 {
		return defaultReminderInterval
	}
	return interval
}

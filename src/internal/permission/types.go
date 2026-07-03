package permission

import "onecode/internal/tools"

// Mode is the global permission fallback level.
type Mode string

const (
	ModeStrict      Mode = "strict"
	ModeDefault     Mode = "default"
	ModeAcceptEdits Mode = "acceptEdits"
	ModePlan        Mode = "plan"
	ModeBypass      Mode = "bypassPermissions"
)

// Action is the result of a permission decision.
type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
	ActionAsk   Action = "ask"
)

// Scope identifies the layer that produced a permission decision.
type Scope string

const (
	ScopeSession Scope = "session"
	ScopeLocal   Scope = "local"
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
	ScopeMode    Scope = "mode"
	ScopeBuiltin Scope = "builtin"
)

// Config is the permissions YAML shape.
type Config struct {
	Mode  Mode     `yaml:"mode"`
	Rules []string `yaml:"rules"`
}

// Rule is a parsed permission rule.
type Rule struct {
	Tool    string
	Pattern string
	Action  Action
	Scope   Scope
}

// RuleSet is one layer of permission rules.
type RuleSet struct {
	Scope Scope
	Rules []Rule
}

// Request is the authorization input for one tool call.
type Request struct {
	ID       string
	Tool     string
	Args     map[string]interface{}
	Safety   tools.Safety
	Category tools.ToolCategory
}

// TargetKind describes what kind of subject a tool call targets.
type TargetKind string

const (
	TargetCommand TargetKind = "command"
	TargetPath    TargetKind = "path"
	TargetSearch  TargetKind = "search"
	TargetUnknown TargetKind = "unknown"
)

// Target is the normalized match subject extracted from a tool call.
type Target struct {
	Kind       TargetKind
	Value      string
	Path       string
	Command    string
	Glob       string
	SearchRoot string
}

// Decision is the output of permission evaluation.
type Decision struct {
	Action  Action
	Reason  string
	Scope   Scope
	Rule    *Rule
	Confirm *ConfirmationRequest
}

// ConfirmationRequest is shown to a user when a decision needs human input.
type ConfirmationRequest struct {
	ID          string
	Tool        string
	ArgsPreview string
	Target      string
	Risk        string
	Reason      string
}

// ConfirmationChoice is a user's response to a permission request.
type ConfirmationChoice string

const (
	ChoiceDeny         ConfirmationChoice = "deny"
	ChoiceAllowOnce    ConfirmationChoice = "allow_once"
	ChoiceAllowSession ConfirmationChoice = "allow_session"
	ChoiceAllowForever ConfirmationChoice = "allow_forever"
)

// ConfirmationResponse carries the user's choice back to the permission manager.
type ConfirmationResponse struct {
	RequestID string
	Choice    ConfirmationChoice
}

package permission

import (
	"context"
	"path/filepath"
	"testing"

	"onecode/internal/tools"
)

type memoryStore struct {
	sets     []RuleSet
	mode     Mode
	appended []Rule
}

func (s *memoryStore) Load(context.Context) ([]RuleSet, Mode, error) {
	return s.sets, s.mode, nil
}

func (s *memoryStore) AppendLocalRule(_ context.Context, rule Rule) error {
	s.appended = append(s.appended, rule)
	return nil
}

func TestManagerBlacklistCannotBeBypassed(t *testing.T) {
	root := testProjectRoot(t)
	store := &memoryStore{sets: []RuleSet{{
		Scope: ScopeLocal,
		Rules: []Rule{{Tool: "bash", Pattern: "rm -rf /", Action: ActionAllow, Scope: ScopeLocal}},
	}}}
	manager := newTestManager(t, root, ManagerOptions{Mode: ModeBypass, Store: store})

	decision := manager.Authorize(context.Background(), Request{
		Tool: "bash",
		Args: map[string]interface{}{"command": "rm -rf /"},
	})
	if decision.Action != ActionDeny || decision.Scope != ScopeBuiltin {
		t.Fatalf("expected builtin deny, got %+v", decision)
	}
}

func TestManagerRulePrecedence(t *testing.T) {
	root := testProjectRoot(t)
	store := &memoryStore{sets: []RuleSet{
		{Scope: ScopeUser, Rules: []Rule{{Tool: "bash", Pattern: "git *", Action: ActionAllow, Scope: ScopeUser}}},
		{Scope: ScopeProject, Rules: []Rule{{Tool: "bash", Pattern: "git push *", Action: ActionDeny, Scope: ScopeProject}}},
		{Scope: ScopeLocal, Rules: []Rule{{Tool: "bash", Pattern: "git push origin dev", Action: ActionAllow, Scope: ScopeLocal}}},
	}}
	manager := newTestManager(t, root, ManagerOptions{Store: store})

	decision := manager.Authorize(context.Background(), Request{
		Tool: "bash",
		Args: map[string]interface{}{"command": "git push origin dev"},
	})
	if decision.Action != ActionAllow || decision.Scope != ScopeLocal {
		t.Fatalf("expected local allow, got %+v", decision)
	}
}

func TestManagerModePolicy(t *testing.T) {
	root := testProjectRoot(t)
	tests := []struct {
		name   string
		mode   Mode
		req    Request
		action Action
	}{
		{"strict read asks", ModeStrict, Request{Tool: "read_file", Safety: tools.SafetyReadOnly}, ActionAsk},
		{"default read allows", ModeDefault, Request{Tool: "read_file", Safety: tools.SafetyReadOnly}, ActionAllow},
		{"default write asks", ModeDefault, Request{Tool: "edit_file", Safety: tools.SafetySideEffect}, ActionAsk},
		{"accept edits allows write", ModeAcceptEdits, Request{Tool: "edit_file", Safety: tools.SafetySideEffect}, ActionAllow},
		{"accept edits asks command", ModeAcceptEdits, Request{Tool: "bash", Safety: tools.SafetySideEffect}, ActionAsk},
		{"plan allows read", ModePlan, Request{Tool: "read_file", Safety: tools.SafetyReadOnly}, ActionAllow},
		{"plan denies write", ModePlan, Request{Tool: "edit_file", Safety: tools.SafetySideEffect}, ActionDeny},
		{"default asks mcp", ModeDefault, Request{Tool: "mcp__demo__mutate", Safety: tools.SafetySideEffect, Category: tools.CategoryMCP}, ActionAsk},
		{"accept edits asks mcp", ModeAcceptEdits, Request{Tool: "mcp__demo__mutate", Safety: tools.SafetySideEffect, Category: tools.CategoryMCP}, ActionAsk},
		{"bypass permissions allows bash", ModeBypass, Request{Tool: "bash", Safety: tools.SafetySideEffect}, ActionAllow},
		{"bypass permissions allows mcp", ModeBypass, Request{Tool: "mcp__demo__mutate", Safety: tools.SafetySideEffect, Category: tools.CategoryMCP}, ActionAllow},
		{"bypass permissions asks unknown", ModeBypass, Request{Tool: "custom_tool", Safety: tools.SafetySideEffect, Category: tools.CategoryUnknown}, ActionAsk},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newTestManager(t, root, ManagerOptions{Mode: tt.mode})
			tt.req.Args = map[string]interface{}{"path": ".", "command": "git status"}
			decision := manager.Authorize(context.Background(), tt.req)
			if decision.Action != tt.action {
				t.Fatalf("expected %s, got %+v", tt.action, decision)
			}
		})
	}
}

func TestResolveConfirmation(t *testing.T) {
	root := testProjectRoot(t)
	confirmer := &StaticConfirmer{Choice: ChoiceAllowOnce}
	manager := newTestManager(t, root, ManagerOptions{Mode: ModeDefault, Confirmer: confirmer})
	decision := manager.Resolve(context.Background(), Request{
		ID:     "call_1",
		Tool:   "bash",
		Args:   map[string]interface{}{"command": "git status"},
		Safety: tools.SafetySideEffect,
	})
	if decision.Action != ActionAllow || confirmer.Count != 1 {
		t.Fatalf("expected confirmed allow, got decision=%+v count=%d", decision, confirmer.Count)
	}
}

func TestConfirmationPreservesToolBatchPosition(t *testing.T) {
	manager := newTestManager(t, testProjectRoot(t), ManagerOptions{Mode: ModeDefault})
	decision := manager.Authorize(context.Background(), Request{
		ID:         "call_2",
		Tool:       "bash",
		Args:       map[string]interface{}{"command": "git status"},
		Safety:     tools.SafetySideEffect,
		BatchIndex: 2,
		BatchTotal: 3,
	})
	if decision.Action != ActionAsk || decision.Confirm == nil {
		t.Fatalf("expected confirmation request, got %+v", decision)
	}
	if decision.Confirm.BatchIndex != 2 || decision.Confirm.BatchTotal != 3 {
		t.Fatalf("unexpected batch position: %+v", decision.Confirm)
	}
}

func TestConfirmationRules(t *testing.T) {
	root := testProjectRoot(t)
	store := &memoryStore{}
	confirmer := &StaticConfirmer{Choice: ChoiceAllowSession}
	manager := newTestManager(t, root, ManagerOptions{Mode: ModeDefault, Store: store, Confirmer: confirmer})
	req := Request{Tool: "bash", Args: map[string]interface{}{"command": "git status"}, Safety: tools.SafetySideEffect}

	first := manager.Resolve(context.Background(), req)
	if first.Action != ActionAllow || len(manager.session.Rules) != 1 {
		t.Fatalf("expected session allow rule, got decision=%+v rules=%+v", first, manager.session.Rules)
	}
	second := manager.Resolve(context.Background(), req)
	if second.Scope != ScopeSession || confirmer.Count != 1 {
		t.Fatalf("expected second call to use session rule, got decision=%+v count=%d", second, confirmer.Count)
	}

	store = &memoryStore{}
	confirmer = &StaticConfirmer{Choice: ChoiceAllowForever}
	manager = newTestManager(t, root, ManagerOptions{Mode: ModeDefault, Store: store, Confirmer: confirmer})
	forever := manager.Resolve(context.Background(), req)
	if forever.Action != ActionAllow || len(store.appended) != 1 {
		t.Fatalf("expected local appended rule, got decision=%+v appended=%+v", forever, store.appended)
	}
	if store.appended[0].Pattern != "git status" {
		t.Fatalf("expected exact rule, got %+v", store.appended[0])
	}
}

func TestManagerSandboxDeniesBeforeRules(t *testing.T) {
	root := testProjectRoot(t)
	store := &memoryStore{sets: []RuleSet{{
		Scope: ScopeLocal,
		Rules: []Rule{{Tool: "read_file", Pattern: "../secret.txt", Action: ActionAllow, Scope: ScopeLocal}},
	}}}
	manager := newTestManager(t, root, ManagerOptions{Mode: ModeBypass, Store: store})
	decision := manager.Authorize(context.Background(), Request{
		Tool:   "read_file",
		Args:   map[string]interface{}{"path": "../secret.txt"},
		Safety: tools.SafetyReadOnly,
	})
	if decision.Action != ActionDeny || decision.Scope != ScopeBuiltin {
		t.Fatalf("expected sandbox deny before rule, got %+v", decision)
	}
}

func TestManagerAllowsMissingLeafInsideRoot(t *testing.T) {
	root := testProjectRoot(t)
	manager := newTestManager(t, root, ManagerOptions{Mode: ModeBypass})
	decision := manager.Authorize(context.Background(), Request{
		Tool:   "write_file",
		Args:   map[string]interface{}{"path": filepath.Join("new", "file.go")},
		Safety: tools.SafetySideEffect,
	})
	if decision.Action != ActionAllow {
		t.Fatalf("expected missing leaf inside root to continue, got %+v", decision)
	}
}

func newTestManager(t *testing.T, root string, opts ManagerOptions) *Manager {
	t.Helper()
	opts.ProjectRoot = root
	manager, err := NewManager(opts)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	return manager
}

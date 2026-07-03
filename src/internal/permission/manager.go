package permission

import (
	"context"
	"fmt"
	"sync"
)

// ManagerOptions configures a permission manager.
type ManagerOptions struct {
	Mode        Mode
	ProjectRoot string
	Store       Store
	Confirmer   Confirmer
}

// Manager evaluates layered permissions for tool calls.
type Manager struct {
	mu        sync.Mutex
	mode      Mode
	rules     []RuleSet
	session   RuleSet
	blacklist Blacklist
	sandbox   Sandbox
	store     Store
	confirmer Confirmer
}

func NewManager(opts ManagerOptions) (*Manager, error) {
	mode := opts.Mode
	if mode == "" {
		mode = ModeDefault
	}
	if !validMode(mode) {
		return nil, fmt.Errorf("权限模式无效: %s", mode)
	}
	sandbox, err := NewSandbox(opts.ProjectRoot)
	if err != nil {
		return nil, err
	}
	var sets []RuleSet
	if opts.Store != nil {
		loaded, storeMode, err := opts.Store.Load(context.Background())
		if err != nil {
			return nil, err
		}
		sets = loaded
		if opts.Mode == "" && storeMode != "" {
			mode = storeMode
		}
	}
	confirmer := opts.Confirmer
	if confirmer == nil {
		confirmer = &StaticConfirmer{Choice: ChoiceDeny}
	}
	return &Manager{
		mode:      mode,
		rules:     sets,
		session:   RuleSet{Scope: ScopeSession},
		blacklist: NewBlacklist(),
		sandbox:   sandbox,
		store:     opts.Store,
		confirmer: confirmer,
	}, nil
}

func (m *Manager) SetConfirmer(confirmer Confirmer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if confirmer == nil {
		confirmer = &StaticConfirmer{Choice: ChoiceDeny}
	}
	m.confirmer = confirmer
}

func (m *Manager) Authorize(ctx context.Context, req Request) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.authorize(ctx, req)
}

func (m *Manager) authorize(ctx context.Context, req Request) Decision {
	if err := ctx.Err(); err != nil {
		return denyDecision("权限判断已取消", ScopeBuiltin)
	}
	target := ExtractTarget(req)
	if decision, ok := m.blacklist.Check(target); ok {
		return decision
	}
	if decision, ok := m.checkSandbox(target, req); ok {
		return decision
	}
	for _, set := range m.orderedRuleSets() {
		for _, rule := range set.Rules {
			if MatchRule(rule, req, target) {
				decision := Decision{
					Action: rule.Action,
					Reason: fmt.Sprintf("权限规则 %s 命中: %s", rule.Scope, FormatRule(rule)),
					Scope:  rule.Scope,
					Rule:   &rule,
				}
				return decision
			}
		}
	}
	return m.defaultByMode(req, target)
}

func (m *Manager) Resolve(ctx context.Context, req Request) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()

	decision := m.authorize(ctx, req)
	if decision.Action != ActionAsk {
		return decision
	}
	if decision.Confirm == nil {
		return denyDecision("权限确认请求缺失", ScopeMode)
	}
	response, err := m.confirmer.Confirm(ctx, *decision.Confirm)
	if err != nil {
		return denyDecision("权限确认已取消或失败: "+err.Error(), ScopeMode)
	}
	target := ExtractTarget(req)
	switch response.Choice {
	case ChoiceAllowOnce:
		return Decision{Action: ActionAllow, Reason: "用户仅本次允许", Scope: ScopeMode}
	case ChoiceAllowSession:
		rule := GenerateExactAllowRule(req, target, ScopeSession)
		m.session.Rules = append(m.session.Rules, rule)
		return Decision{Action: ActionAllow, Reason: "用户本会话允许", Scope: ScopeSession, Rule: &rule}
	case ChoiceAllowForever:
		rule := GenerateExactAllowRule(req, target, ScopeLocal)
		if m.store == nil {
			return denyDecision("无法永久允许: 未配置权限存储", ScopeLocal)
		}
		if err := m.store.AppendLocalRule(ctx, rule); err != nil {
			return denyDecision("写入永久权限规则失败: "+err.Error(), ScopeLocal)
		}
		return Decision{Action: ActionAllow, Reason: "用户永久允许", Scope: ScopeLocal, Rule: &rule}
	case ChoiceDeny:
		fallthrough
	default:
		return denyDecision("用户拒绝执行工具", ScopeMode)
	}
}

func (m *Manager) checkSandbox(target Target, req Request) (Decision, bool) {
	var path string
	allowMissing := false
	switch NormalizeToolName(req.Tool) {
	case "read_file", "edit_file":
		path = target.Path
	case "write_file":
		path = target.Path
		allowMissing = true
	case "glob", "grep":
		path = target.SearchRoot
	default:
		return Decision{}, false
	}
	if _, err := m.sandbox.CheckPath(path, PathCheckOptions{AllowMissingLeaf: allowMissing}); err != nil {
		return denyDecision("权限拒绝: "+err.Error(), ScopeBuiltin), true
	}
	return Decision{}, false
}

func (m *Manager) orderedRuleSets() []RuleSet {
	sets := []RuleSet{m.session}
	for _, scope := range []Scope{ScopeLocal, ScopeProject, ScopeUser} {
		for _, set := range m.rules {
			if set.Scope == scope {
				sets = append(sets, set)
			}
		}
	}
	return sets
}

func (m *Manager) defaultByMode(req Request, target Target) Decision {
	category := categoryForRequest(req)
	action := modeDecision(m.mode, category)
	reason := modeReason(m.mode, category, action)
	switch action {
	case ActionAllow:
		return Decision{Action: ActionAllow, Reason: reason, Scope: ScopeMode}
	case ActionDeny:
		return denyDecision(reason, ScopeMode)
	default:
		return askDecision(req, target, reason)
	}
}

func askDecision(req Request, target Target, reason string) Decision {
	return Decision{
		Action: ActionAsk,
		Reason: reason,
		Scope:  ScopeMode,
		Confirm: &ConfirmationRequest{
			ID:          req.ID,
			Tool:        NormalizeToolName(req.Tool),
			ArgsPreview: fmt.Sprintf("%v", req.Args),
			Target:      target.Value,
			Risk:        riskForRequest(req),
			Reason:      reason,
		},
	}
}

func denyDecision(reason string, scope Scope) Decision {
	return Decision{Action: ActionDeny, Reason: reason, Scope: scope}
}

func riskForRequest(req Request) string {
	switch NormalizeToolName(req.Tool) {
	case "bash":
		return "命令可能产生文件、进程或系统副作用"
	case "write_file", "edit_file":
		return "工具会修改工作区文件"
	default:
		return "需要确认该工具调用"
	}
}

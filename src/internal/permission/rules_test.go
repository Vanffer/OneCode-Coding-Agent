package permission

import "testing"

func TestParseRule(t *testing.T) {
	rule, err := ParseRule("Bash(git status): allow", ScopeUser)
	if err != nil {
		t.Fatalf("ParseRule returned error: %v", err)
	}
	if rule.Tool != "bash" || rule.Pattern != "git status" || rule.Action != ActionAllow || rule.Scope != ScopeUser {
		t.Fatalf("unexpected rule: %+v", rule)
	}

	rule, err = ParseRule("ReadFile(src/**/*.go): deny", ScopeProject)
	if err != nil {
		t.Fatalf("ParseRule returned error: %v", err)
	}
	if rule.Tool != "read_file" || rule.Pattern != "src/**/*.go" || rule.Action != ActionDeny {
		t.Fatalf("unexpected rule: %+v", rule)
	}
}

func TestParseRuleRejectsInvalid(t *testing.T) {
	tests := []string{
		"Bash git status: allow",
		"Bash(): allow",
		"(git status): allow",
		"Bash(git status): ask",
	}
	for _, raw := range tests {
		if _, err := ParseRule(raw, ScopeUser); err == nil {
			t.Fatalf("expected rule %q to fail", raw)
		}
	}
}

func TestMatchRule(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		req    Request
		target Target
		want   bool
	}{
		{
			name:   "exact bash command",
			raw:    "Bash(git status): allow",
			req:    Request{Tool: "bash"},
			target: Target{Kind: TargetCommand, Value: "git status", Command: "git status"},
			want:   true,
		},
		{
			name:   "exact does not match suffix",
			raw:    "Bash(git status): allow",
			req:    Request{Tool: "bash"},
			target: Target{Kind: TargetCommand, Value: "git status --short", Command: "git status --short"},
			want:   false,
		},
		{
			name:   "glob command",
			raw:    "Bash(git *): allow",
			req:    Request{Tool: "bash"},
			target: Target{Kind: TargetCommand, Value: "git diff", Command: "git diff"},
			want:   true,
		},
		{
			name:   "command uses command field not generic value",
			raw:    "Bash(git status): allow",
			req:    Request{Tool: "bash"},
			target: Target{Kind: TargetCommand, Value: "git status", Command: "git diff"},
			want:   false,
		},
		{
			name:   "double star path",
			raw:    "ReadFile(src/**/*.go): allow",
			req:    Request{Tool: "read_file"},
			target: Target{Kind: TargetPath, Value: "src/internal/agent/loop.go", Path: "src/internal/agent/loop.go"},
			want:   true,
		},
		{
			name:   "search can match glob filter",
			raw:    "Grep(**/*.go): allow",
			req:    Request{Tool: "grep"},
			target: Target{Kind: TargetSearch, Value: "src", SearchRoot: "src", Glob: "**/*.go"},
			want:   true,
		},
		{
			name:   "wrong tool",
			raw:    "Grep(src): allow",
			req:    Request{Tool: "glob"},
			target: Target{Kind: TargetSearch, Value: "src", SearchRoot: "src"},
			want:   false,
		},
		{
			name:   "unknown falls back to generic value",
			raw:    "Custom(custom target): allow",
			req:    Request{Tool: "custom"},
			target: Target{Kind: TargetUnknown, Value: "custom target"},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := ParseRule(tt.raw, ScopeUser)
			if err != nil {
				t.Fatalf("ParseRule returned error: %v", err)
			}
			if got := MatchRule(rule, tt.req, tt.target); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestGenerateExactAllowRule(t *testing.T) {
	req := Request{Tool: "bash"}
	target := Target{Kind: TargetCommand, Value: "git status", Command: "git status"}
	rule := GenerateExactAllowRule(req, target, ScopeSession)
	if FormatRule(rule) != "Bash(git status): allow" {
		t.Fatalf("unexpected formatted rule: %s", FormatRule(rule))
	}
}

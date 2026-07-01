package permission

import (
	"fmt"
	"path/filepath"
	"strings"

	"onecode/internal/tools/searchutil"
)

// NormalizeToolName converts user-friendly tool names to internal names.
func NormalizeToolName(name string) string {
	cleaned := strings.TrimSpace(name)
	lower := strings.ToLower(cleaned)
	lower = strings.ReplaceAll(lower, "-", "_")
	switch lower {
	case "bash":
		return "bash"
	case "readfile", "read_file":
		return "read_file"
	case "writefile", "write_file":
		return "write_file"
	case "editfile", "edit_file":
		return "edit_file"
	case "glob":
		return "glob"
	case "grep":
		return "grep"
	default:
		return lower
	}
}

// DisplayToolName converts internal tool names to rule-friendly names.
func DisplayToolName(name string) string {
	switch NormalizeToolName(name) {
	case "bash":
		return "Bash"
	case "read_file":
		return "ReadFile"
	case "write_file":
		return "WriteFile"
	case "edit_file":
		return "EditFile"
	case "glob":
		return "Glob"
	case "grep":
		return "Grep"
	default:
		return name
	}
}

// ParseRule parses a rule in the form Tool(pattern): allow.
func ParseRule(raw string, scope Scope) (Rule, error) {
	text := strings.TrimSpace(raw)
	open := strings.Index(text, "(")
	close := strings.LastIndex(text, ")")
	colon := strings.LastIndex(text, ":")
	if open <= 0 || close <= open || colon <= close {
		return Rule{}, fmt.Errorf("权限规则格式无效: %q", raw)
	}

	tool := NormalizeToolName(text[:open])
	pattern := strings.TrimSpace(text[open+1 : close])
	actionText := strings.ToLower(strings.TrimSpace(text[colon+1:]))
	if tool == "" {
		return Rule{}, fmt.Errorf("权限规则工具名为空: %q", raw)
	}
	if pattern == "" {
		return Rule{}, fmt.Errorf("权限规则模式为空: %q", raw)
	}

	var action Action
	switch Action(actionText) {
	case ActionAllow:
		action = ActionAllow
	case ActionDeny:
		action = ActionDeny
	default:
		return Rule{}, fmt.Errorf("权限规则动作无效: %q", raw)
	}

	return Rule{
		Tool:    tool,
		Pattern: normalizePattern(pattern),
		Action:  action,
		Scope:   scope,
	}, nil
}

// MatchRule reports whether a rule applies to the tool request and target.
func MatchRule(rule Rule, req Request, target Target) bool {
	if NormalizeToolName(rule.Tool) != NormalizeToolName(req.Tool) {
		return false
	}

	values := matchValues(target)
	for _, value := range values {
		if value == "" {
			continue
		}
		if matchPattern(rule.Pattern, normalizePattern(value)) {
			return true
		}
	}
	return false
}

// GenerateExactAllowRule creates a non-glob allow rule for an approved request.
func GenerateExactAllowRule(req Request, target Target, scope Scope) Rule {
	value := target.Value
	if value == "" {
		value = ExtractTarget(req).Value
	}
	return Rule{
		Tool:    NormalizeToolName(req.Tool),
		Pattern: normalizePattern(value),
		Action:  ActionAllow,
		Scope:   scope,
	}
}

func FormatRule(rule Rule) string {
	return fmt.Sprintf("%s(%s): %s", DisplayToolName(rule.Tool), rule.Pattern, rule.Action)
}

func matchValues(target Target) []string {
	values := []string{target.Value}
	switch target.Kind {
	case TargetCommand:
		values = append(values, target.Command)
	case TargetPath:
		values = append(values, target.Path)
	case TargetSearch:
		values = append(values, target.SearchRoot, target.Glob)
	}
	return values
}

func matchPattern(pattern, value string) bool {
	if !hasGlobMeta(pattern) {
		return pattern == value
	}
	matched, err := searchutil.MatchPattern(pattern, value)
	return err == nil && matched
}

func hasGlobMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func normalizePattern(value string) string {
	value = strings.TrimSpace(value)
	return filepath.ToSlash(value)
}

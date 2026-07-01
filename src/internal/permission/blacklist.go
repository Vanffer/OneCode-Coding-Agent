package permission

import (
	"regexp"
	"strings"
)

// DangerPattern is an internal, non-configurable hard block rule.
type DangerPattern struct {
	Name    string
	Pattern *regexp.Regexp
	Message string
}

// Blacklist contains hard-coded dangerous command patterns.
type Blacklist struct {
	patterns []DangerPattern
}

func NewBlacklist() Blacklist {
	return Blacklist{patterns: []DangerPattern{
		{
			Name:    "remove-root",
			Pattern: regexp.MustCompile(`(?i)\brm\s+(-[^\s]*[rf][^\s]*|-[^\s]*[fr][^\s]*)\s+(/|/\*|~|~[/\\]|\$HOME)\b?`),
			Message: "危险操作黑名单拦截: 拒绝递归删除根目录或用户主目录",
		},
		{
			Name:    "windows-system-delete",
			Pattern: regexp.MustCompile(`(?i)\b(rmdir|rd|del)\b.*\b(/s|/q|-r|-f)\b.*(c:\\windows|c:\\|%windir%|%systemroot%)`),
			Message: "危险操作黑名单拦截: 拒绝删除 Windows 系统目录",
		},
		{
			Name:    "format-disk",
			Pattern: regexp.MustCompile(`(?i)\b(format|mkfs(\.[a-z0-9]+)?)\b\s+([a-z]:|/dev/[a-z]+|/dev/nvme[0-9]+n[0-9]+)`),
			Message: "危险操作黑名单拦截: 拒绝格式化磁盘或分区",
		},
		{
			Name:    "disk-wipe",
			Pattern: regexp.MustCompile(`(?i)\bdd\b.*\bof=/dev/`),
			Message: "危险操作黑名单拦截: 拒绝直接写入磁盘设备",
		},
		{
			Name:    "fork-bomb",
			Pattern: regexp.MustCompile(`:\(\)\s*\{\s*:\|:\s*&\s*\}\s*;:`),
			Message: "危险操作黑名单拦截: 拒绝 fork bomb",
		},
	}}
}

func (b Blacklist) Check(target Target) (Decision, bool) {
	if target.Kind != TargetCommand {
		return Decision{}, false
	}
	command := strings.TrimSpace(target.Command)
	for _, pattern := range b.patterns {
		if pattern.Pattern.MatchString(command) {
			return Decision{
				Action: ActionDeny,
				Reason: pattern.Message,
				Scope:  ScopeBuiltin,
			}, true
		}
	}
	return Decision{}, false
}

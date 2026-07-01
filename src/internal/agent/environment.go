package agent

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"onecode/internal/prompt"
	"onecode/internal/tools"
)

const (
	gitStatusTimeout        = 200 * time.Millisecond
	gitStatusFailureBackoff = 30 * time.Second
	gitStatusMaxLines       = 20
	gitStatusMaxRunes       = 2000
)

var gitStatusFailures = struct {
	sync.Mutex
	until map[string]time.Time
}{until: map[string]time.Time{}}

func buildRequestContext(ctx context.Context, opts RunOptions, iteration int) prompt.RequestContext {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return prompt.RequestContext{
		Mode:             opts.Mode.String(),
		Iteration:        iteration,
		CWD:              cwd,
		OS:               runtime.GOOS,
		Arch:             runtime.GOARCH,
		Shell:            tools.CommandShellName(),
		GitStatus:        collectGitStatus(ctx, cwd),
		Now:              time.Now(),
		ReminderInterval: opts.ReminderInterval,
	}
}

func collectGitStatus(parent context.Context, cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	if gitStatusFailedRecently(cwd) {
		return ""
	}

	ctx, cancel := context.WithTimeout(parent, gitStatusTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v1", "-b")
	cmd.Dir = cwd
	cmd.Env = gitStatusEnv()

	output, err := cmd.Output()
	if err != nil || ctx.Err() != nil {
		rememberGitStatusFailure(cwd)
		return ""
	}
	return limitGitStatus(string(output))
}

func gitStatusFailedRecently(cwd string) bool {
	gitStatusFailures.Lock()
	defer gitStatusFailures.Unlock()

	until, ok := gitStatusFailures.until[cwd]
	if !ok {
		return false
	}
	if time.Now().Before(until) {
		return true
	}
	delete(gitStatusFailures.until, cwd)
	return false
}

func rememberGitStatusFailure(cwd string) {
	gitStatusFailures.Lock()
	defer gitStatusFailures.Unlock()
	gitStatusFailures.until[cwd] = time.Now().Add(gitStatusFailureBackoff)
}

func gitStatusEnv() []string {
	if runtime.GOOS != "windows" {
		return []string{}
	}

	var env []string
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func limitGitStatus(status string) string {
	status = strings.TrimSpace(strings.ReplaceAll(status, "\r\n", "\n"))
	if status == "" {
		return ""
	}

	truncated := false
	lines := strings.Split(status, "\n")
	if len(lines) > gitStatusMaxLines {
		lines = lines[:gitStatusMaxLines]
		truncated = true
	}

	status = strings.Join(lines, "\n")
	if utf8.RuneCountInString(status) > gitStatusMaxRunes {
		status = string([]rune(status)[:gitStatusMaxRunes])
		truncated = true
	}
	if truncated {
		status = strings.TrimRight(status, "\n") + "\n... truncated"
	}
	return status
}

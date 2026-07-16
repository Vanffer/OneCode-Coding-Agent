package conversation

import (
	"context"
	"fmt"

	"onecode/internal/llm"
)

// Compactor performs conversation-level compaction.
type Compactor struct {
	Estimator TokenEstimator
	Summary   SummaryBuilder
}

// CompactResult describes a completed compaction attempt.
type CompactResult struct {
	Messages []llm.Message
	Summary  string
	Usage    UsageEstimate
	Statuses []ContextStatus
}

// ShouldCompact reports whether compaction should run for a mode.
func (c Compactor) ShouldCompact(usage UsageEstimate, window WindowInfo, mode CompactMode, opts ContextOptions, fuse CompactFuse) bool {
	opts = normalizeContextOptions(opts)
	limit := window.Limit
	if limit <= 0 {
		limit = defaultContextWindow
	}

	switch mode {
	case CompactModeManual, CompactModeEmergency:
		return true
	case CompactModeForce:
		return usage.Used >= limit-opts.ForceSafetyMarginTokens
	default:
		if fuse.Tripped {
			return false
		}
		return usage.Used >= limit-opts.SummaryReserveTokens-opts.AutoSafetyMarginTokens
	}
}

// Compact summarizes older messages and preserves recent raw messages.
func (c Compactor) Compact(ctx context.Context, messages []llm.Message, state ContextState, compressor Compressor, mode CompactMode, opts ContextOptions) (CompactResult, error) {
	if err := ctx.Err(); err != nil {
		return CompactResult{}, err
	}
	if compressor == nil {
		return CompactResult{}, fmt.Errorf("压缩器未配置")
	}
	if c.Estimator == (TokenEstimator{}) {
		c.Estimator = TokenEstimator{}
	}
	opts = normalizeContextOptions(opts)

	statuses := []ContextStatus{{
		Kind:    ContextStatusCompactStarted,
		Message: compactStartedMessage(mode),
		Usage:   state.Usage,
	}}

	recent, older := c.recentMessages(messages, opts)
	if len(older) == 0 {
		older = cloneMessages(messages)
		recent = nil
	}

	builder := c.Summary
	input := builder.BuildInput(
		older,
		fileIndexEntries(state.Files),
		nil,
		opts.SummaryReserveTokens,
	)
	output, err := compressor.Summarize(ctx, input)
	if err != nil {
		statuses = append(statuses, ContextStatus{
			Kind:    ContextStatusCompactFailed,
			Message: fmt.Sprintf("上下文压缩失败: %s", err.Error()),
			Usage:   state.Usage,
		})
		return CompactResult{Statuses: statuses}, err
	}
	if output.Summary == "" {
		statuses = append(statuses, ContextStatus{
			Kind:    ContextStatusCompactFailed,
			Message: "上下文压缩失败: 摘要为空",
			Usage:   state.Usage,
		})
		return CompactResult{Statuses: statuses}, fmt.Errorf("压缩摘要为空")
	}

	boundary := builder.BoundaryMessage(output.Summary, fileIndexEntries(state.Files))
	newMessages := make([]llm.Message, 0, 1+len(recent))
	newMessages = append(newMessages, boundary)
	newMessages = append(newMessages, recent...)
	usage := c.Estimator.Estimate(newMessages, state.Window, UsageAnchor{})
	statuses = append(statuses, ContextStatus{
		Kind:    ContextStatusCompactCompleted,
		Message: "上下文压缩完成",
		Usage:   usage,
	})

	return CompactResult{
		Messages: newMessages,
		Summary:  output.Summary,
		Usage:    usage,
		Statuses: statuses,
	}, nil
}

func (c Compactor) recentMessages(messages []llm.Message, opts ContextOptions) ([]llm.Message, []llm.Message) {
	opts = normalizeContextOptions(opts)
	tokens := 0
	count := 0
	start := len(messages)
	for start > 0 && (count < opts.RecentMinMessages || tokens < opts.RecentTokens) {
		start--
		tokens += c.Estimator.EstimateMessage(messages[start])
		count++
	}
	start = c.adjustRecentStart(messages, start)
	recent := cloneMessages(messages[start:])
	older := cloneMessages(messages[:start])
	return recent, older
}

func (c Compactor) adjustRecentStart(messages []llm.Message, start int) int {
	if start <= 0 || start >= len(messages) {
		return start
	}
	for {
		next := start
		if isToolResultMessage(messages[start]) {
			if owner := findToolCallOwner(messages, start); owner >= 0 {
				next = owner
			}
		}
		if next == start && messages[start].Role == "assistant" && start > 0 && messages[start-1].Role == "user" {
			next = start - 1
		}
		if next == start {
			return start
		}
		start = next
		if start <= 0 {
			return 0
		}
	}
}

func findToolCallOwner(messages []llm.Message, toolIndex int) int {
	if toolIndex <= 0 || toolIndex >= len(messages) || !isToolResultMessage(messages[toolIndex]) {
		return -1
	}
	toolUseID := messages[toolIndex].ToolResult.ToolUseID
	for i := toolIndex - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "assistant" {
			if hasToolCallID(msg.ToolCalls, toolUseID) {
				return i
			}
			return -1
		}
		if msg.Role != "tool" {
			return -1
		}
	}
	return -1
}

func hasToolCallID(calls []llm.ToolCall, id string) bool {
	if id == "" {
		return false
	}
	for _, call := range calls {
		if call.ID == id {
			return true
		}
	}
	return false
}

func fileIndexEntries(idx *FileIndex) []FileIndexEntry {
	if idx == nil {
		return nil
	}
	return idx.Recent(defaultFileIndexLimit)
}

func compactStartedMessage(mode CompactMode) string {
	switch mode {
	case CompactModeManual:
		return "正在手动压缩上下文"
	case CompactModeEmergency:
		return "上下文过长，正在紧急压缩"
	case CompactModeForce:
		return "上下文接近上限，正在强制压缩"
	default:
		return "正在自动压缩上下文"
	}
}

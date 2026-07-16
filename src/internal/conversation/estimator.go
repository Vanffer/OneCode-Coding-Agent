package conversation

import (
	"encoding/json"
	"time"

	"onecode/internal/llm"
)

const messageTokenOverhead = 4

// TokenEstimator estimates context usage without a model-specific tokenizer.
type TokenEstimator struct{}

// Estimate returns the current estimated context usage.
func (e TokenEstimator) Estimate(messages []llm.Message, window WindowInfo, anchor UsageAnchor) UsageEstimate {
	limit := window.Limit
	if limit <= 0 {
		limit = defaultContextWindow
	}

	used := 0
	estimated := true
	start := 0
	if anchor.Usage.Available && anchor.MessageCount >= 0 && anchor.MessageCount <= len(messages) {
		used = usageTotal(anchor.Usage)
		start = anchor.MessageCount
		estimated = anchor.MessageCount != len(messages)
	}

	for _, msg := range messages[start:] {
		used += e.EstimateMessage(msg)
	}

	percent := 0
	if limit > 0 {
		percent = used * 100 / limit
	}
	return UsageEstimate{
		Used:      used,
		Limit:     limit,
		Percent:   percent,
		Estimated: estimated,
		Anchor:    anchor,
		UpdatedAt: time.Now(),
	}
}

// EstimateText estimates tokens from character count.
func (e TokenEstimator) EstimateText(text string) int {
	runes := len([]rune(text))
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

// EstimateMessage estimates the token usage of one conversation message.
func (e TokenEstimator) EstimateMessage(msg llm.Message) int {
	total := messageTokenOverhead + e.EstimateText(msg.Role) + e.EstimateText(msg.Content)
	for _, call := range msg.ToolCalls {
		total += messageTokenOverhead
		total += e.EstimateText(call.ID)
		total += e.EstimateText(call.Name)
		if data, err := json.Marshal(call.Input); err == nil {
			total += e.EstimateText(string(data))
		}
	}
	if msg.ToolResult != nil {
		total += messageTokenOverhead
		total += e.EstimateText(msg.ToolResult.ToolUseID)
		total += e.EstimateText(msg.ToolResult.Content)
	}
	return total
}

func usageTotal(usage llm.Usage) int {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.InputTokens +
		usage.OutputTokens +
		usage.Cache.CreationInputTokens +
		usage.Cache.ReadInputTokens
}

package conversation

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"onecode/internal/llm"
)

const storedToolResultMarker = "[onecode:stored-tool-result]"

// ToolResultBounder moves oversized tool results out of the model-visible history.
type ToolResultBounder struct {
	Store *ProjectStore
}

// BoundOptions configures tool result bounding thresholds.
type BoundOptions struct {
	SingleMaxTokens int
	BatchMaxTokens  int
}

// BoundResult describes tool results stored during bounding.
type BoundResult struct {
	Messages []llm.Message
	Stored   []StoredToolResult
	Changed  bool
}

// Bound stores oversized tool results and returns updated model-visible messages.
func (b *ToolResultBounder) Bound(messages []llm.Message, opts BoundOptions) (BoundResult, error) {
	if opts.SingleMaxTokens <= 0 {
		opts.SingleMaxTokens = defaultToolResultMaxTokens
	}
	if opts.BatchMaxTokens <= 0 {
		opts.BatchMaxTokens = defaultToolResultBatchMaxToken
	}
	if b.Store == nil {
		b.Store = NewProjectStore(".")
	}

	estimator := TokenEstimator{}
	out := cloneMessages(messages)
	result := BoundResult{Messages: out}

	for i := range out {
		if !isToolResultMessage(out[i]) || isStoredToolResult(out[i].ToolResult.Content) {
			continue
		}
		if estimator.EstimateText(out[i].ToolResult.Content) <= opts.SingleMaxTokens {
			continue
		}
		stored, err := b.storeAndReplace(&out[i])
		if err != nil {
			return result, err
		}
		result.Stored = append(result.Stored, stored)
		result.Changed = true
	}

	for start := 0; start < len(out); {
		if !isToolResultMessage(out[start]) {
			start++
			continue
		}
		end := start + 1
		for end < len(out) && isToolResultMessage(out[end]) {
			end++
		}
		stored, changed, err := b.boundBatch(out[start:end], opts.BatchMaxTokens, estimator)
		if err != nil {
			return result, err
		}
		result.Stored = append(result.Stored, stored...)
		result.Changed = result.Changed || changed
		start = end
	}

	result.Messages = out
	return result, nil
}

func (b *ToolResultBounder) boundBatch(messages []llm.Message, maxTokens int, estimator TokenEstimator) ([]StoredToolResult, bool, error) {
	total := 0
	type candidate struct {
		index  int
		tokens int
	}
	var candidates []candidate
	for i := range messages {
		if !isToolResultMessage(messages[i]) {
			continue
		}
		tokens := estimator.EstimateText(messages[i].ToolResult.Content)
		total += tokens
		if !isStoredToolResult(messages[i].ToolResult.Content) {
			candidates = append(candidates, candidate{index: i, tokens: tokens})
		}
	}
	if total <= maxTokens {
		return nil, false, nil
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].tokens > candidates[j].tokens
	})

	var stored []StoredToolResult
	changed := false
	for _, candidate := range candidates {
		if total <= maxTokens {
			break
		}
		oldTokens := estimator.EstimateText(messages[candidate.index].ToolResult.Content)
		item, err := b.storeAndReplace(&messages[candidate.index])
		if err != nil {
			return stored, changed, err
		}
		newTokens := estimator.EstimateText(messages[candidate.index].ToolResult.Content)
		total -= oldTokens - newTokens
		stored = append(stored, item)
		changed = true
	}
	return stored, changed, nil
}

func (b *ToolResultBounder) storeAndReplace(msg *llm.Message) (StoredToolResult, error) {
	if !isToolResultMessage(*msg) {
		return StoredToolResult{}, fmt.Errorf("消息不是工具结果")
	}
	preview := previewText(msg.ToolResult.Content, 1200)
	stored, err := b.Store.StoreToolResult(context.Background(), *msg.ToolResult, preview)
	if err != nil {
		return StoredToolResult{}, err
	}
	msg.ToolResult.Content = storedToolResultContent(stored)
	return stored, nil
}

func storedToolResultContent(stored StoredToolResult) string {
	var builder strings.Builder
	builder.WriteString(storedToolResultMarker)
	builder.WriteString("\n完整工具结果已保存到: ")
	builder.WriteString(stored.Path)
	builder.WriteString("\n如需具体细节，请使用 read_file 重新读取该路径；不要根据预览虚构完整内容。\n\n")
	builder.WriteString("预览:\n")
	builder.WriteString(stored.Preview)
	return builder.String()
}

func isStoredToolResult(content string) bool {
	return strings.Contains(content, storedToolResultMarker)
}

func isToolResultMessage(msg llm.Message) bool {
	return msg.Role == "tool" && msg.ToolResult != nil
}

func cloneMessages(messages []llm.Message) []llm.Message {
	out := make([]llm.Message, len(messages))
	for i, msg := range messages {
		out[i] = msg
		if msg.ToolCalls != nil {
			out[i].ToolCalls = append([]llm.ToolCall(nil), msg.ToolCalls...)
		}
		if msg.ToolResult != nil {
			result := *msg.ToolResult
			out[i].ToolResult = &result
		}
	}
	return out
}

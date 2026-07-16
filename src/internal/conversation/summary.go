package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"onecode/internal/llm"
)

const (
	formalSummaryOpen          = "<formal_summary>"
	formalSummaryClose         = "</formal_summary>"
	contextSummaryBoundaryMark = "<context-summary-boundary>"
)

// Compressor summarizes older conversation history.
type Compressor interface {
	Summarize(ctx context.Context, input CompactInput) (CompactOutput, error)
}

// CompactInput is the model-facing input for compaction.
type CompactInput struct {
	Messages      []llm.Message
	FileIndex     []FileIndexEntry
	StoredResults []StoredToolResult
	BudgetTokens  int
}

// CompactOutput is a completed compaction response.
type CompactOutput struct {
	Summary string
}

// SummaryBuilder builds and parses compaction summaries.
type SummaryBuilder struct{}

// BuildInput snapshots compaction inputs.
func (b SummaryBuilder) BuildInput(messages []llm.Message, fileIndex []FileIndexEntry, stored []StoredToolResult, budget int) CompactInput {
	return CompactInput{
		Messages:      cloneMessages(messages),
		FileIndex:     append([]FileIndexEntry(nil), fileIndex...),
		StoredResults: append([]StoredToolResult(nil), stored...),
		BudgetTokens:  budget,
	}
}

// Prompt renders a dedicated compaction prompt.
func (b SummaryBuilder) Prompt(input CompactInput) string {
	var builder strings.Builder
	builder.WriteString("You are compacting conversation history for OneCode.\n")
	builder.WriteString("Do not call tools. Do not ask follow-up questions. Do not invent missing details.\n")
	builder.WriteString("First write private analysis inside <analysis_draft>, then write the final summary inside <formal_summary>.\n")
	builder.WriteString("Only the formal summary will be kept. Keep it concise but specific.\n")
	if input.BudgetTokens > 0 {
		builder.WriteString(fmt.Sprintf("Target summary budget: %d tokens.\n", input.BudgetTokens))
	}
	builder.WriteString("\nThe formal summary must use these sections:\n")
	for _, section := range summarySections() {
		builder.WriteString("- ")
		builder.WriteString(section)
		builder.WriteString("\n")
	}

	builder.WriteString("\nRecent file index:\n")
	if len(input.FileIndex) == 0 {
		builder.WriteString("- none\n")
	} else {
		for _, entry := range input.FileIndex {
			builder.WriteString(formatFileIndexEntry(entry))
		}
	}

	builder.WriteString("\nStored tool results:\n")
	if len(input.StoredResults) == 0 {
		builder.WriteString("- none\n")
	} else {
		for _, stored := range input.StoredResults {
			builder.WriteString(fmt.Sprintf("- %s (%d bytes): %s\n", stored.Path, stored.Bytes, oneLine(stored.Preview)))
		}
	}

	builder.WriteString("\nConversation messages to compact:\n")
	for i, msg := range input.Messages {
		builder.WriteString(formatMessageForSummary(i, msg))
	}

	builder.WriteString("\nRequired output format:\n")
	builder.WriteString("<analysis_draft>\n...\n</analysis_draft>\n\n")
	builder.WriteString("<formal_summary>\n")
	for _, section := range summarySections() {
		builder.WriteString("## ")
		builder.WriteString(section)
		builder.WriteString("\n")
	}
	builder.WriteString("</formal_summary>\n")
	return builder.String()
}

// ParseOutput extracts the formal summary and discards any draft text.
func (b SummaryBuilder) ParseOutput(raw string) (string, error) {
	start := strings.Index(raw, formalSummaryOpen)
	end := strings.Index(raw, formalSummaryClose)
	if start < 0 || end < 0 || end <= start {
		return "", fmt.Errorf("压缩摘要缺少 formal_summary 区块")
	}
	summary := strings.TrimSpace(raw[start+len(formalSummaryOpen) : end])
	if summary == "" {
		return "", fmt.Errorf("压缩摘要为空")
	}
	return summary, nil
}

// BoundaryMessage builds the model-visible summary boundary message.
func (b SummaryBuilder) BoundaryMessage(summary string, fileIndex []FileIndexEntry) llm.Message {
	var builder strings.Builder
	builder.WriteString(contextSummaryBoundaryMark)
	builder.WriteString("\nThe conversation history before this point has been compacted.\n")
	builder.WriteString("If exact details are needed, re-read the referenced files or stored tool results. Do not invent details from the summary.\n\n")
	builder.WriteString("Summary:\n")
	builder.WriteString(strings.TrimSpace(summary))
	builder.WriteString("\n\nRecent file index:\n")
	if len(fileIndex) == 0 {
		builder.WriteString("- none\n")
	} else {
		for _, entry := range fileIndex {
			builder.WriteString(formatFileIndexEntry(entry))
		}
	}
	return llm.Message{Role: "user", Content: builder.String()}
}

func summarySections() []string {
	return []string{
		"Task Goal",
		"User Constraints",
		"Completed Work",
		"Key Decisions",
		"Important Files And Paths",
		"Open Items",
		"Risks",
		"Next Steps",
	}
}

func formatFileIndexEntry(entry FileIndexEntry) string {
	preview := oneLine(entry.Preview)
	if preview == "" {
		preview = "no preview"
	}
	reason := strings.TrimSpace(entry.Reason)
	if reason == "" {
		reason = "seen"
	}
	return fmt.Sprintf("- %s [%s]: %s\n", entry.Path, reason, preview)
}

func formatMessageForSummary(index int, msg llm.Message) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("\n[%d] role=%s\n", index, msg.Role))
	if msg.Content != "" {
		builder.WriteString(msg.Content)
		builder.WriteString("\n")
	}
	for _, call := range msg.ToolCalls {
		args := "{}"
		if data, err := json.Marshal(call.Input); err == nil {
			args = string(data)
		}
		builder.WriteString(fmt.Sprintf("tool_call id=%s name=%s args=%s\n", call.ID, call.Name, args))
	}
	if msg.ToolResult != nil {
		builder.WriteString(fmt.Sprintf("tool_result id=%s error=%t\n", msg.ToolResult.ToolUseID, msg.ToolResult.IsError))
		builder.WriteString(msg.ToolResult.Content)
		builder.WriteString("\n")
	}
	return builder.String()
}

func oneLine(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	value = strings.ReplaceAll(value, "\n", " ")
	return previewText(value, 240)
}

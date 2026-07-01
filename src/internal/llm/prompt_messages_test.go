package llm

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"onecode/internal/prompt"
)

func TestOpenAIPromptRemindersUseUserMessages(t *testing.T) {
	reminder := "<system-reminder>runtime</system-reminder>"
	messages := buildOpenAIChatMessages([]Message{
		{Role: "user", Content: "hello"},
	}, StreamOptions{Prompt: prompt.Payload{
		StableSystem: "stable system",
		Reminders: []prompt.Reminder{{
			Kind:    prompt.ReminderEnvironment,
			Content: reminder,
		}},
	}})

	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	if messages[0].OfSystem == nil {
		t.Fatalf("expected stable prompt to be system message: %+v", messages[0])
	}
	if messages[1].OfUser == nil {
		t.Fatalf("expected conversation message to stay user message: %+v", messages[1])
	}
	if messages[2].OfUser == nil {
		t.Fatalf("expected reminder to be user message, got: %+v", messages[2])
	}
	if messages[2].OfSystem != nil {
		t.Fatalf("reminder must not be sent as system message: %+v", messages[2])
	}
	if got := messages[2].OfUser.Content.OfString.Value; got != reminder {
		t.Fatalf("unexpected reminder content: %q", got)
	}
}

func TestAnthropicPromptRemindersUseUserMessages(t *testing.T) {
	reminder := "<system-reminder>runtime</system-reminder>"
	systemBlocks := buildAnthropicSystemBlocks("stable system")
	runtimeMsgs := messagesWithReminders([]Message{
		{Role: "user", Content: "hello"},
	}, []prompt.Reminder{{
		Kind:    prompt.ReminderEnvironment,
		Content: reminder,
	}})
	messages, hasToolUse := buildAnthropicMessages(runtimeMsgs)

	if len(systemBlocks) != 1 {
		t.Fatalf("expected only stable system block, got %d", len(systemBlocks))
	}
	if systemBlocks[0].Text != "stable system" {
		t.Fatalf("unexpected stable system text: %q", systemBlocks[0].Text)
	}
	if len(messages) != 1 {
		t.Fatalf("expected merged user message, got %d messages", len(messages))
	}
	if hasToolUse {
		t.Fatal("plain reminders should not be treated as tool use")
	}
	if messages[0].Role != anthropic.MessageParamRoleUser {
		t.Fatalf("expected reminder to be user message, got %q", messages[0].Role)
	}
	if len(messages[0].Content) != 2 || messages[0].Content[1].OfText == nil {
		t.Fatalf("expected reminder text block after user text, got %+v", messages[0].Content)
	}
	if got := messages[0].Content[1].OfText.Text; got != reminder {
		t.Fatalf("unexpected reminder content: %q", got)
	}
}

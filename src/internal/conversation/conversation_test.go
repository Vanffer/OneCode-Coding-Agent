package conversation

import (
	"testing"
)

func TestConversationAddUser(t *testing.T) {
	conv := New()
	conv.AddUser("Hello")

	msgs := conv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected role 'user', got '%s'", msgs[0].Role)
	}
	if msgs[0].Content != "Hello" {
		t.Errorf("expected content 'Hello', got '%s'", msgs[0].Content)
	}
}

func TestConversationAddAssistant(t *testing.T) {
	conv := New()
	conv.AddAssistant("Hi there!")

	msgs := conv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", msgs[0].Role)
	}
	if msgs[0].Content != "Hi there!" {
		t.Errorf("expected content 'Hi there!', got '%s'", msgs[0].Content)
	}
}

func TestConversationMultipleMessages(t *testing.T) {
	conv := New()
	conv.AddUser("What is Go?")
	conv.AddAssistant("Go is a programming language.")
	conv.AddUser("Tell me more.")
	conv.AddAssistant("Go was created by Google.")

	msgs := conv.Messages()
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}

	expected := []struct {
		role    string
		content string
	}{
		{"user", "What is Go?"},
		{"assistant", "Go is a programming language."},
		{"user", "Tell me more."},
		{"assistant", "Go was created by Google."},
	}

	for i, want := range expected {
		if msgs[i].Role != want.role {
			t.Errorf("message %d: expected role '%s', got '%s'", i, want.role, msgs[i].Role)
		}
		if msgs[i].Content != want.content {
			t.Errorf("message %d: expected content '%s', got '%s'", i, want.content, msgs[i].Content)
		}
	}
}

func TestConversationEmpty(t *testing.T) {
	conv := New()
	msgs := conv.Messages()
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}

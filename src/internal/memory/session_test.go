package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"onecode/internal/llm"
)

func TestSessionIDAndCreate(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 11, 12, 0, time.Local)
	store := testSessionStore(t, now)
	store.random = bytes.NewReader([]byte{0xab, 0xcd})
	journal, err := store.Create()
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	defer journal.Close()
	if journal.ID() != "20260717-101112-abcd" {
		t.Fatalf("unexpected session ID: %s", journal.ID())
	}
	if _, err := os.Stat(journal.Path()); err != nil {
		t.Fatalf("session file was not created: %v", err)
	}
}

func TestSessionInvalidID(t *testing.T) {
	store := testSessionStore(t, time.Now())
	for _, id := range []string{"../secret", "20260717-101112-zzzz", "C:/outside"} {
		if _, err := store.Load(id); err == nil {
			t.Fatalf("expected invalid ID %q to fail", id)
		}
	}
}

func TestSessionJournalAppendsParseableRecords(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	store := testSessionStore(t, now)
	journal := mustCreateJournal(t, store)
	if err := journal.AppendMessage(llm.Message{Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := journal.AppendSnapshot([]llm.Message{{Role: "assistant", Content: "summary"}}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	lines := readSessionLines(t, journal.Path())
	if len(lines) != 2 {
		t.Fatalf("expected 2 records, got %d", len(lines))
	}
	for _, line := range lines {
		var record SessionRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSONL record: %v", err)
		}
	}
	if err := journal.AppendMessage(llm.Message{Role: "user", Content: "closed"}); err == nil {
		t.Fatal("expected append after close to fail")
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("second close should be harmless: %v", err)
	}
}

func TestSessionJournalConcurrentAppend(t *testing.T) {
	store := testSessionStore(t, time.Now())
	journal := mustCreateJournal(t, store)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := journal.AppendMessage(llm.Message{Role: "user", Content: "message"}); err != nil {
				t.Errorf("AppendMessage returned error: %v", err)
			}
		}()
	}
	wg.Wait()
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	lines := readSessionLines(t, journal.Path())
	if len(lines) != 20 {
		t.Fatalf("expected 20 records, got %d", len(lines))
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("concurrent write produced invalid JSON: %q", line)
		}
	}
}

func TestSessionRestoreSnapshotBadLineAndLongLine(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	store := testSessionStore(t, now)
	id := "20260717-100000-abcd"
	path := filepath.Join(store.Root, id+".jsonl")
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("中", 70_000)
	records := []string{
		marshalRecord(t, SessionRecord{Type: RecordMessage, Timestamp: now, Message: &llm.Message{Role: "user", Content: "old"}}),
		"{broken",
		marshalRecord(t, SessionRecord{Type: RecordSnapshot, Timestamp: now.Add(time.Minute), Messages: []llm.Message{{Role: "user", Content: "  标题   有  空格\nsecond"}, {Role: "assistant", Content: large}}}),
	}
	if err := os.WriteFile(path, []byte(strings.Join(records, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(result.Messages) != 2 || result.Messages[0].Content == "old" || result.Messages[1].Content != large {
		t.Fatalf("snapshot replay failed: %+v", result.Messages)
	}
	if result.Info.Title != "标题 有 空格" || result.SkippedLines != 1 || result.Warnings[0].Line != 2 {
		t.Fatalf("unexpected metadata/warnings: %+v", result)
	}
}

func TestSessionTitleUsesUnicodeRunes(t *testing.T) {
	title := sessionTitle([]llm.Message{{Role: "user", Content: strings.Repeat("界", 80)}}, "fallback")
	if len([]rune(title)) != 60 || !strings.HasSuffix(title, "界") {
		t.Fatalf("expected 60 complete runes, got %d: %q", len([]rune(title)), title)
	}
}

func TestSessionRestoreToolCalls(t *testing.T) {
	tests := []struct {
		name      string
		messages  []llm.Message
		wantCount int
		truncated bool
	}{
		{
			name: "complete multiple calls",
			messages: []llm.Message{
				{Role: "user", Content: "inspect"},
				{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "a"}, {ID: "b"}}},
				{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "a", Content: "one"}},
				{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "b", Content: "two"}},
				{Role: "assistant", Content: "done"},
			},
			wantCount: 5,
		},
		{
			name: "missing result",
			messages: []llm.Message{
				{Role: "user", Content: "inspect"},
				{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "a"}}},
			},
			wantCount: 1,
			truncated: true,
		},
		{
			name: "wrong result order",
			messages: []llm.Message{
				{Role: "user", Content: "inspect"},
				{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "a"}, {ID: "b"}}},
				{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "b"}},
			},
			wantCount: 1,
			truncated: true,
		},
		{
			name: "orphan result",
			messages: []llm.Message{
				{Role: "user", Content: "inspect"},
				{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "a"}},
			},
			wantCount: 1,
			truncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testSessionStore(t, time.Now())
			journal := mustCreateJournal(t, store)
			for _, message := range tt.messages {
				if err := journal.AppendMessage(message); err != nil {
					t.Fatal(err)
				}
			}
			journal.Close()
			result, err := store.Load(journal.ID())
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Messages) != tt.wantCount || result.Truncated != tt.truncated {
				t.Fatalf("got count=%d truncated=%t warnings=%+v", len(result.Messages), result.Truncated, result.Warnings)
			}
		})
	}
}

func TestSessionListLatestAndReopen(t *testing.T) {
	base := time.Date(2026, 7, 17, 10, 0, 0, 0, time.Local)
	store := testSessionStore(t, base)
	first := mustCreateJournal(t, store)
	if err := first.AppendMessage(llm.Message{Role: "user", Content: "first"}); err != nil {
		t.Fatal(err)
	}
	first.Close()

	store.now = func() time.Time { return base.Add(time.Hour) }
	store.random = bytes.NewReader([]byte{0x12, 0x34})
	second := mustCreateJournal(t, store)
	if err := second.AppendMessage(llm.Message{Role: "user", Content: "second"}); err != nil {
		t.Fatal(err)
	}
	second.Close()

	infos, err := store.List()
	if err != nil || len(infos) != 2 || infos[0].ID != second.ID() {
		t.Fatalf("unexpected list: infos=%+v err=%v", infos, err)
	}
	latest, err := store.Latest()
	if err != nil || latest.Info.ID != second.ID() {
		t.Fatalf("unexpected latest: %+v err=%v", latest, err)
	}
	reopened, err := store.Open(first.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.AppendMessage(llm.Message{Role: "assistant", Content: "continued"}); err != nil {
		t.Fatal(err)
	}
	reopened.Close()
	loaded, err := store.Load(first.ID())
	if err != nil || len(loaded.Messages) != 2 {
		t.Fatalf("reopen did not append: %+v err=%v", loaded, err)
	}
}

func TestSessionLatestIgnoresExpired(t *testing.T) {
	base := time.Date(2026, 7, 17, 10, 0, 0, 0, time.Local)
	store := testSessionStore(t, base.Add(-31*24*time.Hour))
	journal := mustCreateJournal(t, store)
	if err := journal.AppendMessage(llm.Message{Role: "user", Content: "old"}); err != nil {
		t.Fatal(err)
	}
	journal.Close()
	store.now = func() time.Time { return base }
	if _, err := store.Latest(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no recent session, got %v", err)
	}
}

func TestSessionCleanup(t *testing.T) {
	base := time.Date(2026, 7, 17, 10, 0, 0, 0, time.Local)
	store := testSessionStore(t, base.Add(-40*24*time.Hour))
	old := mustCreateJournal(t, store)
	old.AppendMessage(llm.Message{Role: "user", Content: "old"})
	old.Close()
	store.random = bytes.NewReader([]byte{0x12, 0x34})
	active := mustCreateJournal(t, store)
	active.AppendMessage(llm.Message{Role: "user", Content: "active old"})
	active.Close()
	store.now = func() time.Time { return base }
	store.random = bytes.NewReader([]byte{0x56, 0x78})
	recent := mustCreateJournal(t, store)
	recent.AppendMessage(llm.Message{Role: "user", Content: "recent"})
	recent.Close()
	other := filepath.Join(store.Root, "keep.txt")
	os.WriteFile(other, []byte("keep"), 0o600)

	if err := store.Cleanup(base.Add(-30*24*time.Hour), active.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected old session removed, got %v", err)
	}
	for _, path := range []string{active.Path(), recent.Path(), other} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s preserved: %v", path, err)
		}
	}
}

func testSessionStore(t *testing.T, now time.Time) *SessionStore {
	t.Helper()
	return &SessionStore{
		Root:   filepath.Join(t.TempDir(), "sessions"),
		now:    func() time.Time { return now },
		random: bytes.NewReader([]byte{0xab, 0xcd}),
	}
}

func mustCreateJournal(t *testing.T, store *SessionStore) *SessionJournal {
	t.Helper()
	journal, err := store.Create()
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	return journal
}

func readSessionLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func marshalRecord(t *testing.T, record SessionRecord) string {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

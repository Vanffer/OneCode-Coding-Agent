package memory

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNoteFrontmatterRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	for _, category := range []NoteCategory{CategoryPreference, CategoryCorrection, CategoryProjectKnowledge, CategoryReference} {
		note := Note{
			ID: "20260717-100000-abcd", Scope: ScopeProject, Category: category,
			Title: "中文标题", Summary: "简短摘要", Body: "正文\n\n- item",
			CreatedAt: now, UpdatedAt: now,
		}
		data, err := marshalNote(note)
		if err != nil {
			t.Fatal(err)
		}
		got, err := unmarshalNote(data)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != note.ID || got.Category != category || got.Body != note.Body {
			t.Fatalf("round trip mismatch: %+v", got)
		}
	}
}

func TestNoteFrontmatterInvalid(t *testing.T) {
	for _, content := range []string{"plain", "---\nid: bad\n", "---\nid: bad\n---\n"} {
		if _, err := unmarshalNote([]byte(content)); err == nil {
			t.Fatalf("expected invalid note to fail: %q", content)
		}
	}
}

func TestNoteStoreCreateUpdateAndDuplicate(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	store := testNoteStore(t, now)
	create := NoteMutation{Operation: MutationCreate, Note: testNote(ScopeProject, "Initial body")}
	if err := store.Apply([]NoteMutation{create}); err != nil {
		t.Fatal(err)
	}
	if err := store.Apply([]NoteMutation{create}); err != nil {
		t.Fatal(err)
	}
	notes, err := store.readAllNotes(ScopeProject)
	if err != nil || len(notes) != 1 {
		t.Fatalf("expected exact duplicate skipped: notes=%+v err=%v", notes, err)
	}
	createdAt := notes[0].CreatedAt
	store.now = func() time.Time { return now.Add(time.Hour) }
	replacement := testNote(ScopeProject, "Updated body")
	if err := store.Apply([]NoteMutation{{Operation: MutationUpdate, TargetID: notes[0].ID, Note: replacement}}); err != nil {
		t.Fatal(err)
	}
	updated, err := store.readNote(ScopeProject, notes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Body != "Updated body" || !updated.CreatedAt.Equal(createdAt) || !updated.UpdatedAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("unexpected updated note: %+v", updated)
	}
}

func TestNoteStoreRejectsInvalidUpdate(t *testing.T) {
	store := testNoteStore(t, time.Now())
	err := store.Apply([]NoteMutation{{Operation: MutationUpdate, TargetID: "../outside", Note: testNote(ScopeProject, "body")}})
	if err == nil || !strings.Contains(err.Error(), "无效记忆 ID") {
		t.Fatalf("expected invalid update target, got %v", err)
	}
}

func TestNoteIndexRebuildAndBudget(t *testing.T) {
	now := time.Now()
	store := testNoteStore(t, now)
	notesDir := filepath.Join(store.ProjectRoot, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 260; i++ {
		note := testNote(ScopeProject, strings.Repeat("界", 300))
		note.ID = fmt.Sprintf("20260717-100000-%04x", i)
		note.Title = "title"
		note.Summary = strings.Repeat("摘要", 80)
		note.CreatedAt = now
		note.UpdatedAt = now
		data, err := marshalNote(note)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(notesDir, note.ID+".md"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RebuildIndex(ScopeProject); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(store.ProjectRoot, "INDEX.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > indexMaxBytes || lineCount(string(data)) > indexMaxLines || !utf8.Valid(data) {
		t.Fatalf("index exceeded budget: bytes=%d lines=%d utf8=%t", len(data), lineCount(string(data)), utf8.Valid(data))
	}
}

func TestCombinedIndexReservesUserSpace(t *testing.T) {
	store := testNoteStore(t, time.Now())
	if err := os.MkdirAll(store.UserRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.ProjectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	user := "# User\n" + strings.Repeat("user preference\n", 100)
	project := "# Project\n" + strings.Repeat("project knowledge\n", 3000)
	os.WriteFile(filepath.Join(store.UserRoot, "INDEX.md"), []byte(user), 0o600)
	os.WriteFile(filepath.Join(store.ProjectRoot, "INDEX.md"), []byte(project), 0o600)

	combined, err := store.LoadIndexes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(combined, "user preference") || !strings.Contains(combined, "project knowledge") {
		t.Fatalf("expected both scopes in combined index")
	}
	if len(combined) > indexMaxBytes || lineCount(combined) > indexMaxLines || !utf8.ValidString(combined) {
		t.Fatalf("combined index exceeded budget: bytes=%d lines=%d", len(combined), lineCount(combined))
	}
}

func TestNoteStoreDisabledDoesNotReadOrWrite(t *testing.T) {
	store := testNoteStore(t, time.Now())
	store.Enabled = false
	store.ProjectRoot = filepath.Join(t.TempDir(), "missing")
	if got, err := store.LoadIndexes(); err != nil || got != "" {
		t.Fatalf("disabled LoadIndexes returned %q, %v", got, err)
	}
	if err := store.Apply([]NoteMutation{{Operation: MutationCreate, Note: testNote(ScopeProject, "body")}}); err != nil {
		t.Fatalf("disabled Apply returned error: %v", err)
	}
	if _, err := os.Stat(store.ProjectRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disabled store touched disk: %v", err)
	}
}

func TestNoteIndexFailureDoesNotCreateDanglingEntry(t *testing.T) {
	store := testNoteStore(t, time.Now())
	writes := 0
	store.write = func(path string, data []byte, mode os.FileMode) error {
		writes++
		if filepath.Base(path) == "INDEX.md" {
			return errors.New("index failed")
		}
		return atomicWriteFile(path, data, mode)
	}
	err := store.Apply([]NoteMutation{{Operation: MutationCreate, Note: testNote(ScopeProject, "body")}})
	if err == nil || writes < 2 {
		t.Fatalf("expected index failure after note write, writes=%d err=%v", writes, err)
	}
	if _, statErr := os.Stat(filepath.Join(store.ProjectRoot, "INDEX.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unexpected index after failure: %v", statErr)
	}
}

func TestSensitiveContent(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"-----BEGIN PRIVATE KEY-----\nabc", true},
		{"Authorization: Bearer abcdefghijklmnop", true},
		{"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature123", true},
		{"api_key = sk-abcdefghijklmnopqrstuvwxyz", true},
		{"AWS AKIAABCDEFGHIJKLMNOP", true},
		{"token: abcdefghijklmnopqrstuvwxyz1234", true},
		{"sha256: 9f86d081884c7d659a2feaa0c55ad015", false},
		{"Use token budgeting in the prompt", false},
	}
	for _, tt := range tests {
		if got := containsSensitiveContent(tt.value); got != tt.want {
			t.Errorf("containsSensitiveContent(%q)=%t, want %t", tt.value, got, tt.want)
		}
	}
}

func testNoteStore(t *testing.T, now time.Time) *NoteStore {
	t.Helper()
	root := t.TempDir()
	return &NoteStore{
		UserRoot:    filepath.Join(root, "user-memory"),
		ProjectRoot: filepath.Join(root, "project-memory"),
		Enabled:     true,
		now:         func() time.Time { return now },
		random:      bytes.NewReader([]byte{0xab, 0xcd}),
	}
}

func testNote(scope Scope, body string) Note {
	return Note{
		Scope: scope, Category: CategoryProjectKnowledge,
		Title: "Title", Summary: "Summary", Body: body,
		SourceSessionID: "20260717-100000-abcd",
	}
}

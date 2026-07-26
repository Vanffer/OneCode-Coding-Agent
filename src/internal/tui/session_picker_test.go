package tui

import (
	"strings"
	"testing"
	"time"

	"onecode/internal/conversation"
	"onecode/internal/memory"

	tea "charm.land/bubbletea/v2"
)

func TestSessionPickerNavigationWraps(t *testing.T) {
	model := Model{resumeSessions: []memory.SessionInfo{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	model.moveSessionSelection(-1)
	if model.resumeSelectIndex != 2 {
		t.Fatalf("expected wrap to last item, got %d", model.resumeSelectIndex)
	}
	model.moveSessionSelection(1)
	if model.resumeSelectIndex != 0 {
		t.Fatalf("expected wrap to first item, got %d", model.resumeSelectIndex)
	}
}

func TestSessionPickerViewHighlightsAndTruncates(t *testing.T) {
	model := Model{
		conv:              conversation.New(),
		width:             70,
		resumeSelectIndex: 1,
		resumeSessions: []memory.SessionInfo{
			{ID: "20260717-100000-abcd", Title: strings.Repeat("long title ", 20), UpdatedAt: time.Date(2026, 7, 17, 10, 0, 0, 0, time.Local), MessageCount: 4},
			{ID: "20260717-110000-abcd", Title: "selected", UpdatedAt: time.Date(2026, 7, 17, 11, 0, 0, 0, time.Local), MessageCount: 8},
		},
	}
	view := model.viewSessionPicker()
	for _, want := range []string{"selected", "8 msgs", "20260717-110000-abcd", "> "} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected picker to contain %q, got:\n%s", want, view)
		}
	}
}

func TestSessionPickerConfirmAndCancel(t *testing.T) {
	root := t.TempDir()
	model := New(nil, nil, root, MemoryDependencies{})
	model.state = stateSessionPicker
	model.sessionStore = memory.NewSessionStore(root)
	model.resumeSessions = []memory.SessionInfo{{ID: "20260717-110000-abcd"}}

	loadingModel, cmd := model.startSelectedSessionRestore()
	loading := loadingModel.(Model)
	if loading.state != stateSessionLoading || cmd == nil {
		t.Fatalf("expected confirm to start asynchronous restore, state=%v cmd=%v", loading.state, cmd)
	}

	cancelledModel, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	cancelled := cancelledModel.(Model)
	if cancelled.state != stateIdle {
		t.Fatalf("expected escape to keep current session and return idle, got %v", cancelled.state)
	}
}

func TestSessionPickerEmptyReturnsIdle(t *testing.T) {
	model := New(nil, nil, t.TempDir(), MemoryDependencies{})
	model.state = stateSessionPicker
	nextModel, _ := model.startSelectedSessionRestore()
	next := nextModel.(Model)
	if next.state != stateIdle {
		t.Fatalf("expected empty picker to return idle, got %v", next.state)
	}
}

package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *Model) moveSessionSelection(delta int) {
	if len(m.resumeSessions) == 0 {
		m.resumeSelectIndex = 0
		return
	}
	m.resumeSelectIndex = (m.resumeSelectIndex + delta + len(m.resumeSessions)) % len(m.resumeSessions)
}

func (m Model) startSelectedSessionRestore() (tea.Model, tea.Cmd) {
	if m.sessionStore == nil || len(m.resumeSessions) == 0 {
		m.state = stateIdle
		return m, tea.Println("\n没有可恢复的历史会话。")
	}
	if m.resumeSelectIndex < 0 || m.resumeSelectIndex >= len(m.resumeSessions) {
		m.resumeSelectIndex = 0
	}
	id := m.resumeSessions[m.resumeSelectIndex].ID
	m.state = stateSessionLoading
	m.progressStatus = "正在恢复会话"
	m.turnStart = m.clock()
	return m, tea.Batch(loadSession(m.sessionStore, id), m.spinner.Tick)
}

func (m Model) viewSessionPicker() string {
	var out strings.Builder
	out.WriteString(statusBar(m.provider, "Resume session", m.contextDisplay(), m.width))
	out.WriteString("\n\n  Select a session:\n\n")
	for i, session := range m.resumeSessions {
		marker := "  "
		if i == m.resumeSelectIndex {
			marker = "> "
		}
		metadata := fmt.Sprintf("  %s  %d msgs  %s", session.UpdatedAt.Format("2006-01-02 15:04"), session.MessageCount, session.ID)
		titleWidth := m.width - len([]rune(marker+metadata))
		if titleWidth < 8 {
			titleWidth = 8
		}
		title := truncateRunes(session.Title, titleWidth)
		line := marker + title + metadata
		if i == m.resumeSelectIndex {
			out.WriteString(permissionSelectedStyle.Render(line))
		} else {
			out.WriteString(line)
		}
		out.WriteString("\n")
	}
	out.WriteString("\n  Use ↑/↓ to select, Enter to resume, Esc to cancel.\n")
	return out.String()
}

func (m Model) viewSessionLoading() string {
	status := m.progressStatus
	if status == "" {
		status = "正在加载会话"
	}
	return statusBar(m.provider, m.spinner.View()+" "+status, m.contextDisplay(), m.width) + "\n\n  " + status + "...\n"
}

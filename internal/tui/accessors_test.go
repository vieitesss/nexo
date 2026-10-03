package tui

import "github.com/vieitesss/nexo/internal/capabilities"

// Test-only accessors that let model tests inspect and drive state without
// starting a terminal. They are compiled only into the test binary.

// Tab reports the currently visible tab.
func (m Model) Tab() Tab { return m.input.tab }

// Query reports the current search query.
func (m Model) Query() string { return m.input.query.Value() }

// Rows returns a compact copy of the visible row identities for model tests.
func (m Model) Rows() []string {
	rows := make([]string, 0, len(m.rows))
	for _, row := range m.rows {
		rows = append(rows, row.id)
	}
	return rows
}

// Cursor reports the stable identity under the cursor.
func (m Model) Cursor() string { return m.input.cursor }

// Marked returns marked container IDs in visible source order.
func (m Model) Marked() []string {
	refs := m.input.markedTargetsCatalog(m.catalog)
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	return ids
}

// Cancelled reports whether the model closed without an action.
func (m Model) Cancelled() bool { return m.input.cancelled }

// SetToast displays a status message in place of the normal footer hints.
func (m *Model) SetToast(text string) { m.toast = text }

// SetAgents replaces the local agent snapshot.
func (m *Model) SetAgents(agents []capabilities.Agent) {
	m.source.agents = newAgentRecords(agents, "")
	if m.source.catalog != nil {
		m.source.catalog.ReplaceLocalAgents(agents)
		m.source.syncCatalog()
	}
	m.rebuild(false)
}

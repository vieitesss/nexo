package tui

// Input transitions form a module behind Model.Update's seam. Keeping key,
// mouse, folding, and acceptance policy together gives callers one transition
// interface while preserving identity-stable selection for every tab.

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Modal footer actions own input until they are confirmed or cancelled.
	// Mouse clicks and wheel events are ignored rather than cancelling them.
	if m.actionInFlight || m.confirmKill || m.renameMode {
		return m, nil
	}
	event := tea.MouseEvent(msg)
	button := event.Button
	if button == tea.MouseButtonWheelUp || button == tea.MouseButtonWheelDown || button == tea.MouseButtonWheelLeft || button == tea.MouseButtonWheelRight {
		delta := 0
		switch button {
		case tea.MouseButtonWheelUp:
			delta = -1
		case tea.MouseButtonWheelDown:
			delta = 1
		}
		if delta != 0 {
			m.moveCursor(delta)
		}
		return m, nil
	}
	if button != tea.MouseButtonLeft || event.Action != tea.MouseActionPress || event.X < 0 || event.X >= m.width {
		return m, nil
	}
	if event.Y == 0 {
		if tab, ok := m.tabAt(event.X); ok {
			m.switchTo(tab)
		}
		return m, nil
	}
	if event.Y < 2 || event.Y >= m.height-1 {
		return m, nil
	}
	lines, start := m.bodyLayout(m.height - 3)
	lineIndex := start + event.Y - 2
	if lineIndex < 0 || lineIndex >= len(lines) || lines[lineIndex].rowID == "" {
		return m, nil
	}
	id := lines[lineIndex].rowID
	var clicked row
	var ok bool
	for _, candidate := range m.rows {
		if candidate.id == id && candidate.cursorable {
			clicked, ok = candidate, true
			break
		}
	}
	if !ok {
		return m, nil
	}
	if clicked.kind == groupRow {
		m.toggleClickedFold(clicked)
		return m, nil
	}
	if clicked.foldable && m.foldChevronAt(clicked, event.X) {
		m.toggleClickedFold(clicked)
		return m, nil
	}
	if m.cursor == clicked.id {
		return m.accept()
	}
	m.cursor = clicked.id
	return m, nil
}

func (m Model) foldChevronAt(row row, x int) bool {
	if !row.foldable {
		return false
	}
	start := 2 + shell.StringWidth(foldIndent(row))
	return x >= start && x < start+shell.StringWidth(foldArrow(row))
}

func (m *Model) toggleClickedFold(clicked row) {
	if m.tab != ProjectsTab || !clicked.foldable {
		return
	}
	if clicked.expanded {
		m.folded[clicked.id] = true
		delete(m.expanded, clicked.id)
	} else {
		delete(m.folded, clicked.id)
		m.expanded[clicked.id] = true
	}
	m.rebuild(false)
	m.cursor = clicked.id
}

func foldArrow(row row) string {
	if row.expanded {
		return "▾"
	}
	return "▸"
}

func foldIndent(row row) string {
	switch row.kind {
	case groupRow:
		return strings.Repeat("  ", row.depth)
	case itemRow:
		return row.treePrefix
	default:
		return ""
	}
}

func foldPrefix(row row) string {
	indent := foldIndent(row)
	if !row.foldable {
		return indent
	}
	return indent + foldArrow(row) + "  "
}

// foldPrefixStyled renders the tree guides and the fold glyph with one shared
// style, so a group header and a project with worktrees show the same marker.
func foldPrefixStyled(row row) string {
	prefix := muted().Render(foldIndent(row))
	if !row.foldable {
		return prefix
	}
	return prefix + foldStyle().Render(foldArrow(row)) + "  "
}

func (m Model) tabAt(x int) (Tab, bool) {
	offset := 0
	for _, item := range []struct {
		name string
		tab  Tab
	}{{"Agents", AgentsTab}, {"Containers", ContainersTab}, {"Projects", ProjectsTab}} {
		width := shell.StringWidth(item.name) + shell.StringWidth(fmt.Sprintf(" %d", m.projection().MatchCount(item.tab)))
		if x >= offset && x < offset+width {
			return item.tab, true
		}
		offset += width + 3
	}
	return "", false
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		m.cancelled = true
		m.confirmKill = false
		m.renameMode = false
		m.killTargets = nil
		m.cancelBackgroundLoads()
		return m, tea.Quit
	}
	if m.actionInFlight {
		return m, nil
	}
	if m.confirmKill {
		switch strings.ToLower(msg.String()) {
		case "y":
			m.confirmKill = false
			refs := append([]picker.ContainerRef(nil), m.killTargets...)
			m.killTargets = nil
			m.actionInFlight = true
			return m, m.runKill(refs)
		case "n", "esc":
			m.confirmKill = false
			m.killTargets = nil
			return m, nil
		}
		return m, nil
	}
	if m.renameMode {
		switch msg.Type {
		case tea.KeyEsc:
			m.renameMode = false
			m.renameInput = nil
		case tea.KeyEnter:
			if strings.TrimSpace(string(m.renameInput)) != "" {
				m.renameMode = false
				name := string(m.renameInput)
				m.renameInput = nil
				m.actionInFlight = true
				return m, m.runRename(m.renameTarget, name)
			}
		case tea.KeyBackspace, tea.KeyDelete:
			if len(m.renameInput) > 0 {
				m.renameInput = m.renameInput[:len(m.renameInput)-1]
			}
		case tea.KeyRunes:
			m.renameInput = append(m.renameInput, msg.Runes...)
		case tea.KeySpace:
			m.renameInput = append(m.renameInput, ' ')
		}
		return m, nil
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		m.cancelled = true
		m.cancelBackgroundLoads()
		return m, tea.Quit
	case tea.KeyEsc:
		if len(m.query) > 0 {
			m.SetQuery("")
			return m, nil
		}
		m.cancelled = true
		m.cancelBackgroundLoads()
		return m, tea.Quit
	case tea.KeyTab:
		m.switchTab(1)
		return m, nil
	case tea.KeyF1:
		m.switchTo(AgentsTab)
		return m, nil
	case tea.KeyF2:
		m.switchTo(ContainersTab)
		return m, nil
	case tea.KeyF3:
		m.switchTo(ProjectsTab)
		return m, nil
	case tea.KeyCtrlT:
		m.toggleMark()
		return m, nil
	case tea.KeyCtrlX:
		refs := m.markedTargets()
		if len(refs) == 0 {
			if current, ok := m.currentItem(); ok && current.place.Kind == picker.ContainerKind && current.place.ContainerID != "" {
				refs = []picker.ContainerRef{{Machine: current.place.Machine, ID: current.place.ContainerID}}
			}
		}
		if len(refs) > 0 {
			m.killTargets = append([]picker.ContainerRef(nil), refs...)
			m.confirmKill = true
		}
		return m, nil
	case tea.KeyCtrlR:
		if current, ok := m.currentItem(); ok && current.place.Kind == picker.ContainerKind && current.place.ContainerID != "" {
			m.renameMode = true
			m.renameInput = nil
			m.renameTarget = picker.ContainerRef{Machine: current.place.Machine, ID: current.place.ContainerID}
		}
		return m, nil
	case tea.KeyCtrlW:
		m.SetQuery(string(deleteBackToWhitespace(m.query)))
		return m, nil
	case tea.KeyCtrlU:
		m.SetQuery("")
		return m, nil
	case tea.KeyBackspace:
		if msg.Alt {
			m.SetQuery(string(deleteBackToBoundary(m.query)))
		} else if len(m.query) > 0 {
			m.SetQuery(string(m.query[:len(m.query)-1]))
		}
		return m, nil
	case tea.KeyDelete:
		if len(m.query) > 0 {
			m.SetQuery(string(m.query[:len(m.query)-1]))
		}
		return m, nil
	case tea.KeyUp:
		m.moveCursor(-1)
		return m, nil
	case tea.KeyDown:
		m.moveCursor(1)
		return m, nil
	case tea.KeyCtrlN:
		m.moveCursor(1)
		return m, nil
	case tea.KeyCtrlP:
		m.moveCursor(-1)
		return m, nil
	case tea.KeyPgUp:
		m.moveCursor(-m.pageSize())
		return m, nil
	case tea.KeyPgDown:
		m.moveCursor(m.pageSize())
		return m, nil
	case tea.KeyLeft:
		m.foldLeft()
		return m, nil
	case tea.KeyRight:
		m.foldRight()
		return m, nil
	case tea.KeyEnter:
		return m.accept()
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			query := append(append([]rune(nil), m.query...), msg.Runes...)
			m.SetQuery(string(query))
		}
		return m, nil
	case tea.KeySpace:
		m.SetQuery(string(append(append([]rune(nil), m.query...), ' ')))
		return m, nil
	}
	if msg.String() == "shift+tab" {
		m.switchTab(-1)
	}
	return m, nil
}

func deleteBackToWhitespace(query []rune) []rune {
	end := len(query)
	for end > 0 && unicode.IsSpace(query[end-1]) {
		end--
	}
	for end > 0 && !unicode.IsSpace(query[end-1]) {
		end--
	}
	return query[:end]
}

func deleteBackToBoundary(query []rune) []rune {
	end := len(query)
	for end > 0 && !unicode.IsLetter(query[end-1]) && !unicode.IsDigit(query[end-1]) {
		end--
	}
	for end > 0 && (unicode.IsLetter(query[end-1]) || unicode.IsDigit(query[end-1])) {
		end--
	}
	return query[:end]
}

func (m *Model) switchTab(delta int) {
	tabs := []Tab{AgentsTab, ContainersTab, ProjectsTab}
	index := 0
	for i, tab := range tabs {
		if tab == m.tab {
			index = i
		}
	}
	m.switchTo(tabs[(index+delta+len(tabs))%len(tabs)])
}

func (m *Model) switchTo(tab Tab) {
	if !validTab(tab) {
		return
	}
	m.tab = tab
	m.forceFirst = false
	m.rebuild(false)
}

func (m *Model) moveCursor(delta int) {
	if len(m.rows) == 0 {
		return
	}
	indices := []int{}
	current := 0
	for i, row := range m.rows {
		if row.cursorable {
			indices = append(indices, i)
			if row.id == m.cursor {
				current = len(indices) - 1
			}
		}
	}
	if len(indices) == 0 {
		return
	}
	current += delta
	if current < 0 {
		current = 0
	}
	if current >= len(indices) {
		current = len(indices) - 1
	}
	m.cursor = m.rows[indices[current]].id
}

func (m Model) pageSize() int {
	if m.height <= 4 {
		return 1
	}
	return m.height - 4
}

func (m *Model) toggleMark() {
	current, ok := m.currentItem()
	if !ok || current.place.Kind != picker.ContainerKind || current.place.ContainerID == "" {
		return
	}
	ref := picker.ContainerRef{Machine: current.place.Machine, ID: current.place.ContainerID}
	if m.marked[ref] {
		delete(m.marked, ref)
	} else {
		m.marked[ref] = true
	}
	m.rebuild(false)
}

func (m Model) currentContainer() bool {
	current, ok := m.currentItem()
	return ok && current.place.Kind == picker.ContainerKind && current.place.ContainerID != ""
}

func (m Model) runKill(refs []picker.ContainerRef) tea.Cmd {
	return func() tea.Msg {
		if m.deps.KillContainers == nil {
			return actionMsg{kind: "kill", err: errors.New("container killing is not supported")}
		}
		return actionMsg{kind: "kill", err: m.deps.KillContainers(m.ctx, refs)}
	}
}

func (m Model) runRename(ref picker.ContainerRef, name string) tea.Cmd {
	return func() tea.Msg {
		if m.deps.RenameContainer == nil {
			return actionMsg{kind: "rename", err: errors.New("container renaming is not supported")}
		}
		return actionMsg{kind: "rename", err: m.deps.RenameContainer(m.ctx, ref, name)}
	}
}

func (m *Model) foldRight() {
	current, ok := m.currentRow()
	if !ok || m.tab != ProjectsTab || !current.foldable {
		return
	}
	delete(m.folded, current.id)
	m.expanded[current.id] = true
	m.rebuild(false)
	m.cursor = current.id
}

func (m *Model) foldLeft() {
	if m.tab != ProjectsTab || string(m.query) != "" {
		return
	}
	current, ok := m.currentRow()
	if !ok {
		return
	}
	if current.foldable && current.expanded {
		delete(m.expanded, current.id)
		m.folded[current.id] = true
		m.rebuild(false)
		m.cursor = current.id
		return
	}
	parent := foldParent(current)
	if parent == "" {
		return
	}
	delete(m.expanded, parent)
	m.folded[parent] = true
	m.rebuild(false)
	m.cursor = parent
}

func foldParent(current row) string {
	switch current.kind {
	case groupRow:
		if current.expanded {
			return current.id
		}
	case itemRow:
		if current.itemKind != "project" {
			return ""
		}
		if current.place.ParentPath != "" {
			return "project:" + current.place.ParentPath
		}
		return "group:" + current.place.Group
	}
	return ""
}

func (m Model) accept() (tea.Model, tea.Cmd) {
	current, ok := m.currentItem()
	if !ok {
		return m, nil
	}
	if current.itemKind == "agent" {
		value := current.agent
		m.selectedAgent = &value
		m.selectedAgentRef = AgentRef{Agent: value, Machine: current.agentMachine, Backend: current.agentBackend}
	} else {
		value := current.place
		m.selectedPlace = &value
	}
	m.cancelBackgroundLoads()
	return m, tea.Quit
}

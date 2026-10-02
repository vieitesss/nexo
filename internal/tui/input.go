package tui

// Input is the transition module behind Model.Update. Its focused state owns
// query editing, identity-stable selection, folding, modal actions, and the
// selected result. It receives rows and a lazy presentation frame rather than
// the coordinator, so keyboard and mouse transitions can be tested directly
// and ignored events never materialize a catalog snapshot.

import (
	"context"
	"errors"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
)

type inputState struct {
	ctx  context.Context
	deps Deps

	tab         Tab
	query       []rune
	cursor      string
	forceFirst  bool
	cursorMoved bool
	folded      map[string]bool
	expanded    map[string]bool
	marked      map[picker.ContainerRef]bool

	confirmKill      bool
	killTargets      []picker.ContainerRef
	form             *stagedForm
	deleteForm       *deleteForm
	renameMode       bool
	renameInput      []rune
	renameTarget     picker.ContainerRef
	actionInFlight   bool
	actionGeneration uint64

	selectedPlace    *picker.Place
	selectedAgent    *capabilities.Agent
	selectedAgentRef AgentRef

	selectedRemoteTarget *RemoteTarget
	selectedRemoteName   string
	cancelled            bool
}

type inputEffect struct {
	rebuild bool
	quit    bool
	cmd     tea.Cmd
}

type inputFrame struct {
	rows   []row
	height int
	marked func() []picker.ContainerRef
	render func() renderFrame
}

// markedTargets resolves the marked container identities on demand. Only the
// kill transition asks for them, so ordinary key and mouse events never pay
// for the catalog snapshot the marks are derived from.
func (f inputFrame) markedTargets() []picker.ContainerRef {
	if f.marked == nil {
		return nil
	}
	return f.marked()
}

// layout resolves the presentation frame on demand. Pointer transitions build
// it once, when they are about to hit-test a real click.
func (f inputFrame) layout() renderFrame {
	if f.render == nil {
		return renderFrame{}
	}
	return f.render()
}

func newInputState(tab Tab, deps Deps, ctx context.Context) *inputState {
	return &inputState{ctx: ctx, deps: deps, tab: tab,
		folded: make(map[string]bool), expanded: make(map[string]bool), marked: make(map[picker.ContainerRef]bool)}
}

func (s *inputState) setQuery(query string) {
	wasEmpty := len(s.query) == 0
	s.query = []rune(query)
	s.forceFirst = wasEmpty && len(s.query) > 0
}

// reconcileSelection applies identity-stable selection after a projection
// replacement. Keeping this transition beside key and fold handling means the
// coordinator only supplies new rows; selection policy has one interface.
func (s *inputState) reconcileSelection(oldRows, rows []row) {
	old := s.cursor
	oldIndex := -1
	oldKind := machineRow
	for index, candidate := range oldRows {
		if candidate.id == old {
			oldIndex, oldKind = index, candidate.kind
			break
		}
	}
	if s.forceFirst {
		for _, candidate := range rows {
			if candidate.kind == itemRow && candidate.matched {
				s.cursor, s.forceFirst = candidate.id, false
				return
			}
		}
	}
	for _, candidate := range rows {
		if candidate.cursorable && candidate.id == old {
			s.cursor = old
			return
		}
	}
	if oldIndex >= 0 {
		// A query refinement can drop the selected real result while other real
		// results still match. Prefer the nearest remaining matched item so the
		// cursor never slides onto a synthetic creation row and turns Enter into
		// a create action.
		if oldKind == itemRow {
			if nearest, ok := nearestCursorableWhere(rows, oldIndex, func(candidate row) bool { return candidate.kind == itemRow && candidate.matched }); ok {
				s.cursor = nearest
				return
			}
		}
		if nearest, ok := nearestCursorable(rows, oldIndex); ok {
			s.cursor = nearest
			return
		}
	}
	for _, candidate := range rows {
		if candidate.kind == itemRow && candidate.cursorable {
			s.cursor = candidate.id
			return
		}
	}
	for _, candidate := range rows {
		if candidate.cursorable {
			s.cursor = candidate.id
			return
		}
	}
	s.cursor = ""
}

func nearestCursorable(rows []row, index int) (string, bool) {
	return nearestCursorableWhere(rows, index, func(row) bool { return true })
}

// nearestCursorableWhere returns the cursorable row closest to index for which
// match is true. Distance is measured in row positions, so the caller chooses
// which rows are acceptable candidates and the scan keeps index locality.
func nearestCursorableWhere(rows []row, index int, match func(row) bool) (string, bool) {
	if len(rows) == 0 {
		return "", false
	}
	if index < 0 {
		index = 0
	}
	if index >= len(rows) {
		index = len(rows) - 1
	}
	for distance := 0; distance < len(rows); distance++ {
		for _, candidate := range []int{index + distance, index - distance} {
			if candidate < 0 || candidate >= len(rows) || !rows[candidate].cursorable || !match(rows[candidate]) {
				continue
			}
			return rows[candidate].id, true
		}
	}
	return "", false
}

func (s *inputState) currentRow(rows []row) (row, bool) {
	for _, candidate := range rows {
		if candidate.id == s.cursor && candidate.cursorable {
			return candidate, true
		}
	}
	return row{}, false
}

func (s *inputState) currentItem(rows []row) (row, bool) {
	value, ok := s.currentRow(rows)
	return value, ok && value.kind == itemRow
}

func (s *inputState) markedTargetsCatalog(catalog *picker.Catalog) []picker.ContainerRef {
	if catalog == nil {
		return nil
	}
	return catalog.Marked(s.marked)
}

func (s *inputState) mouse(msg tea.MouseMsg, frame inputFrame) inputEffect {
	// A staged form owns the whole screen operation. Never hit-test tabs or
	// rows painted underneath it, including wheel events.
	if s.form != nil || s.actionInFlight || s.confirmKill || s.renameMode {
		return inputEffect{}
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
			s.moveCursor(delta, frame.rows)
		}
		return inputEffect{}
	}
	if button != tea.MouseButtonLeft || event.Action != tea.MouseActionPress {
		return inputEffect{}
	}
	layout := frame.layout()
	if event.X < 0 || event.X >= layout.width {
		return inputEffect{}
	}
	hit := layout.hitTest(event.X, event.Y)
	if hit.tab != "" {
		s.switchTo(hit.tab)
		return inputEffect{rebuild: true}
	}
	if hit.rowID == "" {
		return inputEffect{}
	}
	var clicked row
	var ok bool
	for _, candidate := range frame.rows {
		if candidate.id == hit.rowID && candidate.cursorable {
			clicked, ok = candidate, true
			break
		}
	}
	if !ok {
		return inputEffect{}
	}
	if clicked.kind == groupRow || hit.fold {
		s.toggleClickedFold(clicked)
		return inputEffect{rebuild: true}
	}
	if s.cursor == clicked.id {
		return s.accept(frame.rows)
	}
	s.cursor = clicked.id
	s.forceFirst = false
	s.cursorMoved = true
	return inputEffect{}
}

func (s *inputState) toggleClickedFold(clicked row) {
	if s.tab != ProjectsTab || !clicked.foldable {
		return
	}
	if clicked.expanded {
		s.folded[clicked.id] = true
		delete(s.expanded, clicked.id)
	} else {
		delete(s.folded, clicked.id)
		s.expanded[clicked.id] = true
	}
	s.cursor = clicked.id
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

func (s *inputState) key(msg tea.KeyMsg, frame inputFrame) inputEffect {
	if msg.Type == tea.KeyCtrlC {
		s.cancelled = true
		if s.form != nil {
			if s.form.cancel != nil {
				s.form.cancel()
				s.form.cancel = nil
			}
			s.form.nextGeneration()
			s.actionGeneration = s.form.generation
		}
		s.form = nil
		s.deleteForm = nil
		s.confirmKill = false
		s.renameMode = false
		s.killTargets = nil
		return inputEffect{quit: true}
	}
	if s.form != nil {
		effect := s.form.key(msg)
		if effect.Canceled {
			s.actionGeneration = s.form.generation
			s.form = nil
			s.deleteForm = nil
			s.actionInFlight = false
			return inputEffect{rebuild: true}
		}
		if effect.Submitted {
			s.actionInFlight = true
			s.actionGeneration = s.form.generation
		}
		return inputEffect{rebuild: effect.Changed || effect.Submitted, cmd: effect.Cmd}
	}
	if s.actionInFlight {
		return inputEffect{}
	}
	if s.confirmKill {
		switch strings.ToLower(msg.String()) {
		case "y":
			s.confirmKill = false
			refs := append([]picker.ContainerRef(nil), s.killTargets...)
			s.killTargets = nil
			s.actionInFlight = true
			return inputEffect{cmd: s.runKill(refs)}
		case "n", "esc":
			s.confirmKill = false
			s.killTargets = nil
		}
		return inputEffect{}
	}
	if s.renameMode {
		switch msg.Type {
		case tea.KeyEsc:
			s.renameMode = false
			s.renameInput = nil
		case tea.KeyEnter:
			if strings.TrimSpace(string(s.renameInput)) != "" {
				s.renameMode = false
				name := string(s.renameInput)
				s.renameInput = nil
				s.actionInFlight = true
				return inputEffect{cmd: s.runRename(s.renameTarget, name)}
			}
		case tea.KeyBackspace, tea.KeyDelete:
			if len(s.renameInput) > 0 {
				s.renameInput = s.renameInput[:len(s.renameInput)-1]
			}
		case tea.KeyRunes:
			s.renameInput = append(s.renameInput, msg.Runes...)
		case tea.KeySpace:
			s.renameInput = append(s.renameInput, ' ')
		}
		return inputEffect{}
	}
	switch msg.Type {
	case tea.KeyEsc:
		if len(s.query) > 0 {
			s.setQuery("")
			return inputEffect{rebuild: true}
		}
		s.cancelled = true
		return inputEffect{quit: true}
	case tea.KeyTab:
		s.switchTab(1)
		return inputEffect{rebuild: true}
	case tea.KeyF1:
		s.switchTo(AgentsTab)
		return inputEffect{rebuild: true}
	case tea.KeyF2:
		s.switchTo(ContainersTab)
		return inputEffect{rebuild: true}
	case tea.KeyF3:
		s.switchTo(ProjectsTab)
		return inputEffect{rebuild: true}
	case tea.KeyCtrlT:
		s.toggleMark(frame.rows)
		return inputEffect{rebuild: true}
	case tea.KeyCtrlO:
		if s.tab == ProjectsTab {
			if current, ok := s.currentItem(frame.rows); ok && current.place.Kind == picker.ProjectKind {
				s.form = newCloneForm(s.deps, s.ctx)
				return inputEffect{rebuild: true, cmd: s.form.initialFocusCmd()}
			}
		}
		return inputEffect{}
	case tea.KeyCtrlD:
		if s.tab == ProjectsTab {
			s.form = newDirectoryForm(s.deps, s.ctx)
			return inputEffect{rebuild: true, cmd: s.form.initialFocusCmd()}
		}
		return inputEffect{}
	case tea.KeyCtrlE:
		if s.tab == ProjectsTab {
			if current, ok := s.currentItem(frame.rows); ok && current.place.Kind == picker.ProjectKind && current.place.Git {
				s.form = newExtractForm(s.deps, current.place, s.ctx)
				return inputEffect{rebuild: true, cmd: s.form.initialFocusCmd()}
			}
		}
		return inputEffect{}
	case tea.KeyCtrlY:
		if s.tab == ProjectsTab {
			if current, ok := s.currentItem(frame.rows); ok && current.place.Kind == picker.ProjectKind && current.place.Path != "" {
				delete := newDeleteForm(s.deps, s.ctx, current.place.Path)
				s.deleteForm = delete
				s.form = delete.stagedForm
				if cmd := delete.startPreflight(s.deps); cmd != nil {
					s.actionInFlight = true
					s.actionGeneration = s.form.generation
					return inputEffect{rebuild: true, cmd: cmd}
				}
				return inputEffect{rebuild: true}
			}
		}
		return inputEffect{}
	case tea.KeyCtrlX:
		refs := append([]picker.ContainerRef(nil), frame.markedTargets()...)
		if len(refs) == 0 {
			if current, ok := s.currentItem(frame.rows); ok && current.place.Kind == picker.ContainerKind && current.place.ContainerID != "" {
				refs = []picker.ContainerRef{{Machine: current.place.Machine, ID: current.place.ContainerID}}
			}
		}
		if len(refs) > 0 {
			s.killTargets = append([]picker.ContainerRef(nil), refs...)
			s.confirmKill = true
		}
		return inputEffect{}
	case tea.KeyCtrlR:
		if current, ok := s.currentItem(frame.rows); ok && current.place.Kind == picker.ContainerKind && current.place.ContainerID != "" {
			s.renameMode = true
			s.renameInput = nil
			s.renameTarget = picker.ContainerRef{Machine: current.place.Machine, ID: current.place.ContainerID}
		}
		return inputEffect{}
	case tea.KeyCtrlW:
		s.setQuery(string(deleteBackToWhitespace(s.query)))
		return inputEffect{rebuild: true}
	case tea.KeyCtrlU:
		s.setQuery("")
		return inputEffect{rebuild: true}
	case tea.KeyBackspace:
		if msg.Alt {
			s.setQuery(string(deleteBackToBoundary(s.query)))
		} else if len(s.query) > 0 {
			s.setQuery(string(s.query[:len(s.query)-1]))
		}
		return inputEffect{rebuild: true}
	case tea.KeyDelete:
		if len(s.query) > 0 {
			s.setQuery(string(s.query[:len(s.query)-1]))
		}
		return inputEffect{rebuild: true}
	case tea.KeyUp:
		s.moveCursor(-1, frame.rows)
		return inputEffect{}
	case tea.KeyDown:
		s.moveCursor(1, frame.rows)
		return inputEffect{}
	case tea.KeyCtrlN:
		s.moveCursor(1, frame.rows)
		return inputEffect{}
	case tea.KeyCtrlP:
		s.moveCursor(-1, frame.rows)
		return inputEffect{}
	case tea.KeyPgUp:
		s.moveCursor(-s.pageSize(frame.height), frame.rows)
		return inputEffect{}
	case tea.KeyPgDown:
		s.moveCursor(s.pageSize(frame.height), frame.rows)
		return inputEffect{}
	case tea.KeyLeft:
		if s.foldLeft(frame.rows) {
			return inputEffect{rebuild: true}
		}
		return inputEffect{}
	case tea.KeyRight:
		if s.foldRight(frame.rows) {
			return inputEffect{rebuild: true}
		}
		return inputEffect{}
	case tea.KeyEnter:
		return s.accept(frame.rows)
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			query := append(append([]rune(nil), s.query...), msg.Runes...)
			s.setQuery(string(query))
		}
		return inputEffect{rebuild: true}
	case tea.KeySpace:
		s.setQuery(string(append(append([]rune(nil), s.query...), ' ')))
		return inputEffect{rebuild: true}
	}
	if msg.String() == "shift+tab" {
		s.switchTab(-1)
		return inputEffect{rebuild: true}
	}
	return inputEffect{}
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

func (s *inputState) switchTab(delta int) {
	tabs := []Tab{AgentsTab, ContainersTab, ProjectsTab}
	index := 0
	for i, tab := range tabs {
		if tab == s.tab {
			index = i
		}
	}
	s.switchTo(tabs[(index+delta+len(tabs))%len(tabs)])
}

func (s *inputState) switchTo(tab Tab) {
	if validTab(tab) {
		s.tab = tab
		s.forceFirst = false
	}
}

func (s *inputState) moveCursor(delta int, rows []row) {
	if len(rows) == 0 {
		return
	}
	indices := []int{}
	current := 0
	for i, value := range rows {
		if value.cursorable {
			indices = append(indices, i)
			if value.id == s.cursor {
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
	s.cursor = rows[indices[current]].id
	s.forceFirst = false
	s.cursorMoved = true
}

func (s *inputState) pageSize(height int) int {
	if height <= 4 {
		return 1
	}
	return height - 4
}

func (s *inputState) toggleMark(rows []row) {
	current, ok := s.currentItem(rows)
	if !ok || current.place.Kind != picker.ContainerKind || current.place.ContainerID == "" {
		return
	}
	ref := picker.ContainerRef{Machine: current.place.Machine, ID: current.place.ContainerID}
	if s.marked[ref] {
		delete(s.marked, ref)
	} else {
		s.marked[ref] = true
	}
}

func (s *inputState) foldRight(rows []row) bool {
	current, ok := s.currentRow(rows)
	if !ok || s.tab != ProjectsTab || !current.foldable {
		return false
	}
	delete(s.folded, current.id)
	s.expanded[current.id] = true
	s.cursor = current.id
	return true
}

func (s *inputState) revealProjectAncestors(projects []picker.Place, path string) {
	target := projectPathIdentity(path)
	for target != "" {
		var found *picker.Place
		for i := range projects {
			if projectPathIdentity(projects[i].Path) == target {
				found = &projects[i]
				break
			}
		}
		if found == nil {
			return
		}
		for _, key := range []string{projectIdentity(*found), "project:" + found.ParentPath, "group:" + found.Group} {
			s.expanded[key] = true
			delete(s.folded, key)
		}
		if found.ParentPath == "" {
			return
		}
		target = projectPathIdentity(found.ParentPath)
	}
}

func (s *inputState) foldLeft(rows []row) bool {
	if s.tab != ProjectsTab || string(s.query) != "" {
		return false
	}
	current, ok := s.currentRow(rows)
	if !ok {
		return false
	}
	if current.foldable && current.expanded {
		delete(s.expanded, current.id)
		s.folded[current.id] = true
		s.cursor = current.id
		return true
	}
	parent := foldParent(current)
	if parent == "" {
		return false
	}
	delete(s.expanded, parent)
	s.folded[parent] = true
	s.cursor = parent
	return true
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

func (s *inputState) accept(rows []row) inputEffect {
	current, ok := s.currentRow(rows)
	if !ok {
		return inputEffect{}
	}
	if current.kind == newContainerRow {
		name := strings.TrimSpace(string(s.query))
		target := RemoteTarget{Machine: current.machine, Backend: current.backend}
		if name == "" {
			s.form = newRemoteNewForm(s.ctx, target, "")
			return inputEffect{rebuild: true, cmd: s.form.initialFocusCmd()}
		}
		if message := remoteContainerNameError(name); message != "" {
			// A multiline paste is a search query, not a valid name. Prefill a
			// form so the shared policy surfaces the error inline instead of
			// quitting with a name the backend rejects later.
			form := newRemoteNewForm(s.ctx, target, name)
			form.applyValidation()
			s.form = form
			return inputEffect{rebuild: true}
		}
		s.selectedRemoteTarget = &target
		s.selectedRemoteName = name
		s.cancelled = false
		return inputEffect{quit: true}
	}
	if current.kind != itemRow {
		return inputEffect{}
	}
	if current.itemKind == "agent" {
		value := current.agent
		s.selectedAgent = &value
		s.selectedAgentRef = AgentRef{Agent: value, Machine: current.agentMachine, Backend: current.agentBackend}
	} else {
		value := current.place
		s.selectedPlace = &value
	}
	s.cancelled = false
	return inputEffect{quit: true}
}

// runKill builds the kill command from the callback and context captured now.
// Command construction is the last point where the live transition state is
// safe to read: the returned closure runs on Bubble Tea's command goroutine
// while later events keep rewriting inputState.
func (s *inputState) runKill(refs []picker.ContainerRef) tea.Cmd {
	kill, ctx := s.deps.KillContainers, s.ctx
	return func() tea.Msg {
		if kill == nil {
			return actionMsg{kind: "kill", err: errors.New("container killing is not supported")}
		}
		return actionMsg{kind: "kill", err: kill(ctx, refs)}
	}
}

func (s *inputState) runRename(ref picker.ContainerRef, name string) tea.Cmd {
	rename, ctx := s.deps.RenameContainer, s.ctx
	return func() tea.Msg {
		if rename == nil {
			return actionMsg{kind: "rename", err: errors.New("container renaming is not supported")}
		}
		return actionMsg{kind: "rename", err: rename(ctx, ref, name)}
	}
}

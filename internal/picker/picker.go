// Package picker implements the place picker model and its terminal view.
package picker

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/projects"
)

// PlaceKind identifies the kind of place represented by a picker item. New
// place kinds can be added without changing the row model.
type PlaceKind string

const (
	// ProjectKind is a configured project candidate.
	ProjectKind PlaceKind = "project"
	// ContainerKind is an open local (or future remote) container.
	ContainerKind PlaceKind = "container"
	// NewContainerKind is the per-machine create affordance.
	NewContainerKind PlaceKind = "new-container"
)

// Decoration is an optional piece of metadata supplied by a future place
// source. The project picker currently has no decorations.
type Decoration struct {
	Text  string
	Style DecorationStyle
}

// DecorationStyle describes how a decoration should be drawn.
type DecorationStyle string

const (
	DecorationMuted  DecorationStyle = "muted"
	DecorationNormal DecorationStyle = "normal"
	DecorationAccent DecorationStyle = "accent"
)

// OpenAction is an optional action owned by a place source. Project places
// leave it nil so the active backend can open them; future place kinds can
// carry their own action without changing the picker model.
type OpenAction func(context.Context, Place) error

type ContainerRef struct {
	Machine string `json:"machine"`
	ID      string `json:"id"`
}

type PreviewBatch struct {
	Previews map[ContainerRef]string
}

// Place is the additive item model used by the picker. SearchText and
// DisplayRemainder keep ranking and rendering independent of filesystem paths;
// Path and Group remain populated for project places.
type Place struct {
	Kind             PlaceKind    `json:"kind"`
	Path             string       `json:"path,omitempty"`
	Group            string       `json:"group,omitempty"`
	Machine          string       `json:"machine,omitempty"`
	SearchText       string       `json:"search_text,omitempty"`
	DisplayRemainder string       `json:"display_remainder,omitempty"`
	ContainerID      string       `json:"container_id,omitempty"`
	ContainerName    string       `json:"container_name,omitempty"`
	Preview          string       `json:"preview,omitempty"`
	Payload          any          `json:"-"`
	OpenAction       OpenAction   `json:"-"`
	Git              bool         `json:"git,omitempty"`
	LinkedWorktree   bool         `json:"linked_worktree,omitempty"`
	Decorations      []Decoration `json:"decorations,omitempty"`
}

// ProjectPlaces converts project discovery results into picker places while
// preserving discovery order (which is the tie-breaker for equal scores).
func ProjectPlaces(candidates []projects.Candidate) []Place {
	places := make([]Place, 0, len(candidates))
	for _, candidate := range candidates {
		places = append(places, Place{
			Kind:             ProjectKind,
			Path:             candidate.Path,
			Group:            candidate.Group,
			SearchText:       candidate.Path,
			DisplayRemainder: projectRemainder(candidate.Path, candidate.Group),
			Payload:          candidate,
			Git:              candidate.Git,
			LinkedWorktree:   candidate.LinkedWorktree,
		})
	}
	return places
}

// LocalMachine returns the hostname used to group local containers.
func LocalMachine() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "local"
	}
	return name
}

// MachinePlace adds the create affordance for a machine, including one with
// no currently open containers.
func MachinePlace(machine string) Place {
	if strings.TrimSpace(machine) == "" {
		machine = LocalMachine()
	}
	return Place{Kind: NewContainerKind, Group: machine, Machine: machine, SearchText: "+ new container", DisplayRemainder: "+ new container…"}
}

// ContainerPlaces converts backend containers to machine-grouped picker
// places. The machine field is deliberately separate from the group so remote
// sources can add more machines without changing the row model.
func ContainerPlaces(containers []capabilities.Container) []Place {
	places := make([]Place, 0, len(containers))
	for _, container := range containers {
		machine := container.Machine
		if machine == "" {
			machine = LocalMachine()
		}
		places = append(places, Place{
			Kind:             ContainerKind,
			Group:            machine,
			Machine:          machine,
			SearchText:       container.Name,
			DisplayRemainder: container.Name,
			ContainerID:      container.ID,
			ContainerName:    container.Name,
			Preview:          container.Preview,
			Payload:          container,
		})
	}
	return places
}

// MachinePlaces returns one create affordance per machine represented by the
// containers, always including the local machine for creating a new local
// container.
func MachinePlaces(containers []capabilities.Container) []Place {
	local := LocalMachine()
	machines := map[string]bool{local: true}
	places := []Place{MachinePlace(local)}
	for _, container := range containers {
		machine := container.Machine
		if machine == "" {
			machine = local
		}
		if !machines[machine] {
			machines[machine] = true
			places = append(places, MachinePlace(machine))
		}
	}
	return places
}

// Match is the result of matching a query against text.
type Match struct {
	Score     int
	Positions []int // rune positions in the matched text
}

// Rank performs nexo's own fuzzy ranking. A query matches when its runes are
// a case-insensitive subsequence of text. Contiguous and prefix matches rank
// ahead of sparse matches; the original order remains the final tie-breaker.
func Rank(query, text string) (Match, bool) {
	queryRunes := []rune(strings.ToLower(query))
	textRunes := []rune(strings.ToLower(text))
	if len(queryRunes) == 0 {
		return Match{Score: 0}, true
	}
	positions := make([]int, 0, len(queryRunes))
	cursor := 0
	for _, wanted := range queryRunes {
		found := -1
		for cursor < len(textRunes) {
			if textRunes[cursor] == wanted {
				found = cursor
				cursor++
				break
			}
			cursor++
		}
		if found == -1 {
			return Match{}, false
		}
		positions = append(positions, found)
	}

	score := len(queryRunes) * 10
	if positions[0] == 0 {
		score += 30
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] == positions[i-1]+1 {
			score += 15
		} else {
			score -= positions[i] - positions[i-1] - 1
		}
	}
	score -= positions[0]
	return Match{Score: score, Positions: positions}, true
}

// Theme controls picker colors. Color is false by default so model views are
// deterministic in tests and in terminals that do not advertise color.
type Theme struct {
	Foreground          string
	Background          string
	SelectionBackground string
	Muted               string
	Highlight           string
	Color               bool
}

// DefaultTheme is the terminal-safe fallback palette.
var DefaultTheme = Theme{
	Foreground:          "default",
	Background:          "default",
	SelectionBackground: "8",
	Muted:               "8",
	Highlight:           "15",
}

// RowKind identifies a rendered row.
type RowKind string

const (
	HeaderRow       RowKind = "header"
	PlaceRow        RowKind = "place"
	NewContainerRow RowKind = "new-container"
)

// Row is an observable picker row. It is useful for model tests and for
// adapters that want to decorate rows without changing ranking.
type Row struct {
	Kind       RowKind
	Group      string
	groupKey   groupKey
	Place      Place
	Remainder  string
	Matches    []int
	Score      int
	Expanded   bool
	ChildCount int
	Marked     bool
}

// GroupKey identifies a displayed group without conflating project and
// machine namespaces that happen to share the same label.
type GroupKey struct {
	Kind PlaceKind
	Name string
}

type groupKey = GroupKey

type group struct {
	key    groupKey
	name   string
	places []Place
	orders []int
}

// Model is the Bubble Tea place picker model.
type Model struct {
	places []Place
	theme  Theme
	query  []rune
	rows   []Row
	cursor int
	width  int
	height int

	// expanded is used for empty-query groups. collapsed is an explicit user
	// collapse in a non-empty query, where matching groups otherwise unfold.
	expanded    map[groupKey]bool
	collapsed   map[groupKey]bool
	selected    *Place
	cancelled   bool
	marked      map[ContainerRef]bool
	renameMode  bool
	renameInput []rune
	renameReady bool
	confirmKill bool
	killReady   bool
	previewCmd  func() tea.Msg
}

// NewModel constructs a picker model with all groups folded.
func NewModel(places []Place, theme Theme) Model {
	if theme.Foreground == "" {
		theme = DefaultTheme
	}
	m := Model{
		places:    append([]Place(nil), places...),
		theme:     theme,
		expanded:  make(map[groupKey]bool),
		collapsed: make(map[groupKey]bool),
		marked:    make(map[ContainerRef]bool),
	}
	m.rebuild()
	return m
}

// WithPreviewCommand starts asynchronous screen preview loading when the
// Bubble Tea program starts, rather than blocking picker construction.
func (m Model) WithPreviewCommand(command func() tea.Msg) Model {
	m.previewCmd = command
	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	if m.previewCmd == nil {
		return nil
	}
	return m.previewCmd
}

// Places returns a copy of the model's source places.
func (m Model) Places() []Place { return append([]Place(nil), m.places...) }

// Query returns the current fuzzy query.
func (m Model) Query() string { return string(m.query) }

// Cursor returns the index of the keyboard-selected row.
func (m Model) Cursor() int { return m.cursor }

// Rows returns a copy of the current visible rows.
func (m Model) Rows() []Row { return append([]Row(nil), m.rows...) }

// Cancelled reports whether the picker exited without accepting a place.
func (m Model) Cancelled() bool { return m.cancelled }

// Selected reports the place accepted by Enter, if any.
func (m Model) Selected() (Place, bool) {
	if m.selected == nil {
		return Place{}, false
	}
	return *m.selected, true
}

// RenameTargetRequest reports the confirmed rename keyed by machine and ID.
func (m Model) RenameTargetRequest() (ContainerRef, string, bool) {
	if !m.renameReady || len(m.rows) == 0 || m.cursor >= len(m.rows) {
		return ContainerRef{}, "", false
	}
	row := m.rows[m.cursor]
	if row.Kind != PlaceRow || row.Place.Kind != ContainerKind {
		return ContainerRef{}, "", false
	}
	return ContainerRef{Machine: row.Place.Machine, ID: row.Place.ContainerID}, string(m.renameInput), true
}

// RenameRequest retains the ID-only capability seam for local backends.
func (m Model) RenameRequest() (string, string, bool) {
	ref, name, ok := m.RenameTargetRequest()
	return ref.ID, name, ok
}

// KillTargetsRequest reports the complete confirmed selection keyed by
// machine and container ID, so future remote sources cannot collide.
func (m Model) KillTargetsRequest() ([]ContainerRef, bool) {
	if !m.killReady {
		return nil, false
	}
	targets := m.markedTargets()
	if len(targets) == 0 && len(m.rows) > 0 && m.cursor < len(m.rows) {
		row := m.rows[m.cursor]
		if row.Kind == PlaceRow && row.Place.Kind == ContainerKind && row.Place.ContainerID != "" {
			targets = []ContainerRef{{Machine: row.Place.Machine, ID: row.Place.ContainerID}}
		}
	}
	return targets, len(targets) > 0
}

// KillRequest retains the ID-only capability seam for local backends.
func (m Model) KillRequest() ([]string, bool) {
	targets, ok := m.KillTargetsRequest()
	if !ok {
		return nil, false
	}
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ID)
	}
	return ids, true
}

// Marked returns the selected container IDs in display order.
func (m Model) Marked() []string {
	targets := m.markedTargets()
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ID)
	}
	return ids
}

func (m Model) markedTargets() []ContainerRef {
	var targets []ContainerRef
	seen := make(map[ContainerRef]bool)
	for _, place := range m.places {
		ref := ContainerRef{Machine: place.Machine, ID: place.ContainerID}
		if place.Kind != ContainerKind || place.ContainerID == "" || !m.marked[ref] || seen[ref] {
			continue
		}
		seen[ref] = true
		targets = append(targets, ref)
	}
	return targets
}

// SetQuery is primarily useful to model tests and embedding callers.
func (m *Model) SetPreview(ref ContainerRef, preview string) {
	for index := range m.places {
		place := &m.places[index]
		if place.Kind == ContainerKind && place.ContainerID == ref.ID && place.Machine == ref.Machine {
			place.Preview = preview
		}
	}
	m.rebuild()
}

func (m *Model) SetQuery(query string) {
	m.query = []rune(query)
	m.collapsed = make(map[groupKey]bool)
	m.expanded = make(map[groupKey]bool)
	m.rebuild()
}

// SetSize updates the terminal dimensions used by View.
func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
}

// PlaceGroupKey is the grouping identity shared by the picker and scripted
// place listings. Container affordances use the machine namespace; projects
// use the configured parent namespace.
func PlaceGroupKey(place Place) GroupKey {
	if place.Kind == ContainerKind || place.Kind == NewContainerKind {
		machine := place.Machine
		if machine == "" {
			machine = place.Group
		}
		return GroupKey{Kind: ContainerKind, Name: machine}
	}
	return GroupKey{Kind: ProjectKind, Name: place.Group}
}

func placeGroupKey(place Place) groupKey { return PlaceGroupKey(place) }

func (m *Model) rebuild() {
	groups := make([]group, 0)
	byKey := make(map[groupKey]int)
	for order, place := range m.places {
		key := placeGroupKey(place)
		index, ok := byKey[key]
		if !ok {
			index = len(groups)
			byKey[key] = index
			groups = append(groups, group{key: key, name: key.Name})
		}
		groups[index].places = append(groups[index].places, place)
		groups[index].orders = append(groups[index].orders, order)
	}
	m.rows = m.rows[:0]
	query := string(m.query)
	if query == "" {
		for _, g := range groups {
			m.rows = append(m.rows, Row{Kind: HeaderRow, Group: g.name, groupKey: g.key, Expanded: m.expanded[g.key], ChildCount: m.groupCount(groups, g.key)})
			if m.expanded[g.key] {
				for index, place := range g.places {
					if place.Kind != NewContainerKind {
						m.rows = append(m.rows, m.placeRow(place, 0, nil, index == len(g.places)-1))
					}
				}
				if groupHasContainers(g) {
					m.rows = append(m.rows, Row{Kind: NewContainerRow, Group: g.name, groupKey: g.key, Place: MachinePlace(g.name)})
				}
			}
		}
		m.clampCursor()
		return
	}

	type candidate struct {
		place Place
		group groupKey
		full  Match
		rem   Match
		remOK bool
		order int
	}
	var ranked []candidate
	visible := make(map[groupKey]bool)
	for _, g := range groups {
		for placeIndex, place := range g.places {
			if place.Kind == NewContainerKind {
				continue
			}
			full, ok := Rank(query, searchText(place))
			if !ok {
				continue
			}
			rem := visibleRemainder(place)
			remMatch, remOK := Rank(query, rem)
			if remOK {
				visible[g.key] = true
			}
			ranked = append(ranked, candidate{place: place, group: g.key, full: full, rem: remMatch, remOK: remOK, order: g.orders[placeIndex]})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].full.Score != ranked[j].full.Score {
			return ranked[i].full.Score > ranked[j].full.Score
		}
		return ranked[i].order < ranked[j].order
	})

	seenFolded := make(map[groupKey]bool)
	lastRenderedGroup := groupKey{}
	for _, hit := range ranked {
		if !visible[hit.group] && !m.expanded[hit.group] {
			if !seenFolded[hit.group] {
				m.rows = append(m.rows, Row{Kind: HeaderRow, Group: hit.group.Name, groupKey: hit.group, ChildCount: m.groupCount(groups, hit.group)})
				seenFolded[hit.group] = true
				lastRenderedGroup = hit.group
			}
			continue
		}
		if m.collapsed[hit.group] {
			if lastRenderedGroup != hit.group {
				m.rows = append(m.rows, Row{Kind: HeaderRow, Group: hit.group.Name, groupKey: hit.group, ChildCount: m.groupCount(groups, hit.group)})
				lastRenderedGroup = hit.group
			}
			continue
		}
		if lastRenderedGroup != hit.group {
			m.rows = append(m.rows, Row{Kind: HeaderRow, Group: hit.group.Name, groupKey: hit.group, Expanded: visible[hit.group] || m.expanded[hit.group], ChildCount: m.groupCount(groups, hit.group)})
			lastRenderedGroup = hit.group
		}
		positions := hit.rem.Positions
		if !hit.remOK {
			positions = nil
		}
		m.rows = append(m.rows, m.placeRow(hit.place, hit.full.Score, positions, false))
	}
	for index := len(m.rows) - 1; index >= 0; index-- {
		if m.rows[index].Kind != HeaderRow || !m.rows[index].Expanded || !m.groupHasContainerPlace(m.rows[index].groupKey) {
			continue
		}
		groupKey := m.rows[index].groupKey
		insertAt := index + 1
		for insertAt < len(m.rows) && m.rows[insertAt].groupKey == groupKey {
			insertAt++
		}
		m.rows = append(m.rows, Row{})
		copy(m.rows[insertAt+1:], m.rows[insertAt:])
		m.rows[insertAt] = Row{Kind: NewContainerRow, Group: groupKey.Name, groupKey: groupKey, Place: MachinePlace(groupKey.Name)}
	}
	m.clampCursor()
}

func groupHasContainers(g group) bool {
	for _, place := range g.places {
		if place.Kind == ContainerKind || place.Kind == NewContainerKind {
			return true
		}
	}
	return false
}

func (m Model) groupHasContainerPlace(key groupKey) bool {
	for _, place := range m.places {
		if placeGroupKey(place) == key && (place.Kind == ContainerKind || place.Kind == NewContainerKind) {
			return true
		}
	}
	return false
}

func (m Model) groupCount(groups []group, key groupKey) int {
	for _, g := range groups {
		if g.key == key {
			count := 0
			for _, place := range g.places {
				if place.Kind != NewContainerKind {
					count++
				}
			}
			return count
		}
	}
	return 0
}

func (m Model) placeRow(place Place, score int, positions []int, _ bool) Row {
	ref := ContainerRef{Machine: place.Machine, ID: place.ContainerID}
	return Row{Kind: PlaceRow, Group: place.Group, groupKey: placeGroupKey(place), Place: place, Remainder: visibleRemainder(place), Matches: append([]int(nil), positions...), Score: score, Marked: m.marked[ref]}
}

func searchText(place Place) string {
	if place.SearchText != "" {
		return place.SearchText
	}
	return place.Path
}

func projectRemainder(path, group string) string {
	if group == "" {
		return filepath.ToSlash(path)
	}
	remainder, err := filepath.Rel(group, path)
	if err == nil && remainder != "." && remainder != ".." && !strings.HasPrefix(remainder, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(remainder)
	}
	return filepath.ToSlash(path)
}

func visibleRemainder(place Place) string {
	if place.DisplayRemainder != "" {
		return filepath.ToSlash(place.DisplayRemainder)
	}
	return projectRemainder(place.Path, place.Group)
}

func (m *Model) clampCursor() {
	if len(m.rows) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// KeyMap documents the picker key bindings using Bubbles' key package.
type KeyMap struct {
	Up, Down, Accept, Expand, Collapse, Clear key.Binding
	MarkNext, MarkPrevious                    key.Binding
	Rename, Kill                              key.Binding
}

var defaultKeyMap = KeyMap{
	Up:           key.NewBinding(key.WithKeys("up", "ctrl+k")),
	Down:         key.NewBinding(key.WithKeys("down", "ctrl+j")),
	Accept:       key.NewBinding(key.WithKeys("enter")),
	Expand:       key.NewBinding(key.WithKeys("right", tea.KeySpace.String())),
	Collapse:     key.NewBinding(key.WithKeys("left")),
	Clear:        key.NewBinding(key.WithKeys("esc")),
	MarkNext:     key.NewBinding(key.WithKeys("tab")),
	MarkPrevious: key.NewBinding(key.WithKeys("shift+tab")),
	Rename:       key.NewBinding(key.WithKeys("r")),
	Kill:         key.NewBinding(key.WithKeys("x", "delete")),
}

// Update handles search input, folding, navigation, container actions and
// accepting a place.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil
	case PreviewBatch:
		for ref, preview := range msg.Previews {
			m.SetPreview(ref, preview)
		}
		return m, nil
	case tea.KeyMsg:
		if m.renameMode {
			switch {
			case msg.Type == tea.KeyEsc:
				m.renameMode = false
				return m, nil
			case key.Matches(msg, defaultKeyMap.Accept):
				if strings.TrimSpace(string(m.renameInput)) != "" {
					m.renameReady = true
					m.renameMode = false
					return m, tea.Quit
				}
				return m, nil
			case msg.Type == tea.KeyBackspace, msg.Type == tea.KeyDelete:
				if len(m.renameInput) > 0 {
					m.renameInput = m.renameInput[:len(m.renameInput)-1]
				}
				return m, nil
			case msg.Type == tea.KeyRunes:
				m.renameInput = append(m.renameInput, msg.Runes...)
				return m, nil
			}
		}
		if m.confirmKill {
			switch {
			case msg.Type == tea.KeyEsc, (msg.Type == tea.KeyRunes && strings.EqualFold(string(msg.Runes), "n")):
				m.confirmKill = false
				return m, nil
			case msg.Type == tea.KeyRunes && strings.EqualFold(string(msg.Runes), "y"):
				m.confirmKill = false
				m.killReady = true
				return m, tea.Quit
			default:
				return m, nil
			}
		}
		switch {
		case msg.Type == tea.KeyCtrlC, msg.Type == tea.KeyEsc:
			m.cancelled = true
			return m, tea.Quit
		case msg.Type == tea.KeyRunes && len(m.query) == 0 && string(msg.Runes) == "q":
			m.cancelled = true
			return m, tea.Quit
		case msg.Type == tea.KeyRunes && len(m.query) == 0 && string(msg.Runes) == "r":
			if m.currentContainer() {
				m.renameMode = true
				m.renameInput = nil
			}
			return m, nil
		case msg.Type == tea.KeyRunes && len(m.query) == 0 && (string(msg.Runes) == "x" || string(msg.Runes) == "d"):
			if len(m.markedTargets()) > 0 || m.currentContainer() {
				m.confirmKill = true
			}
			return m, nil
		case key.Matches(msg, defaultKeyMap.MarkNext):
			m.markCurrent()
			m.moveCursor(1)
			return m, nil
		case key.Matches(msg, defaultKeyMap.MarkPrevious):
			m.markCurrent()
			m.moveCursor(-1)
			return m, nil
		case key.Matches(msg, defaultKeyMap.Clear):
			return m, nil
		case key.Matches(msg, defaultKeyMap.Up):
			m.moveCursor(-1)
			return m, nil
		case key.Matches(msg, defaultKeyMap.Down):
			m.moveCursor(1)
			return m, nil
		case key.Matches(msg, defaultKeyMap.Collapse):
			return m.collapseCurrent()
		case key.Matches(msg, defaultKeyMap.Expand):
			return m.expandCurrent()
		case key.Matches(msg, defaultKeyMap.Accept):
			if len(m.rows) == 0 {
				return m, nil
			}
			row := m.rows[m.cursor]
			if row.Kind == HeaderRow {
				return m.expandCurrent()
			}
			if row.Kind == NewContainerRow {
				return m, nil
			}
			selected := row.Place
			m.selected = &selected
			return m, tea.Quit
		case msg.Type == tea.KeyBackspace, msg.Type == tea.KeyDelete:
			if len(m.query) > 0 {
				m.query = m.query[:len(m.query)-1]
				m.collapsed = make(map[groupKey]bool)
				m.expanded = make(map[groupKey]bool)
				m.rebuild()
			}
			return m, nil
		case msg.Type == tea.KeyRunes:
			if len(msg.Runes) > 0 {
				m.query = append(m.query, msg.Runes...)
				m.collapsed = make(map[groupKey]bool)
				m.expanded = make(map[groupKey]bool)
				m.rebuild()
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) currentContainer() bool {
	return len(m.rows) > 0 && m.cursor < len(m.rows) && m.rows[m.cursor].Kind == PlaceRow && m.rows[m.cursor].Place.Kind == ContainerKind && m.rows[m.cursor].Place.ContainerID != ""
}

func (m *Model) markCurrent() {
	if !m.currentContainer() {
		return
	}
	place := m.rows[m.cursor].Place
	ref := ContainerRef{Machine: place.Machine, ID: place.ContainerID}
	m.marked[ref] = !m.marked[ref]
	m.rebuild()
}

func (m *Model) moveCursor(direction int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor += direction
	m.clampCursor()
}

func (m Model) expandCurrent() (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 || m.rows[m.cursor].Kind != HeaderRow {
		return m, nil
	}
	name := m.rows[m.cursor].groupKey
	if string(m.query) == "" {
		m.expanded[name] = true
	} else {
		m.expanded[name] = true
		delete(m.collapsed, name)
	}
	m.rebuild()
	return m, nil
}

func (m Model) collapseCurrent() (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 {
		return m, nil
	}
	row := m.rows[m.cursor]
	groupName := row.groupKey
	if row.Kind == HeaderRow {
		if string(m.query) == "" {
			m.expanded[groupName] = false
		}
		m.rebuild()
		return m, nil
	}
	if string(m.query) == "" {
		m.expanded[groupName] = false
	} else {
		delete(m.expanded, groupName)
		m.collapsed[groupName] = true
	}
	m.rebuild()
	for index := m.cursor; index >= 0; index-- {
		candidate := m.rows[index]
		if candidate.Kind == HeaderRow && candidate.groupKey == groupName {
			m.cursor = index
			break
		}
	}
	return m, nil
}

// View renders the picker, with group headers and tree spines excluded from
// ranking and highlighting.
func (m Model) View() string {
	var lines []string
	queryStyle := m.style(m.theme.Foreground)
	if m.renameMode {
		lines = append(lines, queryStyle.Render("rename: "+string(m.renameInput)))
	} else {
		lines = append(lines, queryStyle.Render("> "+string(m.query)))
	}
	for index, row := range m.rows {
		selected := index == m.cursor
		if row.Kind == HeaderRow {
			arrow := "▸"
			if row.Expanded {
				arrow = "▾"
			}
			label := filepath.Base(filepath.ToSlash(row.Group))
			if label == "." || label == "/" || label == "" {
				label = row.Group
			}
			if row.Expanded {
				lines = append(lines, m.rowStyle(selected, true).Render(arrow+" "+label))
			} else {
				lines = append(lines, m.rowStyle(selected, true).Render(arrow+" "+label+" ("+itoa(row.ChildCount)+")"))
			}
			continue
		}
		if row.Kind == NewContainerRow {
			spineStyle := m.style(m.theme.Muted)
			lines = append(lines, m.rowStyle(selected, false).Render("  ")+spineStyle.Render("└ ")+m.rowStyle(selected, false).Render(row.Place.DisplayRemainder))
			continue
		}
		spine := "├"
		if index+1 >= len(m.rows) || m.rows[index+1].Kind == HeaderRow || m.rows[index+1].Group != row.Group {
			spine = "└"
		}
		spineStyle := m.style(m.theme.Muted)
		body := m.highlight(row.Remainder, row.Matches, selected)
		body = strings.ReplaceAll(body, "/", spineStyle.Render(" │ "))
		if row.Place.Kind == ContainerKind {
			mark := "☐"
			if row.Marked {
				mark = "☑"
			}
			body = mark + " " + body
			if preview := previewLine(row.Place.Preview); preview != "" {
				body += "  " + spineStyle.Render(preview)
			}
		}
		lines = append(lines, m.rowStyle(selected, false).Render("  ")+spineStyle.Render(spine+" ")+m.rowStyle(selected, false).Render(body))
	}
	if m.confirmKill {
		lines = append(lines, queryStyle.Render("kill selected containers? [y/N]"))
	}
	return strings.Join(lines, "\n")
}

func previewLine(preview string) string {
	for _, line := range strings.Split(preview, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}

func (m Model) style(color string) lipgloss.Style {
	style := lipgloss.NewStyle()
	if m.theme.Color && color != "" {
		style = style.Foreground(lipgloss.Color(normalizeColor(color)))
	}
	return style
}

func normalizeColor(color string) string {
	if numeric, ok := ansiColor(color); ok {
		return numeric
	}
	return color
}

func (m Model) rowStyle(selected, header bool) lipgloss.Style {
	style := m.style(m.theme.Foreground)
	if header {
		style = m.style(m.theme.Muted)
	}
	if selected && m.theme.Color {
		style = style.Background(lipgloss.Color(m.theme.SelectionBackground))
	}
	return style
}

func (m Model) highlight(text string, positions []int, selected bool) string {
	if len(positions) == 0 || !m.theme.Color {
		return text
	}
	set := make(map[int]bool, len(positions))
	for _, position := range positions {
		set[position] = true
	}
	runes := []rune(text)
	var out strings.Builder
	for index, r := range runes {
		if set[index] {
			out.WriteString(m.style(m.theme.Highlight).Bold(true).Render(string(r)))
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// IsWordRune is kept small and public for future place sources that want to
// apply the same boundary semantics as the built-in ranker.
func IsWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

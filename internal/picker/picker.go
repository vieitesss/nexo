// Package picker implements the place picker model and its terminal view.
package picker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/shell"
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

// RemoteBatch is one asynchronous machine listing. Error keeps an unreachable
// machine visible without turning a partial picker result into a failure.
type RemoteBatch struct {
	Machine string
	Places  []Place
	Agents  []capabilities.Agent
	Error   string
}

// AgentBatch is the asynchronous local agent listing used for container dots.
type AgentBatch struct {
	Agents []capabilities.Agent
	Error  string
}

// Place is the additive item model used by the picker. SearchText and
// DisplayRemainder keep ranking and rendering independent of filesystem paths;
// Path and Group remain populated for project places.
type Place struct {
	Kind             PlaceKind  `json:"kind"`
	Path             string     `json:"path,omitempty"`
	Group            string     `json:"group,omitempty"`
	Machine          string     `json:"machine,omitempty"`
	SearchText       string     `json:"search_text,omitempty"`
	DisplayRemainder string     `json:"display_remainder,omitempty"`
	ContainerID      string     `json:"container_id,omitempty"`
	ContainerName    string     `json:"container_name,omitempty"`
	Preview          string     `json:"preview,omitempty"`
	Payload          any        `json:"-"`
	OpenAction       OpenAction `json:"-"`
	Git              bool       `json:"git,omitempty"`
	LinkedWorktree   bool       `json:"linked_worktree,omitempty"`
	// GitContext is populated asynchronously for Git project candidates.
	GitContext *GitContext `json:"-"`
	// Agents is the latest asynchronous agent snapshot for a container.
	Agents []capabilities.Agent `json:"-"`
	// ParentPath is the primary checkout path for a linked worktree.
	ParentPath  string       `json:"-"`
	Decorations []Decoration `json:"decorations,omitempty"`
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
	Error      string
	Marked     bool
	// Depth is one for a group child and greater when the place is nested
	// below another checkout, such as a linked worktree.
	Depth int
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
	error  string
}

type rowIdentity struct {
	kind     RowKind
	groupKey groupKey
	place    Place
	ordinal  int
}

func (m Model) cursorIdentity() (rowIdentity, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return rowIdentity{}, false
	}
	row := m.rows[m.cursor]
	return rowIdentity{kind: row.Kind, groupKey: row.groupKey, place: row.Place, ordinal: rowOrdinal(m.rows, m.cursor)}, true
}

func rowOrdinal(rows []Row, index int) int {
	if index < 0 || index >= len(rows) {
		return 0
	}
	row := rows[index]
	ordinal := 0
	for _, candidate := range rows[:index] {
		if candidate.Kind == row.Kind && candidate.groupKey == row.groupKey {
			ordinal++
		}
	}
	return ordinal
}

func samePlace(first, second Place) bool {
	if first.Kind != second.Kind || first.Machine != second.Machine {
		return false
	}
	if first.ContainerID != "" || second.ContainerID != "" {
		return first.ContainerID != "" && first.ContainerID == second.ContainerID
	}
	if first.Path != "" || second.Path != "" {
		return first.Path != "" && first.Path == second.Path
	}
	return first.Group == second.Group && first.DisplayRemainder == second.DisplayRemainder
}

func (m *Model) restoreCursor(previous rowIdentity, keepPrevious bool) {
	if keepPrevious {
		for index, row := range m.rows {
			if row.Kind != previous.kind {
				continue
			}
			if row.Kind == PlaceRow {
				if !samePlace(row.Place, previous.place) {
					continue
				}
				m.cursor = index
				return
			}
			if row.groupKey != previous.groupKey || rowOrdinal(m.rows, index) != previous.ordinal {
				continue
			}
			m.cursor = index
			return
		}
		// If a selected place disappeared, retain its group context rather than
		// silently selecting whichever row inherited its numeric index.
		if previous.kind == PlaceRow {
			for index, row := range m.rows {
				if row.Kind == HeaderRow && row.groupKey == previous.groupKey && rowOrdinal(m.rows, index) == previous.ordinal {
					m.cursor = index
					return
				}
			}
		}
		for index, row := range m.rows {
			if row.groupKey == previous.groupKey {
				m.cursor = index
				return
			}
		}
	}
	if len(m.rows) == 0 {
		m.cursor = 0
		return
	}
	m.cursor = 0
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
	sized  bool

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

	gitLoader      GitLoader
	gitPool        *gitLoadPool
	gitTimeout     time.Duration
	remoteCmds     []tea.Cmd
	agentCmd       tea.Cmd
	remoteMachines []string
	groupErrors    map[groupKey]string
	gitErrors      map[string]string
	now            func() time.Time
}

// NewModel constructs a picker model with all groups folded.
func NewModel(places []Place, theme Theme, loaders ...GitLoader) Model {
	if theme.Foreground == "" {
		theme = DefaultTheme
	}
	m := Model{
		places:      append([]Place(nil), places...),
		theme:       theme,
		expanded:    make(map[groupKey]bool),
		collapsed:   make(map[groupKey]bool),
		marked:      make(map[ContainerRef]bool),
		groupErrors: make(map[groupKey]string),
		gitTimeout:  DefaultGitTimeout,
		gitErrors:   make(map[string]string),
		now:         time.Now,
	}
	if len(loaders) > 0 && loaders[0] != nil {
		m.gitLoader = loaders[0]
		concurrency := DefaultGitConcurrency
		if configured, ok := loaders[0].(gitLoaderSettings); ok {
			concurrency = configured.GitConcurrency()
			m.gitTimeout = configured.GitTimeout()
		}
		m.gitPool = newGitLoadPool(concurrency)
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

// WithRemoteCommands starts one Bubble Tea command per configured machine, so
// a slow machine cannot delay rows from machines that already responded.
func (m Model) WithRemoteCommands(commands []tea.Cmd) Model {
	m.remoteCmds = append([]tea.Cmd(nil), commands...)
	return m
}

// WithAgentCommand starts asynchronous agent discovery for local container
// dots when the selected backend exposes the AgentSource capability.
func (m Model) WithAgentCommand(command tea.Cmd) Model {
	m.agentCmd = command
	return m
}

// Init implements tea.Model. Git context and screen previews are started
// independently of input handling, so opening the picker never waits on I/O.
func (m Model) Init() tea.Cmd {
	commands := append([]tea.Cmd(nil), m.remoteCmds...)
	if m.agentCmd != nil {
		commands = append(commands, m.agentCmd)
	}
	if m.previewCmd != nil {
		commands = append(commands, m.previewCmd)
	}
	if m.gitLoader != nil && m.gitPool != nil {
		commands = append(commands, newGitContextCommands(m.gitLoader, m.gitPool, m.gitTimeout, m.places))
	}
	if len(commands) == 0 {
		return nil
	}
	return tea.Batch(commands...)
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

// SetClock replaces the clock used to render stopped-agent ages.
func (m *Model) SetClock(now func() time.Time) {
	if now == nil {
		m.now = time.Now
		return
	}
	m.now = now
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
	m.sized = true
}

func (m *Model) applyRemoteBatch(batch RemoteBatch) {
	machine := strings.TrimSpace(batch.Machine)
	if machine == "" {
		return
	}
	key := GroupKey{Kind: ContainerKind, Name: machine}
	if m.groupErrors == nil {
		m.groupErrors = make(map[groupKey]string)
	}
	if _, seen := m.groupErrors[key]; !seen {
		m.remoteMachines = append(m.remoteMachines, machine)
	}
	m.groupErrors[key] = batch.Error
	places := append([]Place(nil), batch.Places...)
	for index := range places {
		if places[index].Machine == "" {
			places[index].Machine = machine
		}
	}
	places = ApplyAgentDotsForMachine(places, batch.Agents, machine)
	m.places = append(m.places, places...)
	m.rebuild()
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
	previous, keepPrevious := m.cursorIdentity()
	groups := make([]group, 0)
	byKey := make(map[groupKey]int)
	for order, place := range m.places {
		key := placeGroupKey(place)
		index, ok := byKey[key]
		if !ok {
			index = len(groups)
			byKey[key] = index
			groups = append(groups, group{key: key, name: key.Name, error: m.groupErrors[key]})
		}
		groups[index].places = append(groups[index].places, place)
		groups[index].orders = append(groups[index].orders, order)
	}
	for index := range groups {
		groups[index].places, groups[index].orders = nestPlaces(groups[index].places, groups[index].orders)
	}
	for _, machine := range m.remoteMachines {
		key := GroupKey{Kind: ContainerKind, Name: machine}
		if _, ok := byKey[key]; ok {
			continue
		}
		byKey[key] = len(groups)
		groups = append(groups, group{key: key, name: machine, error: m.groupErrors[key]})
	}
	m.rows = m.rows[:0]
	query := string(m.query)
	if query == "" {
		for _, g := range groups {
			m.rows = append(m.rows, Row{Kind: HeaderRow, Group: g.name, groupKey: g.key, Expanded: m.expanded[g.key], ChildCount: m.groupCount(groups, g.key), Error: g.error})
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
		m.restoreCursor(previous, keepPrevious)
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
				m.rows = append(m.rows, Row{Kind: HeaderRow, Group: hit.group.Name, groupKey: hit.group, ChildCount: m.groupCount(groups, hit.group), Error: m.groupErrors[hit.group]})
				seenFolded[hit.group] = true
				lastRenderedGroup = hit.group
			}
			continue
		}
		if m.collapsed[hit.group] {
			if lastRenderedGroup != hit.group {
				m.rows = append(m.rows, Row{Kind: HeaderRow, Group: hit.group.Name, groupKey: hit.group, ChildCount: m.groupCount(groups, hit.group), Error: m.groupErrors[hit.group]})
				lastRenderedGroup = hit.group
			}
			continue
		}
		if lastRenderedGroup != hit.group {
			m.rows = append(m.rows, Row{Kind: HeaderRow, Group: hit.group.Name, groupKey: hit.group, Expanded: visible[hit.group] || m.expanded[hit.group], ChildCount: m.groupCount(groups, hit.group), Error: m.groupErrors[hit.group]})
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
	m.restoreCursor(previous, keepPrevious)
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

func nestPlaces(places []Place, orders []int) ([]Place, []int) {
	if len(places) < 2 {
		return places, orders
	}
	byPath := make(map[string]int, len(places))
	for index, place := range places {
		byPath[place.Path] = index
	}
	emitted := make([]bool, len(places))
	nestedPlaces := make([]Place, 0, len(places))
	nestedOrders := make([]int, 0, len(places))
	var appendPlace func(int)
	appendPlace = func(index int) {
		if index < 0 || index >= len(places) || emitted[index] {
			return
		}
		emitted[index] = true
		nestedPlaces = append(nestedPlaces, places[index])
		nestedOrders = append(nestedOrders, orders[index])
		for childIndex, candidate := range places {
			if candidate.ParentPath == places[index].Path {
				appendPlace(childIndex)
			}
		}
	}
	for index, place := range places {
		parentIndex, hasParent := byPath[place.ParentPath]
		if !hasParent || place.ParentPath == "" || parentIndex == index {
			appendPlace(index)
		}
	}
	for index := range places {
		appendPlace(index)
	}
	return nestedPlaces, nestedOrders
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
	depth := 1
	if place.ParentPath != "" && m.hasPlacePath(place.ParentPath) {
		depth = 2
	}
	return Row{Kind: PlaceRow, Group: place.Group, groupKey: placeGroupKey(place), Place: place, Remainder: visibleRemainder(place), Matches: append([]int(nil), positions...), Score: score, Marked: m.marked[ref], Depth: depth}
}

func (m Model) hasPlacePath(path string) bool {
	if path == "" {
		return false
	}
	for _, place := range m.places {
		if place.Path == path {
			return true
		}
	}
	return false
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
	case GitContextMsg:
		if msg.Err != nil {
			if m.gitErrors == nil {
				m.gitErrors = make(map[string]string)
			}
			m.gitErrors[msg.Path] = msg.Err.Error()
			m.rebuild()
			return m, nil
		}
		delete(m.gitErrors, msg.Path)
		for index := range m.places {
			if m.places[index].Path != msg.Path {
				continue
			}
			value := msg.Context
			m.places[index].GitContext = &value
			m.places[index].ParentPath = value.PrimaryPath
			if value.PrimaryPath != "" {
				m.places[index].LinkedWorktree = true
				for _, primary := range m.places {
					if primary.Path == value.PrimaryPath {
						// A linked worktree belongs beside its primary
						// checkout even when discovery assigned a different
						// group to the two paths.
						m.places[index].Group = primary.Group
						m.places[index].DisplayRemainder = projectRemainder(m.places[index].Path, primary.Group)
						break
					}
				}
			}
			decoration := value.Decoration()
			if decoration.Text != "" {
				m.places[index].Decorations = append(m.places[index].Decorations, decoration)
			}
		}
		m.rebuild()
		return m, nil
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil
	case PreviewBatch:
		for ref, preview := range msg.Previews {
			m.SetPreview(ref, preview)
		}
		return m, nil
	case AgentBatch:
		if msg.Error == "" {
			m.places = ApplyAgentDotsForMachine(m.places, msg.Agents, LocalMachine())
			m.rebuild()
		}
		return m, nil
	case RemoteBatch:
		m.applyRemoteBatch(msg)
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

// View renders a full terminal surface after the first WindowSizeMsg. Before
// Bubble Tea supplies dimensions, the compact legacy frame is used because
// there is no rectangle to paint yet.
func (m Model) View() string {
	if !m.sized {
		return m.legacyView()
	}
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	return m.fullView()
}

func (m Model) legacyView() string {
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
			if row.Error != "" {
				lines = append(lines, m.rowStyle(selected, true).Render(arrow+" "+label+" — "+row.Error))
			} else if row.Expanded {
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
		for _, decoration := range row.Place.Decorations {
			if decoration.Text == "" {
				continue
			}
			body += " " + m.decorationStyle(decoration).Render(decoration.Text)
		}
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
		indent := "  "
		if row.Depth > 1 {
			indent = strings.Repeat("  ", row.Depth-1) + spineStyle.Render("│ ")
		}
		lines = append(lines, m.rowStyle(selected, false).Render(indent)+spineStyle.Render(spine+" ")+m.rowStyle(selected, false).Render(body))
	}
	if m.confirmKill {
		lines = append(lines, queryStyle.Render("kill selected containers? [y/N]"))
	}
	return strings.Join(lines, "\n")
}

func (m Model) fullView() string {
	items := make([]shell.Item, 0, len(m.rows))
	for index, row := range m.rows {
		selected := index == m.cursor
		item := shell.Item{Selectable: row.Kind == PlaceRow, Selected: selected}
		switch row.Kind {
		case HeaderRow:
			arrow := "▸"
			if row.Expanded {
				arrow = "▾"
			}
			label := filepath.Base(filepath.ToSlash(row.Group))
			if label == "." || label == "/" || label == "" {
				label = row.Group
			}
			primary := arrow + " " + label
			if row.Error != "" {
				primary += " — " + row.Error
			} else if !row.Expanded {
				primary += " (" + itoa(row.ChildCount) + ")"
			}
			item.Primary = m.style(m.theme.Muted).Render(primary)
		case NewContainerRow:
			item.Primary = "  " + m.style(m.theme.Muted).Render("└ ") + row.Place.DisplayRemainder
		case PlaceRow:
			following := index+1 < len(m.rows) && m.rows[index+1].Kind != HeaderRow && m.rows[index+1].groupKey == row.groupKey
			spine := "├"
			if !following {
				spine = "└"
			}
			prefix := "  "
			if row.Depth > 1 {
				prefix = strings.Repeat("  ", row.Depth-1) + m.style(m.theme.Muted).Render("│ ")
			}
			primary := m.highlight(row.Remainder, row.Matches, selected)
			primary = strings.ReplaceAll(primary, "/", m.style(m.theme.Muted).Render(" │ "))
			if row.Place.Kind == ContainerKind {
				mark := "☐"
				if row.Marked {
					mark = "☑"
				}
				primary = mark + " " + primary
				hasDot := false
				for _, decoration := range row.Place.Decorations {
					if decoration.Text != "" {
						primary += " " + m.decorationStyle(decoration).Render(decoration.Text)
						if decoration.Text == "●" {
							hasDot = true
						}
					}
				}
				if !hasDot {
					if dot, ok := AgentDot(row.Place.Agents); ok {
						primary += " " + m.decorationStyle(dot).Render(dot.Text)
					}
				}
			} else {
				primary = strings.TrimSpace(primary)
			}
			item.Primary = prefix + m.style(m.theme.Muted).Render(spine+" ") + primary
			continuation := "  "
			if following {
				continuation = "│ "
			}
			secondaryPrefix := prefix + m.style(m.theme.Muted).Render(continuation)
			if row.Place.Kind == ContainerKind {
				secondaryPrefix += "  "
			}
			metadata := m.placeMetadata(row.Place, m.width-shell.StringWidth(secondaryPrefix))
			if shell.StringWidth(metadata) > 0 {
				item.Secondary = secondaryPrefix + metadata
			}
		}
		if index+1 < len(m.rows) && m.rows[index+1].Kind == HeaderRow && m.rows[index+1].groupKey != row.groupKey {
			item.Separator = true
		}
		items = append(items, item)
	}

	cursor := m.style(m.theme.Highlight).Render("▌")
	search := shell.PromptLine(m.width, "Search: ", string(m.query), cursor)
	if m.renameMode {
		search = shell.PromptLine(m.width, "rename: ", string(m.renameInput), cursor)
	}
	dialog := ""
	if m.confirmKill {
		dialog = "kill selected containers? [y/N]"
	}
	return shell.Render(shell.Options{
		Width:  m.width,
		Height: m.height,
		Search: search,
		Items:  items,
		Hints:  m.hints(),
		Dialog: dialog,
		Paint: func(value string, selected bool) string {
			style := m.surfaceStyle(m.theme.Foreground)
			if selected && m.theme.Color {
				style = style.Background(lipgloss.Color(normalizeColor(m.theme.SelectionBackground)))
			}
			return style.Render(value)
		},
		PaintSearch: func(value string) string { return m.surfaceStyle(m.theme.Foreground).Render(value) },
		PaintHint:   func(value string) string { return m.surfaceStyle(m.theme.Muted).Render(value) },
	})
}

func (m Model) placeMetadata(place Place, width int) string {
	if place.Kind == ProjectKind {
		if place.GitContext != nil {
			return m.gitContextLine(*place.GitContext, width)
		}
		if errText := m.gitErrors[place.Path]; errText != "" {
			return m.style(m.theme.Muted).Render("git: error " + errText)
		}
		if place.Git {
			return m.style(m.theme.Muted).Render("git: loading…")
		}
		return ""
	}
	if place.Kind != ContainerKind {
		return ""
	}
	if len(place.Agents) > 0 {
		parts := make([]string, 0, len(place.Agents))
		for _, agent := range place.Agents {
			dot := m.style(agentDotColor(agent)).Render("●")
			value := strings.TrimSpace(agent.Harness) + " " + strings.TrimSpace(agent.State)
			if strings.TrimSpace(agent.Harness) == "" {
				value = strings.TrimSpace(agent.State)
			}
			if strings.EqualFold(agent.State, "stopped") && agent.StoppedSince != nil {
				clock := m.now
				if clock == nil {
					clock = time.Now
				}
				value += " " + stoppedAge(clock(), *agent.StoppedSince)
			}
			parts = append(parts, dot+" "+m.style(m.theme.Muted).Render(value))
		}
		return strings.Join(parts, " · ")
	}
	if preview := previewLine(place.Preview); preview != "" {
		return m.style(m.theme.Muted).Render(preview)
	}
	return m.style(m.theme.Muted).Render("no agents")
}

func (m Model) gitContextLine(context GitContext, width int) string {
	identity := context.Identity
	changes := ""
	if context.Adds != 0 || context.Deletes != 0 {
		if context.Adds != 0 {
			changes += "+" + itoa(context.Adds)
		}
		if context.Deletes != 0 {
			if changes != "" {
				changes += " "
			}
			changes += "-" + itoa(context.Deletes)
		}
	} else if context.Dirty {
		changes = "*"
	}
	divergence := ""
	if context.Ahead > 0 {
		divergence += "↑" + itoa(context.Ahead)
	}
	if context.Behind > 0 {
		if divergence != "" {
			divergence += " "
		}
		divergence += "↓" + itoa(context.Behind)
	}
	if identity == "" && changes == "" && divergence == "" {
		return m.style(m.theme.Muted).Render("clean")
	}
	if width <= 0 {
		return ""
	}
	if gitWidth(identity, changes, divergence) > width {
		// Divergence is the least important field and is dropped first.
		divergence = ""
	}
	if gitWidth(identity, changes, divergence) > width {
		// Keep the change signal while discarding its counts.
		if context.Adds != 0 || context.Deletes != 0 {
			changes = ""
			if context.Adds != 0 {
				changes += "+"
			}
			if context.Deletes != 0 {
				if changes != "" {
					changes += " "
				}
				changes += "-"
			}
		}
	}
	if gitWidth(identity, changes, divergence) > width {
		// Identity is retained longest; only now may it be truncated.
		suffix := gitWidth("", changes, divergence)
		if suffix > 0 {
			suffix++ // separator between identity and the retained suffix
		}
		identity = shell.Truncate(identity, width-suffix, "…")
	}
	if gitWidth(identity, changes, divergence) > width {
		// A one-cell surface cannot fit a separated suffix; identity wins.
		changes = ""
		divergence = ""
		identity = shell.Truncate(identity, width, "…")
	}
	parts := make([]string, 0, 4)
	if identity != "" {
		parts = append(parts, m.style(m.theme.Muted).Render(identity))
	}
	if changes != "" {
		for _, change := range strings.Fields(changes) {
			if strings.HasPrefix(change, "+") {
				parts = append(parts, m.style("green").Render(change))
			} else if strings.HasPrefix(change, "-") {
				parts = append(parts, m.style("red").Render(change))
			} else {
				parts = append(parts, m.style("yellow").Render(change))
			}
		}
	}
	if divergence != "" {
		parts = append(parts, m.style(m.theme.Muted).Render(divergence))
	}
	return strings.Join(parts, " ")
}

func gitWidth(identity, changes, divergence string) int {
	parts := make([]string, 0, 3)
	for _, value := range []string{identity, changes, divergence} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return 0
	}
	return shell.StringWidth(strings.Join(parts, " "))
}

func agentDotColor(agent capabilities.Agent) string {
	if strings.EqualFold(strings.TrimSpace(agent.State), "stopped") {
		if agent.Seen {
			return "green"
		}
		return "red"
	}
	return "yellow"
}

func stoppedAge(now, since time.Time) string {
	seconds := int(now.Sub(since) / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh", minutes/60)
}

func (m Model) hints() []shell.Hint {
	if m.renameMode {
		return []shell.Hint{m.hint(defaultKeyMap.Accept, "accept"), m.hint(defaultKeyMap.Clear, "cancel")}
	}
	if m.confirmKill {
		return []shell.Hint{m.hintKey("y", "kill"), m.hintKey("n", "cancel")}
	}
	hints := make([]shell.Hint, 0, 10)
	if len(m.rows) > 0 {
		hints = append(hints, m.hintKeys("↑↓", "move"))
		if m.cursor < len(m.rows) {
			switch m.rows[m.cursor].Kind {
			case HeaderRow:
				hints = append(hints, m.hint(defaultKeyMap.Accept, "expand"), m.hintKeys("←→", "fold"))
			case PlaceRow:
				hints = append(hints, m.hint(defaultKeyMap.Accept, "open"), m.hint(defaultKeyMap.Collapse, "fold"))
			default:
				hints = append(hints, m.hint(defaultKeyMap.Collapse, "fold"))
			}
		}
	}
	hints = append(hints, m.hint(defaultKeyMap.Clear, "cancel"))
	if len(m.query) == 0 && (len(m.markedTargets()) > 0 || m.currentContainer()) {
		hints = append(hints, m.hint(defaultKeyMap.Kill, "kill"))
	}
	if m.currentContainer() {
		hints = append(hints, m.hint(defaultKeyMap.Rename, "rename"), m.hint(defaultKeyMap.MarkNext, "mark"))
	}
	return hints
}

func (m Model) hint(binding key.Binding, label string) shell.Hint {
	return m.hintKey(firstKey(binding), label)
}

func (m Model) hintKey(keyValue, label string) shell.Hint {
	return shell.Hint{Key: m.style(m.theme.Highlight).Bold(true).Render(displayKey(keyValue)), Label: label}
}

func (m Model) hintKeys(keyValue, label string) shell.Hint {
	return shell.Hint{Key: m.style(m.theme.Highlight).Bold(true).Render(keyValue), Label: label}
}

func displayKey(value string) string {
	switch value {
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "left":
		return "←"
	case "right":
		return "→"
	case "shift+tab":
		return "⇧tab"
	default:
		return value
	}
}

func firstKey(binding key.Binding) string {
	keys := binding.Keys()
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
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

func (m Model) surfaceStyle(color string) lipgloss.Style {
	style := m.style(color)
	if m.theme.Color && m.theme.Background != "" {
		style = style.Background(lipgloss.Color(normalizeColor(m.theme.Background)))
	}
	return style
}

func normalizeColor(color string) string {
	if numeric, ok := ansiColor(color); ok {
		return numeric
	}
	return color
}

func (m Model) decorationStyle(decoration Decoration) lipgloss.Style {
	color := m.theme.Foreground
	switch decoration.Style {
	case DecorationMuted:
		color = m.theme.Muted
	case DecorationAccent:
		color = m.theme.Highlight
	case DecorationRed:
		color = "red"
	case DecorationYellow:
		color = "yellow"
	case DecorationGreen:
		color = "green"
	}
	return m.style(color)
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

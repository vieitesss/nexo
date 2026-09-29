// Package picker implements the place picker model and its terminal view.
package picker

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/projects"
)

// PlaceKind identifies the kind of place represented by a picker item. New
// place kinds can be added without changing the row model.
type PlaceKind string

const (
	// ProjectKind is a configured project candidate.
	ProjectKind PlaceKind = "project"
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

// Place is the additive item model used by the picker. SearchText and
// DisplayRemainder keep ranking and rendering independent of filesystem paths;
// Path and Group remain populated for project places.
type Place struct {
	Kind             PlaceKind
	Path             string
	Group            string
	SearchText       string
	DisplayRemainder string
	Payload          any
	OpenAction       OpenAction
	Git              bool
	LinkedWorktree   bool
	Decorations      []Decoration
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
	HeaderRow RowKind = "header"
	PlaceRow  RowKind = "place"
)

// Row is an observable picker row. It is useful for model tests and for
// adapters that want to decorate rows without changing ranking.
type Row struct {
	Kind       RowKind
	Group      string
	Place      Place
	Remainder  string
	Matches    []int
	Score      int
	Expanded   bool
	ChildCount int
}

type group struct {
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
	expanded  map[string]bool
	collapsed map[string]bool
	selected  *Place
	cancelled bool
}

// NewModel constructs a picker model with all groups folded.
func NewModel(places []Place, theme Theme) Model {
	if theme.Foreground == "" {
		theme = DefaultTheme
	}
	m := Model{
		places:    append([]Place(nil), places...),
		theme:     theme,
		expanded:  make(map[string]bool),
		collapsed: make(map[string]bool),
	}
	m.rebuild()
	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

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

// SetQuery is primarily useful to model tests and embedding callers.
func (m *Model) SetQuery(query string) {
	m.query = []rune(query)
	m.collapsed = make(map[string]bool)
	m.expanded = make(map[string]bool)
	m.rebuild()
}

// SetSize updates the terminal dimensions used by View.
func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
}

func (m *Model) rebuild() {
	groups := make([]group, 0)
	byName := make(map[string]int)
	for order, place := range m.places {
		index, ok := byName[place.Group]
		if !ok {
			index = len(groups)
			byName[place.Group] = index
			groups = append(groups, group{name: place.Group})
		}
		groups[index].places = append(groups[index].places, place)
		groups[index].orders = append(groups[index].orders, order)
	}
	m.rows = m.rows[:0]
	query := string(m.query)
	if query == "" {
		for _, g := range groups {
			m.rows = append(m.rows, Row{
				Kind:       HeaderRow,
				Group:      g.name,
				Expanded:   m.expanded[g.name],
				ChildCount: len(g.places),
			})
			if m.expanded[g.name] {
				for index, place := range g.places {
					m.rows = append(m.rows, m.placeRow(place, 0, nil, index == len(g.places)-1))
				}
			}
		}
		m.clampCursor()
		return
	}

	type candidate struct {
		place Place
		group string
		full  Match
		rem   Match
		remOK bool
		order int
	}
	var ranked []candidate
	visible := make(map[string]bool)
	for _, g := range groups {
		for placeIndex, place := range g.places {
			full, ok := Rank(query, searchText(place))
			if !ok {
				continue
			}
			rem := visibleRemainder(place)
			remMatch, remOK := Rank(query, rem)
			if remOK {
				visible[g.name] = true
			}
			ranked = append(ranked, candidate{place: place, group: g.name, full: full, rem: remMatch, remOK: remOK, order: g.orders[placeIndex]})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].full.Score != ranked[j].full.Score {
			return ranked[i].full.Score > ranked[j].full.Score
		}
		return ranked[i].order < ranked[j].order
	})

	seenFolded := make(map[string]bool)
	lastRenderedGroup := ""
	for _, hit := range ranked {
		if !visible[hit.group] && !m.expanded[hit.group] {
			if !seenFolded[hit.group] {
				m.rows = append(m.rows, Row{Kind: HeaderRow, Group: hit.group, ChildCount: m.groupCount(groups, hit.group)})
				seenFolded[hit.group] = true
				lastRenderedGroup = hit.group
			}
			continue
		}
		if m.collapsed[hit.group] {
			if lastRenderedGroup != hit.group {
				m.rows = append(m.rows, Row{Kind: HeaderRow, Group: hit.group, ChildCount: m.groupCount(groups, hit.group)})
				lastRenderedGroup = hit.group
			}
			continue
		}
		if lastRenderedGroup != hit.group {
			m.rows = append(m.rows, Row{Kind: HeaderRow, Group: hit.group, Expanded: visible[hit.group] || m.expanded[hit.group], ChildCount: m.groupCount(groups, hit.group)})
			lastRenderedGroup = hit.group
		}
		positions := hit.rem.Positions
		if !hit.remOK {
			positions = nil
		}
		m.rows = append(m.rows, m.placeRow(hit.place, hit.full.Score, positions, false))
	}
	m.clampCursor()
}

func (m Model) groupCount(groups []group, name string) int {
	for _, g := range groups {
		if g.name == name {
			return len(g.places)
		}
	}
	return 0
}

func (m Model) placeRow(place Place, score int, positions []int, _ bool) Row {
	return Row{Kind: PlaceRow, Group: place.Group, Place: place, Remainder: visibleRemainder(place), Matches: append([]int(nil), positions...), Score: score}
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
}

var defaultKeyMap = KeyMap{
	Up:       key.NewBinding(key.WithKeys("up", "ctrl+k")),
	Down:     key.NewBinding(key.WithKeys("down", "ctrl+j")),
	Accept:   key.NewBinding(key.WithKeys("enter")),
	Expand:   key.NewBinding(key.WithKeys("right", tea.KeySpace.String())),
	Collapse: key.NewBinding(key.WithKeys("left")),
	Clear:    key.NewBinding(key.WithKeys("esc")),
}

// Update handles search input, folding, navigation and accepting a project.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyMsg:
		switch {
		case msg.Type == tea.KeyCtrlC, msg.Type == tea.KeyEsc:
			m.cancelled = true
			return m, tea.Quit
		case msg.Type == tea.KeyRunes && len(m.query) == 0 && string(msg.Runes) == "q":
			m.cancelled = true
			return m, tea.Quit
		case key.Matches(msg, defaultKeyMap.Clear):
			return m, nil
		case key.Matches(msg, defaultKeyMap.Up):
			m.cursor--
			m.clampCursor()
			return m, nil
		case key.Matches(msg, defaultKeyMap.Down):
			m.cursor++
			m.clampCursor()
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
			selected := row.Place
			m.selected = &selected
			return m, tea.Quit
		case msg.Type == tea.KeyBackspace, msg.Type == tea.KeyDelete:
			if len(m.query) > 0 {
				m.query = m.query[:len(m.query)-1]
				m.collapsed = make(map[string]bool)
				m.expanded = make(map[string]bool)
				m.rebuild()
			}
			return m, nil
		case msg.Type == tea.KeyRunes:
			if len(msg.Runes) > 0 {
				m.query = append(m.query, msg.Runes...)
				m.collapsed = make(map[string]bool)
				m.expanded = make(map[string]bool)
				m.rebuild()
			}
			return m, nil
		}
	}
	return m, nil
}

func (m Model) expandCurrent() (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 || m.rows[m.cursor].Kind != HeaderRow {
		return m, nil
	}
	name := m.rows[m.cursor].Group
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
	groupName := row.Group
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
		if candidate.Kind == HeaderRow && candidate.Group == groupName {
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
	lines = append(lines, queryStyle.Render("> "+string(m.query)))
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
		spine := "├"
		if index+1 >= len(m.rows) || m.rows[index+1].Kind == HeaderRow || m.rows[index+1].Group != row.Group {
			spine = "└"
		}
		spineStyle := m.style(m.theme.Muted)
		body := m.highlight(row.Remainder, row.Matches, selected)
		body = strings.ReplaceAll(body, "/", spineStyle.Render(" │ "))
		lines = append(lines, m.rowStyle(selected, false).Render("  ")+spineStyle.Render(spine+" ")+m.rowStyle(selected, false).Render(body))
	}
	return strings.Join(lines, "\n")
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

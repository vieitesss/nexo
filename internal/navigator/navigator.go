// Package navigator implements the agent navigator model and terminal view.
package navigator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

// RefreshMsg replaces the navigator's current agent snapshot. Commands that
// embed the navigator can send this message from any source, while the model
// uses its AgentSource automatically when one was supplied.
type RefreshMsg struct {
	Agents []capabilities.Agent
	Err    error
}

type refreshTickMsg struct{}

// Row is the visible representation of one agent pane.
type Row struct {
	Agent capabilities.Agent
	Score int
}

// Model is the Bubble Tea agent navigator. AgentSource calls are made by tea
// commands, rather than Update, so a slow backend never blocks key handling.
type Model struct {
	source          capabilities.AgentSource
	ctx             context.Context
	theme           picker.Theme
	interval        time.Duration
	now             func() time.Time
	query           []rune
	agents          []capabilities.Agent
	rows            []Row
	cursor          int
	selected        *capabilities.Agent
	cancelled       bool
	err             error
	width           int
	height          int
	sized           bool
	refreshInFlight *bool
}

// NewModel constructs an agent navigator model.
func NewModel(source capabilities.AgentSource, theme picker.Theme, interval time.Duration) Model {
	if theme.Foreground == "" {
		theme = picker.DefaultTheme
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	m := Model{source: source, ctx: context.Background(), theme: theme, interval: interval, now: time.Now, refreshInFlight: new(bool)}
	m.rebuild()
	return m
}

// Init starts the first asynchronous refresh and its recurring timer.
func (m Model) Init() tea.Cmd {
	if m.refreshInFlight == nil {
		m.refreshInFlight = new(bool)
	}
	*m.refreshInFlight = true
	return tea.Batch(m.refresh(), m.tick())
}

// Query returns the current fuzzy filter.
func (m Model) Query() string { return string(m.query) }

// Rows returns the current visible rows.
func (m Model) Rows() []Row { return append([]Row(nil), m.rows...) }

// Agents returns the latest complete source snapshot.
func (m Model) Agents() []capabilities.Agent { return append([]capabilities.Agent(nil), m.agents...) }

// Cursor returns the visible row selected by the keyboard.
func (m Model) Cursor() int { return m.cursor }

// SetSize records the terminal surface supplied by Bubble Tea.
func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
	m.sized = true
}

// Selected returns the pane accepted by Enter.
func (m Model) Selected() (capabilities.Agent, bool) {
	if m.selected == nil {
		return capabilities.Agent{}, false
	}
	return *m.selected, true
}

// Cancelled reports whether the navigator exited without accepting a pane.
func (m Model) Cancelled() bool { return m.cancelled }

// Error returns the most recent refresh error, if any. The model keeps the
// previous rows visible while a refresh fails.
func (m Model) Error() error { return m.err }

// SetContext replaces the context used for asynchronous source refreshes.
func (m *Model) SetContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.ctx = ctx
}

// SetClock replaces the clock used to render stopped ages. It is useful for
// deterministic model and golden tests.
func (m *Model) SetClock(now func() time.Time) {
	if now == nil {
		m.now = time.Now
		return
	}
	m.now = now
	m.rebuild()
}

// SetAgents supplies a source snapshot and rebuilds the visible rows.
func (m *Model) SetAgents(agents []capabilities.Agent) {
	m.agents = append([]capabilities.Agent(nil), agents...)
	m.err = nil
	m.rebuild()
}

// SetQuery updates the fuzzy filter.
func (m *Model) SetQuery(query string) {
	m.query = []rune(query)
	m.rebuild()
}

// Update handles refreshes, filtering, navigation and pane selection.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshMsg:
		if m.refreshInFlight != nil {
			*m.refreshInFlight = false
		}
		if msg.Err != nil {
			m.err = msg.Err
		} else {
			m.SetAgents(msg.Agents)
		}
		return m, nil
	case refreshTickMsg:
		if m.refreshInFlight != nil && *m.refreshInFlight {
			return m, m.tick()
		}
		if m.refreshInFlight == nil {
			m.refreshInFlight = new(bool)
		}
		*m.refreshInFlight = true
		return m, tea.Batch(m.refresh(), m.tick())
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyMsg:
		switch {
		case msg.Type == tea.KeyCtrlC, msg.Type == tea.KeyEsc:
			m.cancelled = true
			return m, tea.Quit
		case key.Matches(msg, upKey):
			m.cursor--
			m.clampCursor()
			return m, nil
		case key.Matches(msg, downKey):
			m.cursor++
			m.clampCursor()
			return m, nil
		case key.Matches(msg, acceptKey):
			if len(m.rows) == 0 {
				return m, nil
			}
			selected := m.rows[m.cursor].Agent
			m.selected = &selected
			return m, tea.Quit
		case msg.Type == tea.KeyBackspace, msg.Type == tea.KeyDelete:
			if len(m.query) > 0 {
				m.query = m.query[:len(m.query)-1]
				m.rebuild()
			}
			return m, nil
		case msg.Type == tea.KeyRunes:
			m.query = append(m.query, msg.Runes...)
			m.rebuild()
			return m, nil
		}
	}
	return m, nil
}

var (
	upKey     = key.NewBinding(key.WithKeys("up", "ctrl+k"))
	downKey   = key.NewBinding(key.WithKeys("down", "ctrl+j"))
	acceptKey = key.NewBinding(key.WithKeys("enter"))
)

func (m Model) refresh() tea.Cmd {
	if m.source == nil {
		return nil
	}
	return func() tea.Msg {
		agents, err := m.source.ListAgents(m.ctx)
		return RefreshMsg{Agents: agents, Err: err}
	}
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

func (m *Model) rebuild() {
	previous := ""
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		previous = agentIdentity(m.rows[m.cursor].Agent)
	}
	agents := append([]capabilities.Agent(nil), m.agents...)
	sort.SliceStable(agents, func(i, j int) bool {
		rankI, rankJ := attentionRank(agents[i]), attentionRank(agents[j])
		if rankI != rankJ {
			return rankI < rankJ
		}
		if agents[i].Target != agents[j].Target {
			return agents[i].Target < agents[j].Target
		}
		return agents[i].PaneID < agents[j].PaneID
	})

	query := string(m.query)
	m.rows = m.rows[:0]
	for _, agent := range agents {
		search := strings.Join([]string{agent.Target, agent.Harness, agent.Branch}, " ")
		match, ok := picker.Rank(query, search)
		if !ok {
			continue
		}
		m.rows = append(m.rows, Row{Agent: agent, Score: match.Score})
	}
	if previous != "" {
		for index, row := range m.rows {
			if agentIdentity(row.Agent) == previous {
				m.cursor = index
				return
			}
		}
	}
	m.clampCursor()
}

func agentIdentity(agent capabilities.Agent) string {
	if agent.PaneID != "" {
		return agent.PaneID
	}
	return agent.Target
}

func attentionRank(agent capabilities.Agent) int {
	switch strings.ToLower(strings.TrimSpace(agent.State)) {
	case "stopped":
		if !agent.Seen {
			return 0
		}
		return 2
	case "working":
		return 1
	default:
		return 1
	}
}

func (m *Model) clampCursor() {
	if len(m.rows) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
}

// View renders a full terminal surface after the first WindowSizeMsg. Before
// Bubble Tea supplies dimensions, the compact frame remains useful to callers
// that inspect a model before it is started.
func (m Model) View() string {
	if !m.sized {
		return m.legacyView()
	}
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	items := make([]shell.Item, 0, len(m.rows))
	for index, row := range m.rows {
		selected := index == m.cursor
		items = append(items, shell.Item{
			Primary:    m.primaryText(row.Agent),
			Secondary:  m.secondaryText(row.Agent, index+1 < len(m.rows)),
			Selectable: true,
			Selected:   selected,
		})
	}
	dialog := ""
	if m.err != nil {
		dialog = m.err.Error()
	}
	search := shell.PromptLine(m.width, "Search: ", string(m.query), m.style(m.theme.Highlight).Render("▌"))
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
				style = style.Background(lipgloss.Color(m.theme.SelectionBackground))
			}
			return style.Render(value)
		},
		PaintSearch: func(value string) string { return m.surfaceStyle(m.theme.Foreground).Render(value) },
		PaintHint:   func(value string) string { return m.surfaceStyle(m.theme.Muted).Render(value) },
	})
}

func (m Model) legacyView() string {
	lines := []string{m.style(m.theme.Foreground).Render("> " + string(m.query))}
	for index, row := range m.rows {
		selected := index == m.cursor
		text := rowText(row.Agent, m.now())
		style := m.style(m.theme.Foreground)
		if selected && m.theme.Color {
			style = style.Background(lipgloss.Color(m.theme.SelectionBackground))
		}
		marker := m.marker(row.Agent)
		lines = append(lines, style.Render(marker+" "+text))
	}
	return strings.Join(lines, "\n")
}

func (m Model) primaryText(agent capabilities.Agent) string {
	parts := []string{m.marker(agent), strings.TrimSpace(agent.Harness)}
	if strings.TrimSpace(agent.Container) != "" {
		parts = append(parts, "·", strings.TrimSpace(agent.Container))
	}
	return strings.Join(parts, " ")
}

func (m Model) secondaryText(agent capabilities.Agent, continuing ...bool) string {
	continues := true
	if len(continuing) > 0 {
		continues = continuing[0]
	}
	indent := "  "
	if continues {
		indent += m.style(m.theme.Muted).Render("│ ")
	} else {
		indent += m.style(m.theme.Muted).Render("  ")
	}
	width := m.width - shell.StringWidth(indent)
	if m.width <= 0 {
		width = 1 << 20
	}
	if width < 0 {
		width = 0
	}
	parts := make([]metadataPart, 0, 4)
	if agent.Window != "" {
		parts = append(parts, metadataPart{value: agent.Window, flexible: true})
	}
	if agent.Target != "" {
		parts = append(parts, metadataPart{value: agent.Target, flexible: true})
	}
	if agent.Branch != "" {
		parts = append(parts, metadataPart{value: "⎇ " + agent.Branch})
	}
	if strings.EqualFold(strings.TrimSpace(agent.State), "stopped") && agent.StoppedSince != nil {
		parts = append(parts, metadataPart{value: age(m.now(), *agent.StoppedSince)})
	}
	metadata := fitMetadata(parts, width)
	if metadata == "" {
		metadata = "no metadata"
	}
	return indent + m.style(m.theme.Muted).Render(metadata)
}

type metadataPart struct {
	value    string
	flexible bool
}

// fitMetadata budgets each navigator field independently. Window and target
// may consume the flexible space, while branch and age are reserved first so
// a long location cannot hide the identifying suffix.
func fitMetadata(parts []metadataPart, width int) string {
	if len(parts) == 0 || width <= 0 {
		return ""
	}
	budgets := make([]int, len(parts))
	for index, part := range parts {
		budgets[index] = shell.StringWidth(part.value)
	}
	separators := (len(parts) - 1) * shell.StringWidth(" · ")
	available := width - separators
	if available <= 0 {
		values := make([]string, 0, len(parts))
		for _, part := range parts {
			values = append(values, part.value)
		}
		return shell.Truncate(strings.Join(values, " · "), width, "")
	}
	used := metadataBudgetTotal(budgets)
	if used > available {
		flexible := make([]int, 0, len(parts))
		protected := make([]int, 0, len(parts))
		for index, part := range parts {
			if part.flexible {
				flexible = append(flexible, index)
			} else {
				protected = append(protected, index)
			}
		}
		reserve := 0
		for _, index := range protected {
			reserve += budgets[index]
		}
		flexibleWidth := available - reserve
		if flexibleWidth < 0 {
			flexibleWidth = 0
		}
		allocateFlexibleBudgets(budgets, flexible, flexibleWidth)
		used = metadataBudgetTotal(budgets)
		if used > available {
			reduceMetadataBudgets(budgets, used-available, protected)
		}
	}
	fitted := make([]string, 0, len(parts))
	for index, part := range parts {
		if budgets[index] <= 0 {
			continue
		}
		fitted = append(fitted, truncateMetadataField(part.value, budgets[index]))
	}
	result := strings.Join(fitted, " · ")
	if shell.StringWidth(result) > width {
		return shell.Truncate(result, width, "")
	}
	return result
}

func allocateFlexibleBudgets(budgets, flexible []int, available int) {
	desired := append([]int(nil), budgets...)
	for _, index := range flexible {
		budgets[index] = 1
	}
	available -= len(flexible)
	if available <= 0 {
		return
	}
	// Fill short fields first, then share whatever remains between long
	// fields. This keeps a short target intact beside a long window.
	for _, index := range flexible {
		need := desired[index]
		if need-1 > available {
			continue
		}
		budgets[index] += need - 1
		available -= need - 1
	}
	for available > 0 {
		index := -1
		for _, candidate := range flexible {
			if budgets[candidate] >= desired[candidate] {
				continue
			}
			if index < 0 || budgets[candidate] < budgets[index] {
				index = candidate
			}
		}
		if index < 0 {
			return
		}
		budgets[index]++
		available--
	}
}

func metadataBudgetTotal(budgets []int) int {
	total := 0
	for _, budget := range budgets {
		total += budget
	}
	return total
}

func reduceMetadataBudgets(budgets []int, excess int, candidates []int) {
	for excess > 0 {
		index := -1
		for _, candidate := range candidates {
			if candidate >= len(budgets) || budgets[candidate] <= 1 {
				continue
			}
			if index < 0 || budgets[candidate] > budgets[index] {
				index = candidate
			}
		}
		if index < 0 {
			return
		}
		budgets[index]--
		excess--
	}
}

func truncateMetadataField(value string, width int) string {
	if width <= 1 {
		return shell.Truncate(value, width, "")
	}
	return shell.Truncate(value, width, "…")
}

func (m Model) hints() []shell.Hint {
	hints := make([]shell.Hint, 0, 4)
	if len(m.rows) > 0 {
		hints = append(hints, m.hintKeys("↑↓", "move"), m.hint(acceptKey, "open"))
	}
	hints = append(hints, m.hintKey("esc", "cancel"))
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

func (m Model) marker(agent capabilities.Agent) string {
	marker := "●"
	if !m.theme.Color {
		return marker
	}
	color := "3"
	state := strings.ToLower(strings.TrimSpace(agent.State))
	if state == "stopped" && !agent.Seen {
		color = "1"
	} else if state == "stopped" && agent.Seen {
		color = "2"
	}
	return m.style(color).Render(marker)
}

func (m Model) style(color string) lipgloss.Style {
	style := lipgloss.NewStyle()
	if m.theme.Color && color != "" {
		style = style.Foreground(lipgloss.Color(color))
	}
	return style
}

func (m Model) surfaceStyle(color string) lipgloss.Style {
	style := m.style(color)
	if m.theme.Color && m.theme.Background != "" {
		style = style.Background(lipgloss.Color(m.theme.Background))
	}
	return style
}

func rowText(agent capabilities.Agent, now time.Time) string {
	parts := []string{agent.Harness}
	for _, value := range []string{agent.Container, agent.Window, agent.Target} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	if agent.Branch != "" {
		parts = append(parts, "⎇ "+agent.Branch)
	}
	text := strings.Join(parts, " · ")
	if strings.EqualFold(strings.TrimSpace(agent.State), "stopped") && agent.StoppedSince != nil {
		text += "  " + age(now, *agent.StoppedSince)
	}
	return text
}

func age(now, since time.Time) string {
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

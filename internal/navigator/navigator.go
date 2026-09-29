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
	refreshInFlight bool
}

// NewModel constructs an agent navigator model.
func NewModel(source capabilities.AgentSource, theme picker.Theme, interval time.Duration) Model {
	if theme.Foreground == "" {
		theme = picker.DefaultTheme
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	m := Model{source: source, ctx: context.Background(), theme: theme, interval: interval, now: time.Now}
	m.rebuild()
	return m
}

// Init starts the first asynchronous refresh and its recurring timer.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refresh(), m.tick())
}

// Query returns the current fuzzy filter.
func (m Model) Query() string { return string(m.query) }

// Rows returns the current visible rows.
func (m Model) Rows() []Row { return append([]Row(nil), m.rows...) }

// Agents returns the latest complete source snapshot.
func (m Model) Agents() []capabilities.Agent { return append([]capabilities.Agent(nil), m.agents...) }

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
		m.refreshInFlight = false
		if msg.Err != nil {
			m.err = msg.Err
		} else {
			m.SetAgents(msg.Agents)
		}
		return m, nil
	case refreshTickMsg:
		if m.refreshInFlight {
			return m, m.tick()
		}
		m.refreshInFlight = true
		return m, tea.Batch(m.refresh(), m.tick())
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
	m.clampCursor()
}

func attentionRank(agent capabilities.Agent) int {
	if agent.State == "stopped" {
		if !agent.Seen {
			return 0
		}
		return 2
	}
	if agent.State == "working" {
		return 1
	}
	return 1
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

// View renders the agent navigator using the place picker's theme vocabulary.
func (m Model) View() string {
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

func (m Model) marker(agent capabilities.Agent) string {
	marker := "●"
	if !m.theme.Color {
		return marker
	}
	color := "3"
	if agent.State == "stopped" && !agent.Seen {
		color = "1"
	} else if agent.State == "stopped" && agent.Seen {
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

func rowText(agent capabilities.Agent, now time.Time) string {
	container, window, location := targetParts(agent)
	parts := []string{agent.Harness}
	for _, value := range []string{container, window, location} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	if agent.Branch != "" {
		parts = append(parts, "⎇ "+agent.Branch)
	}
	text := strings.Join(parts, " · ")
	if agent.State == "stopped" && agent.StoppedSince != nil {
		text += "  " + age(now, *agent.StoppedSince)
	}
	return text
}

func targetParts(agent capabilities.Agent) (container, window, location string) {
	target := agent.Target
	colon := strings.LastIndex(target, ":")
	if colon < 0 {
		return "", "", ":" + target
	}
	container = target[:colon]
	remainder := target[colon+1:]
	window = remainder
	if dot := strings.LastIndex(remainder, "."); dot >= 0 {
		window = remainder[:dot]
	}
	return container, window, ":" + remainder
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

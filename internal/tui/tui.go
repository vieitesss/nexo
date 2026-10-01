// Package tui implements nexo's tabbed terminal interface.
//
// The package deliberately owns the screen model rather than making callers
// coordinate three independent pickers. Its public seam is Run: callers
// supply data and the few actions that can leave the screen, while the model
// keeps search, folding, scrolling and identity-stable selection local.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
)

// Tab is the initially selected tab and the identity of one tab in the model.
type Tab string

const (
	AgentsTab     Tab = "agents"
	ContainersTab Tab = "containers"
	ProjectsTab   Tab = "projects"
)

type AgentRef struct {
	Agent   capabilities.Agent
	Machine string
	Backend string
}

// Deps is the deliberately small seam between the TUI and command wiring.
// Places and agents are snapshots; optional commands keep slow discovery out
// of Update and let a caller retain its backend-specific command runner.
type Deps struct {
	Backend string
	// Theme is auto, dark, or light. An empty value is treated as auto.
	Theme      string
	Projects   []picker.Place
	Containers []capabilities.Container
	Agents     []capabilities.Agent

	ContainerSource      capabilities.ContainerLister
	AgentSource          capabilities.AgentSource
	AgentRefreshInterval time.Duration
	GitLoader            picker.GitLoader
	RemoteCommands       []tea.Cmd
	RemoteMachines       []string

	OpenProject     func(context.Context, picker.Place) error
	OpenContainer   func(context.Context, picker.Place) error
	FocusAgent      func(context.Context, AgentRef) error
	KillContainers  func(context.Context, []picker.ContainerRef) error
	RenameContainer func(context.Context, picker.ContainerRef, string) error

	Input      io.Reader
	Output     io.Writer
	Now        func() time.Time
	AgentError error
}

// Run starts the full-screen TUI and executes the accepted action through the
// callbacks in deps. It never creates or controls a multiplexer itself.
func Run(ctx context.Context, deps Deps, tab Tab) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !validTab(tab) {
		tab = defaultTab(deps.Agents)
	}
	configureTheme(deps.Theme)
	model := NewModel(ctx, deps, tab)
	input := deps.Input
	if input == nil {
		input = os.Stdin
	}
	output := deps.Output
	if output == nil {
		output = os.Stdout
	}
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen(), tea.WithMouseCellMotion())
	result, err := program.Run()
	model.cancelBackgroundLoads()
	if err != nil {
		return fmt.Errorf("run tui: %w", err)
	}
	final, ok := result.(Model)
	if !ok {
		return errors.New("tui returned an invalid model")
	}
	return final.execute(ctx)
}

func gitConcurrency(loader picker.GitLoader) int {
	if configured, ok := loader.(interface{ GitConcurrency() int }); ok && configured.GitConcurrency() > 0 {
		return configured.GitConcurrency()
	}
	return 4
}

func validTab(tab Tab) bool {
	return tab == AgentsTab || tab == ContainersTab || tab == ProjectsTab
}

// DefaultTab applies the no-argument opening rule to an initial agent snapshot.
func DefaultTab(agents []capabilities.Agent) Tab { return defaultTab(agents) }

func defaultTab(agents []capabilities.Agent) Tab {
	for _, agent := range agents {
		if strings.EqualFold(strings.TrimSpace(agent.State), "stopped") && !agent.Seen {
			return AgentsTab
		}
	}
	return ContainersTab
}

type rowKind uint8

const (
	machineRow rowKind = iota
	groupRow
	itemRow
	newContainerRow
	separatorRow
)

type row struct {
	id           string
	kind         rowKind
	itemKind     string
	label        string
	machine      string
	group        string
	depth        int
	count        int
	cursorable   bool
	matched      bool
	score        int
	place        picker.Place
	agent        capabilities.Agent
	agentMachine string
	agentBackend string
	expanded     bool
	foldable     bool
	remoteErr    string
	remoteWarn   string
	loading      bool
	activity     *time.Time
	nameWidth    int
	treePrefix   string
}

type agentsMsg struct {
	generation int
	agents     []capabilities.Agent
	err        error
}

type containersMsg struct {
	generation int
	containers []capabilities.Container
	err        error
}

type remoteMsg struct {
	generation int
	batch      picker.RemoteBatch
}

type actionMsg struct {
	kind string
	err  error
}

type sourceGenerations struct {
	containers int
	agents     int
	remotes    int
}

type reloadState struct {
	containers int
	agents     int
	remotes    int
}

func (state reloadState) done() bool {
	return state.containers == 0 && state.agents == 0 && state.remotes == 0
}

type agentRecord struct {
	agent   capabilities.Agent
	machine string
	backend string
}

type refreshTickMsg struct{}

// ToastMsg replaces footer hints with a short status message.
type ToastMsg struct{ Text string }

// Model is the complete tabbed Bubble Tea model. Tests can drive it through
// Update and inspect View without starting a terminal or a multiplexer.
type Model struct {
	ctx     context.Context
	deps    Deps
	catalog *picker.Catalog
	tab     Tab
	query   []rune
	rows    []row
	cursor  string

	width, height   int
	sized           bool
	forceFirst      bool
	folded          map[string]bool
	expanded        map[string]bool
	marked          map[picker.ContainerRef]bool
	remoteMachines  []string
	remoteLoaded    map[string]bool
	groupErrors     map[string]string
	groupWarnings   map[string]string
	agents          []agentRecord
	remoteAgents    []agentRecord
	gitErrors       map[string]string
	refreshInFlight *bool
	refreshErr      error
	generations     *sourceGenerations
	pendingReload   *reloadState
	gitSlots        chan struct{}
	cancelLoads     context.CancelFunc

	confirmKill      bool
	killTargets      []picker.ContainerRef
	renameMode       bool
	renameInput      []rune
	renameTarget     picker.ContainerRef
	actionInFlight   bool
	selectedPlace    *picker.Place
	selectedAgent    *capabilities.Agent
	selectedAgentRef AgentRef
	cancelled        bool
	toast            string
	now              func() time.Time
}

// NewModel creates a model with the requested opening tab. It does not do I/O.
func NewModel(ctx context.Context, deps Deps, tab Tab) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	loadCtx, cancelLoads := context.WithCancel(ctx)
	if !validTab(tab) {
		tab = defaultTab(deps.Agents)
	}
	if deps.AgentRefreshInterval <= 0 {
		deps.AgentRefreshInterval = 2 * time.Second
	}
	deps.Projects = append([]picker.Place(nil), deps.Projects...)
	deps.Containers = append([]capabilities.Container(nil), deps.Containers...)
	deps.Agents = append([]capabilities.Agent(nil), deps.Agents...)
	remoteMachines := make([]string, 0, len(deps.RemoteMachines))
	seenRemoteMachines := make(map[string]bool, len(deps.RemoteMachines))
	for _, machine := range deps.RemoteMachines {
		machine = picker.NormalizeMachine(machine)
		if machine == "" || machine == picker.LocalMachine() || seenRemoteMachines[machine] {
			continue
		}
		seenRemoteMachines[machine] = true
		remoteMachines = append(remoteMachines, machine)
	}
	deps.RemoteMachines = remoteMachines
	catalog := picker.NewCatalog(deps.Projects, deps.Containers, deps.Agents)
	catalog.SetRemoteMachines(remoteMachines)
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	m := Model{
		ctx: loadCtx, cancelLoads: cancelLoads, deps: deps, catalog: catalog, tab: tab, agents: newAgentRecords(deps.Agents, ""),
		folded: make(map[string]bool), expanded: make(map[string]bool), marked: make(map[picker.ContainerRef]bool),
		groupErrors: make(map[string]string), groupWarnings: make(map[string]string), gitErrors: make(map[string]string), now: now, refreshErr: deps.AgentError,
		remoteMachines: append([]string(nil), deps.RemoteMachines...), remoteLoaded: make(map[string]bool), refreshInFlight: new(bool), generations: &sourceGenerations{}, gitSlots: make(chan struct{}, gitConcurrency(deps.GitLoader)),
	}
	for _, machine := range remoteMachines {
		m.remoteLoaded[machine] = false
	}
	m.syncCatalog()
	m.rebuild(false)
	return m
}

// Tab reports the currently visible tab.
func (m Model) Tab() Tab { return m.tab }

// Query reports the current search query.
func (m Model) Query() string { return string(m.query) }

// Rows returns a compact copy of the visible row identities for model tests.
func (m Model) Rows() []string {
	rows := make([]string, 0, len(m.rows))
	for _, row := range m.rows {
		rows = append(rows, row.id)
	}
	return rows
}

// Cursor reports the stable identity under the cursor.
func (m Model) Cursor() string { return m.cursor }

// Marked returns marked container IDs in visible source order.
func (m Model) Marked() []string {
	refs := m.markedTargets()
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	return ids
}

// Cancelled reports whether the model closed without an action.
func (m Model) Cancelled() bool { return m.cancelled }

// SelectedPlace reports an accepted project or container.
func (m Model) SelectedPlace() (picker.Place, bool) {
	if m.selectedPlace == nil {
		return picker.Place{}, false
	}
	return *m.selectedPlace, true
}

// SelectedAgent reports an accepted agent pane.
func (m Model) SelectedAgent() (capabilities.Agent, bool) {
	if m.selectedAgent == nil {
		return capabilities.Agent{}, false
	}
	return *m.selectedAgent, true
}

func (m Model) execute(ctx context.Context) error {
	if m.cancelled {
		return nil
	}
	if place, ok := m.SelectedPlace(); ok {
		if place.OpenAction != nil {
			return place.OpenAction(ctx, place)
		}
		if place.Kind == picker.ProjectKind {
			if m.deps.OpenProject == nil {
				return errors.New("project opening is not supported")
			}
			return m.deps.OpenProject(ctx, place)
		}
		if place.Kind == picker.ContainerKind {
			if m.deps.OpenContainer == nil {
				return errors.New("container opening is not supported")
			}
			return m.deps.OpenContainer(ctx, place)
		}
	}
	if _, ok := m.SelectedAgent(); ok {
		if m.deps.FocusAgent == nil {
			return errors.New("agent focusing is not supported")
		}
		return m.deps.FocusAgent(ctx, m.selectedAgentRef)
	}
	return nil
}

func (m *Model) SetSize(width, height int) {
	m.width, m.height, m.sized = width, height, true
	m.rebuild(false)
}

// SetToast displays a status message in place of the normal footer hints.
func (m *Model) SetToast(text string) { m.toast = text }

// SetQuery is useful to embedding callers and model tests. Entering a query
// selects its best match; refining one keeps the current identity when it
// still matches. Query changes do not alter the saved fold state.
func (m *Model) SetQuery(query string) {
	wasEmpty := len(m.query) == 0
	m.query = []rune(query)
	m.forceFirst = wasEmpty && len(m.query) > 0
	m.rebuild(false)
}

func (m *Model) rebuild(force bool) {
	if force {
		m.forceFirst = true
	}
	old := m.cursor
	oldIndex := -1
	for index, candidate := range m.rows {
		if candidate.id == old {
			oldIndex = index
			break
		}
	}
	projection := m.projection()
	m.rows = projection.Rows(m.tab)
	if projection.query != "" && projection.MatchCount(m.tab) == 0 {
		for _, tab := range []Tab{AgentsTab, ContainersTab, ProjectsTab} {
			if tab == m.tab || projection.MatchCount(tab) == 0 {
				continue
			}
			m.tab = tab
			m.rows = projection.Rows(tab)
			break
		}
	}
	if m.forceFirst {
		for _, row := range m.rows {
			if row.kind == itemRow && row.matched {
				m.cursor, m.forceFirst = row.id, false
				return
			}
		}
		m.forceFirst = false
	}
	for _, row := range m.rows {
		if row.cursorable && row.id == old {
			m.cursor = old
			return
		}
	}
	if oldIndex >= 0 {
		if nearest, ok := nearestCursorable(m.rows, oldIndex); ok {
			m.cursor = nearest
			return
		}
	}
	for _, row := range m.rows {
		if row.kind == itemRow && row.cursorable {
			m.cursor = row.id
			return
		}
	}
	for _, row := range m.rows {
		if row.cursorable {
			m.cursor = row.id
			return
		}
	}
	m.cursor = ""
}

func nearestCursorable(rows []row, index int) (string, bool) {
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
			if candidate < 0 || candidate >= len(rows) || !rows[candidate].cursorable {
				continue
			}
			return rows[candidate].id, true
		}
	}
	return "", false
}

// Update implements tea.Model. Printable keys are intentionally handled last
// and always become search input; there are no printable action shortcuts.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil
	case agentsMsg:
		if msg.generation != m.agentsGeneration() {
			return m, nil
		}
		if m.refreshInFlight != nil {
			*m.refreshInFlight = false
		}
		if msg.err != nil {
			m.refreshErr = msg.err
		} else {
			m.refreshErr = nil
			m.agents = newAgentRecords(msg.agents, "")
			if m.catalog != nil {
				m.catalog.ReplaceLocalAgents(msg.agents)
				m.syncCatalog()
			}
			m.rebuild(false)
		}
		m.finishReload("agents")
		return m, nil
	case containersMsg:
		if msg.generation != m.containersGeneration() {
			return m, nil
		}
		if msg.err != nil {
			m.toast = "container refresh: " + msg.err.Error()
		} else {
			m.deps.Containers = append([]capabilities.Container(nil), msg.containers...)
			if m.catalog != nil {
				m.catalog.ReplaceContainers(msg.containers)
				m.syncCatalog()
			}
			m.rebuild(false)
		}
		m.finishReload("containers")
		return m, nil
	case actionMsg:
		if msg.err != nil {
			m.toast = msg.err.Error()
			return m, m.reload()
		}
		if msg.kind == "kill" {
			m.toast = "containers killed"
			m.marked = make(map[picker.ContainerRef]bool)
		} else {
			m.toast = "container renamed"
		}
		return m, m.reload()
	case picker.AgentBatch:
		// Keep the existing picker command seam compatible while callers move
		// to AgentSource. A batch without machine provenance is local.
		if msg.Error == "" {
			m.agents = newAgentRecords(msg.Agents, "")
			if m.catalog != nil {
				m.catalog.ReplaceLocalAgents(msg.Agents)
				m.syncCatalog()
			}
			m.rebuild(false)
		}
		return m, nil
	case refreshTickMsg:
		if m.deps.AgentSource == nil {
			return m, nil
		}
		if m.pendingReload != nil || (m.refreshInFlight != nil && *m.refreshInFlight) {
			return m, m.tick()
		}
		if m.refreshInFlight == nil {
			m.refreshInFlight = new(bool)
		}
		if m.generations == nil {
			m.generations = &sourceGenerations{}
		}
		m.generations.agents++
		*m.refreshInFlight = true
		return m, tea.Batch(m.refreshAgentsAt(m.generations.agents), m.tick())
	case picker.GitContextMsg:
		m.applyGit(msg)
		return m, nil
	case remoteMsg:
		if msg.generation != m.remoteGeneration() {
			return m, nil
		}
		m.applyRemote(msg.batch)
		m.finishReload("remotes")
		return m, nil
	case picker.RemoteBatch:
		m.applyRemote(msg)
		m.finishReload("remotes")
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	case tea.MouseMsg:
		return m.mouse(msg)
	case ToastMsg:
		m.toast = msg.Text
		return m, nil
	}
	return m, nil
}

func (m *Model) SetAgents(agents []capabilities.Agent) {
	m.agents = newAgentRecords(agents, "")
	if m.catalog != nil {
		m.catalog.ReplaceLocalAgents(agents)
		m.syncCatalog()
	}
	m.rebuild(false)
}

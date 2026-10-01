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
	"github.com/vieitesss/nexo/internal/startup"
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
	// ProjectSource is started after the first frame. A nil source means the
	// Projects snapshot is already complete.
	ProjectSource func(context.Context) ([]picker.Place, error)
	// AutoTab applies the no-argument opening rule once the initial agent
	// snapshot arrives. Explicit tabs never change when sources refresh.
	AutoTab bool
	// StartupTrace is opt-in timing instrumentation; normal runs leave it nil.
	StartupTrace *startup.Trace
	// themeAsync is set only by Run; tests and embedders can keep theme setup
	// deterministic while the real program probes after its first frame.
	themeAsync bool

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
	if strings.EqualFold(strings.TrimSpace(deps.Theme), "auto") || strings.TrimSpace(deps.Theme) == "" {
		// Theme probing may wait for a terminal response or a multiplexer
		// round-trip. Start with the stable dark palette and correct it after
		// the first frame so probing never gates startup.
		configureTheme("dark")
		deps.themeAsync = true
	} else {
		configureTheme(deps.Theme)
	}
	if deps.StartupTrace == nil {
		deps.StartupTrace = startup.New("tui", nil)
	}
	deps.StartupTrace.MarkOnce("start")
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

type projectsMsg struct {
	generation int
	projects   []picker.Place
	err        error
}

type agentsMsg struct {
	generation int
	agents     []capabilities.Agent
	err        error
}

type themeMsg struct{ dark bool }

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
	deps    Deps
	catalog *picker.Catalog
	rows    []row

	width, height int
	sized         bool
	gitErrors     map[string]string
	source        *sourceState
	input         *inputState
	now           func() time.Time
	toast         string
	autoTab       bool
	trace         *startup.Trace
	themeAsync    bool
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
	source := newSource(loadCtx, catalog, deps, cancelLoads, remoteMachines)
	source.syncCatalog()
	m := Model{
		deps: deps, catalog: catalog, source: source,
		input:     newInputState(tab, deps, loadCtx),
		gitErrors: make(map[string]string), now: now,
		autoTab: deps.AutoTab, trace: deps.StartupTrace, themeAsync: deps.themeAsync,
	}
	m.rebuild(false)
	return m
}

// Tab reports the currently visible tab.
func (m Model) Tab() Tab { return m.input.tab }

// Query reports the current search query.
func (m Model) Query() string { return string(m.input.query) }

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

// SelectedPlace reports an accepted project or container.
func (m Model) SelectedPlace() (picker.Place, bool) {
	if m.input.selectedPlace == nil {
		return picker.Place{}, false
	}
	return *m.input.selectedPlace, true
}

// SelectedAgent reports an accepted agent pane.
func (m Model) SelectedAgent() (capabilities.Agent, bool) {
	if m.input.selectedAgent == nil {
		return capabilities.Agent{}, false
	}
	return *m.input.selectedAgent, true
}

func (m Model) execute(ctx context.Context) error {
	if m.input.cancelled {
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
		return m.deps.FocusAgent(ctx, m.input.selectedAgentRef)
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
	m.input.setQuery(query)
	m.rebuild(false)
}

func (m *Model) rebuild(force bool) {
	if force {
		m.input.forceFirst = true
	}
	oldRows := m.rows
	projection := m.projection()
	rows := projection.Rows(m.input.tab)
	if projection.query != "" && projection.MatchCount(m.input.tab) == 0 {
		for _, tab := range []Tab{AgentsTab, ContainersTab, ProjectsTab} {
			if tab == m.input.tab || projection.MatchCount(tab) == 0 {
				continue
			}
			m.input.tab = tab
			rows = projection.Rows(tab)
			break
		}
	}
	m.rows = rows
	m.input.reconcileSelection(oldRows, rows)
}

// Init starts source commands; all later source results cross source.Accept.
func (m Model) Init() tea.Cmd {
	if m.source == nil {
		return nil
	}
	cmd := m.source.Init(m.projection().projectPlaces())
	if !m.themeAsync {
		return cmd
	}
	return tea.Batch(cmd, detectThemeCmd(m.trace))
}

func (m *Model) cancelBackgroundLoads() {
	if m.source != nil {
		m.source.cancel()
	}
}

// Update implements tea.Model. Printable keys are intentionally handled last
// and always become search input; there are no printable action shortcuts.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil
	case actionMsg:
		if msg.err != nil {
			m.toast = msg.err.Error()
			m.source.remoteCommands = append([]tea.Cmd(nil), m.deps.RemoteCommands...)
			cmd := m.source.Reload()
			if !m.source.pending() {
				m.input.actionInFlight = false
			}
			m.rebuild(false)
			return m, cmd
		}
		if msg.kind == "kill" {
			m.toast = "containers killed"
			m.input.marked = make(map[picker.ContainerRef]bool)
		} else {
			m.toast = "container renamed"
		}
		m.source.remoteCommands = append([]tea.Cmd(nil), m.deps.RemoteCommands...)
		cmd := m.source.Reload()
		if !m.source.pending() {
			m.input.actionInFlight = false
		}
		m.rebuild(false)
		return m, cmd
	case refreshTickMsg:
		return m, m.source.Tick()
	case picker.GitContextMsg:
		m.applyGit(msg)
		return m, nil
	case tea.KeyMsg:
		m.autoTab = false
		m.input.deps = m.deps
		effect := m.input.key(msg, m.keyFrame())
		if effect.rebuild {
			m.rebuild(false)
		}
		if effect.quit {
			m.cancelBackgroundLoads()
			return m, tea.Quit
		}
		return m, effect.cmd
	case tea.MouseMsg:
		m.autoTab = false
		m.input.deps = m.deps
		effect := m.input.mouse(msg, m.mouseFrame())
		if effect.rebuild {
			m.rebuild(false)
		}
		if effect.quit {
			m.cancelBackgroundLoads()
			return m, tea.Quit
		}
		return m, effect.cmd
	case ToastMsg:
		m.toast = msg.Text
		return m, nil
	case themeMsg:
		if msg.dark {
			configureTheme("dark")
		} else {
			configureTheme("light")
		}
		return m, nil
	}
	if projectResult, ok := msg.(projectsMsg); ok && projectResult.err == nil {
		m.deps.Projects = append([]picker.Place(nil), projectResult.projects...)
	}
	if update, ok := m.source.Accept(msg); ok {
		if update.toast != "" {
			m.toast = update.toast
		}
		if update.reloadDone {
			m.input.actionInFlight = false
		}
		if _, initialAgents := msg.(agentsMsg); initialAgents && m.autoTab {
			m.input.tab = defaultTab(m.source.agentValues())
			m.autoTab = false
		}
		if update.changed {
			m.rebuild(false)
		}
		return m, update.cmd
	}
	return m, nil
}

// keyFrame supplies keyboard transitions with rows, height, and a lazy marked
// lookup. Blocked and navigation-only keys therefore never materialize the
// catalog snapshot a full presentation frame would build.
func (m Model) keyFrame() inputFrame {
	return inputFrame{
		rows:   m.rows,
		height: m.height,
		marked: func() []picker.ContainerRef { return m.input.markedTargetsCatalog(m.catalog) },
	}
}

// mouseFrame supplies pointer transitions with rows and a lazy layout frame.
// The layout is built once, only for a click that reaches hit testing.
func (m Model) mouseFrame() inputFrame {
	return inputFrame{rows: m.rows, render: m.frame}
}

func (m *Model) applyGit(msg picker.GitContextMsg) {
	if msg.Err != nil {
		m.gitErrors[msg.Path] = msg.Err.Error()
		m.rebuild(false)
		return
	}
	delete(m.gitErrors, msg.Path)
	selectedPath := ""
	if current, ok := m.input.currentItem(m.rows); ok && current.place.Kind == picker.ProjectKind {
		selectedPath = current.place.Path
	}
	value := msg.Context
	for i := range m.deps.Projects {
		if m.deps.Projects[i].Path != msg.Path {
			continue
		}
		m.deps.Projects[i].GitContext = &value
		m.deps.Projects[i].ParentPath = value.PrimaryPath
		if value.PrimaryPath != "" {
			m.deps.Projects[i].LinkedWorktree = true
		}
	}
	for i := range m.deps.Projects {
		if m.deps.Projects[i].ParentPath == "" {
			continue
		}
		for _, primary := range m.deps.Projects {
			if primary.Path == m.deps.Projects[i].ParentPath {
				m.deps.Projects[i].Group = primary.Group
				break
			}
		}
	}
	if selectedPath == msg.Path {
		m.input.revealProjectAncestors(m.deps.Projects, msg.Path)
	}
	if m.catalog != nil {
		m.catalog.SetProjects(m.deps.Projects)
	}
	m.rebuild(false)
}

func (m *Model) SetAgents(agents []capabilities.Agent) {
	m.source.setAgents(agents)
	m.rebuild(false)
}

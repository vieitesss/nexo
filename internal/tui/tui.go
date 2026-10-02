// Package tui implements nexo's tabbed terminal interface.
//
// The package deliberately owns the screen model rather than making callers
// coordinate three independent pickers. Its public seam is Run: callers
// supply data and the few actions that can leave the screen, while the model
// keeps search, folding, scrolling and identity-stable selection local.
package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	cloneflow "github.com/vieitesss/nexo/internal/clone"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/deletion"
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

// RemoteTarget is a reachable remote machine plus the multiplexer kind its
// listing resolved. The new-container row carries it so an action never probes
// the machine again.
type RemoteTarget struct {
	Machine string
	Backend string
}

// Deps is the deliberately small seam between the TUI and command wiring.
// Places and agents are snapshots; optional commands keep slow discovery out
// of Update and let a caller retain its backend-specific command runner.
type Deps struct {
	// Theme is auto, dark, or light. An empty value is treated as auto.
	Theme      string
	Projects   []picker.Place
	Containers []capabilities.Container
	Agents     []capabilities.Agent
	// ConfigPath is the config file a running TUI watches for [tui].theme
	// changes. An empty path disables the watch.
	ConfigPath string
	// ThemeReloadInterval overrides the ~1s theme poll. Zero uses the default.
	ThemeReloadInterval time.Duration
	// ThemeStat and ThemeRead are the file seams for the theme watch. Nil uses
	// os.Stat and os.ReadFile.
	ThemeStat func(string) (os.FileInfo, error)
	ThemeRead func(string) ([]byte, error)
	// ProjectSource is started after the first frame. A nil source means the
	// Projects snapshot is already complete.
	ProjectSource   func(context.Context) ([]picker.Place, error)
	ProjectWarnings func() []string
	// AutoTab applies the no-argument opening rule once the initial agent
	// snapshot arrives. Explicit tabs never change when sources refresh.
	AutoTab bool
	// StartupTrace is opt-in timing instrumentation; normal runs leave it nil.
	StartupTrace         *startup.Trace
	ContainerSource      capabilities.ContainerLister
	AgentSource          capabilities.AgentSource
	AgentRefreshInterval time.Duration
	GitLoader            picker.GitLoader
	PullRequestLoader    picker.PullRequestLoader
	// PullRequestRefreshInterval overrides how often the TUI checks visible
	// checkouts for an expired pull request answer. Zero uses the default.
	PullRequestRefreshInterval time.Duration
	RemoteCommands             []tea.Cmd
	RemoteMachines             []string

	OpenProject   func(context.Context, picker.Place) error
	OpenContainer func(context.Context, picker.Place) error
	FocusAgent    func(context.Context, AgentRef) error
	// CloneProject is the backend-neutral clone transaction used by the
	// Projects-tab staged form. It must not open or focus a container.
	CloneProject func(context.Context, cloneflow.Request) error
	// CreateDirectory is the backend-neutral filesystem action behind the
	// Projects-tab directory form. It must not open or focus a container.
	CreateDirectory func(context.Context, string) error
	// ExtractWorktree is the shared worktree operation used by the Projects-tab
	// staged form. The backend owns native preflight, creation, opening and
	// rollback while the operation owns project-path registration.
	ExtractWorktree     func(context.Context, string, string, string) (capabilities.Worktree, error)
	ExtractReturnBranch func(context.Context, string) (string, error)
	// DeleteFingerprint captures selection identity before the confirmation UI.
	DeleteFingerprint func(context.Context, string) (string, error)
	// DeletePreflight gathers the complete read-only plan before confirmation.
	DeletePreflight func(context.Context, string, string) (deletion.Plan, error)
	// DeleteProjectPlan executes a previously displayed plan without replacing
	// its identity or silently re-running policy on a different pathname.
	DeleteProjectPlan func(context.Context, deletion.Plan, bool, bool) error
	AddProjectPath    func(context.Context, string) error
	// NewRemoteContainer creates and focuses a container on a reachable
	// remote machine using the backend its listing resolved.
	NewRemoteContainer func(context.Context, RemoteTarget, string) error
	CloneConfig        config.CloneConfig
	ProjectPaths       []string
	Home               string
	KillContainers     func(context.Context, []picker.ContainerRef) error
	RenameContainer    func(context.Context, picker.ContainerRef, string) error

	Input  io.Reader
	Output io.Writer
	Now    func() time.Time
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
	var bufferedTrace bytes.Buffer
	if deps.StartupTrace == nil {
		deps.StartupTrace = startup.New("tui", &bufferedTrace)
		model.trace = deps.StartupTrace
		model.source.trace = deps.StartupTrace
	}
	deps.StartupTrace.MarkOnce("start")
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen(), tea.WithMouseCellMotion())
	result, err := program.Run()
	model.cancelBackgroundLoads()
	if bufferedTrace.Len() > 0 {
		_, _ = output.Write(bufferedTrace.Bytes())
	}
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

func pullRequestConcurrency(loader picker.PullRequestLoader) int {
	if configured, ok := loader.(interface{ PullRequestConcurrency() int }); ok && configured.PullRequestConcurrency() > 0 {
		return configured.PullRequestConcurrency()
	}
	return picker.DefaultPullRequestConcurrency
}

// defaultPullRequestRefreshInterval is the revalidation check cadence. The
// loader's freshness window decides when a check actually asks gh, so this is
// only how often the TUI looks for expired answers.
const defaultPullRequestRefreshInterval = 5 * time.Second

func pullRequestRefreshInterval(configured time.Duration) time.Duration {
	if configured <= 0 {
		return defaultPullRequestRefreshInterval
	}
	return configured
}

func pullRequestFreshness(loader picker.PullRequestLoader) time.Duration {
	if configured, ok := loader.(interface{ PullRequestFreshness() time.Duration }); ok && configured.PullRequestFreshness() > 0 {
		return configured.PullRequestFreshness()
	}
	return picker.DefaultPullRequestFreshness
}

func validTab(tab Tab) bool {
	return tab == AgentsTab || tab == ContainersTab || tab == ProjectsTab
}

func defaultTab(agents []capabilities.Agent) Tab {
	for _, agent := range agents {
		if picker.AttentionRank(agent) == 0 {
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

// newContainerLabel is the action row shown under every reachable remote
// machine.
const newContainerLabel = "+ new container…"

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
	backend      string
}

type projectsMsg struct {
	generation int
	projects   []picker.Place
	warnings   []string
	err        error
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

// remoteNewMsg carries a validated name and its form generation from the
// remote new-container form to the coordinator, which closes the TUI and runs
// the accepted action. The generation lets a late result from a canceled or
// replaced form be rejected.
type remoteNewMsg struct {
	target     RemoteTarget
	name       string
	generation uint64
}

type pullRequestMsg struct {
	generation  int
	path        string
	pullRequest picker.PullRequest
	err         error
}

type actionMsg struct {
	kind        string
	destination string
	generation  uint64
	plan        *deletion.Plan
	err         error
}

type agentRecord struct {
	agent   capabilities.Agent
	machine string
	backend string
}

type refreshTickMsg struct{}

type pullRequestRefreshMsg struct{}

// ToastMsg replaces footer hints with a short status message.
type ToastMsg struct{ Text string }

// Model is the complete tabbed Bubble Tea model. Tests can drive it through
// Update and inspect View without starting a terminal or a multiplexer.
type Model struct {
	deps    Deps
	catalog *picker.Catalog
	rows    []row

	width, height           int
	sized                   bool
	gitErrors               map[string]string
	source                  *sourceState
	input                   *inputState
	now                     func() time.Time
	toast                   string
	theme                   *themeWatcher
	autoTab                 bool
	trace                   *startup.Trace
	initialSelectionPending bool
	userInteracted          bool
	selectAfterRefresh      string
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
	deps.CloneConfig = deps.CloneConfig.WithDefaults()
	deps.ProjectPaths = append([]string(nil), deps.ProjectPaths...)
	deps.Agents = append([]capabilities.Agent(nil), deps.Agents...)
	remoteMachines := make([]string, 0, len(deps.RemoteMachines))
	seenRemoteMachines := make(map[string]bool, len(deps.RemoteMachines))
	for _, machine := range deps.RemoteMachines {
		machine = picker.NormalizeMachine(machine)
		if machine == "" || machine == containerstate.LocalMachine() || seenRemoteMachines[machine] {
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
	deps.Now = now
	source := newSource(loadCtx, catalog, deps, cancelLoads, remoteMachines)
	source.syncCatalog()
	m := Model{
		deps: deps, catalog: catalog, source: source,
		input:     newInputState(tab, deps, loadCtx),
		gitErrors: make(map[string]string), now: now,
		theme:   newThemeWatcher(deps.ConfigPath, deps.Theme, deps),
		autoTab: deps.AutoTab, trace: deps.StartupTrace, initialSelectionPending: true,
	}
	m.rebuild(false)
	m.initialSelectionPending = !hasItemRows(m.rows)
	return m
}

func projectPathIdentity(path string) string {
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		absolute = filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute)
}

func hasItemRows(rows []row) bool {
	for _, candidate := range rows {
		if candidate.cursorable && (candidate.kind == itemRow || candidate.kind == newContainerRow) {
			return true
		}
	}
	return false
}

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
	if m.input.selectedRemoteTarget != nil {
		if m.deps.NewRemoteContainer == nil {
			return errors.New("remote container creation is not supported")
		}
		return m.deps.NewRemoteContainer(ctx, *m.input.selectedRemoteTarget, m.input.selectedRemoteName)
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
	m.rebuildWithTabPolicy(false, !m.userInteracted)
}

// SetQuery is useful to embedding callers and model tests. Entering a query
// selects its best match; refining one keeps the current identity when it
// still matches. Query changes do not alter the saved fold state.
func (m *Model) SetQuery(query string) {
	m.userInteracted = true
	m.input.setQuery(query)
	m.rebuild(false)
}

func (m *Model) rebuild(force bool) {
	m.rebuildWithTabPolicy(force, true)
}

func (m *Model) rebuildSource() {
	m.rebuildWithTabPolicy(false, !m.userInteracted)
	if m.initialSelectionPending && !m.input.cursorMoved {
		m.input.forceFirst = true
		m.rebuildWithTabPolicy(false, !m.userInteracted)
		if !m.input.forceFirst {
			m.initialSelectionPending = false
		}
	}
}

func (m *Model) rebuildWithTabPolicy(force bool, allowQueryTab bool) {
	if force {
		m.input.forceFirst = true
	}
	oldRows := m.rows
	projection := m.projection()
	rows := projection.Rows(m.input.tab)
	if allowQueryTab && projection.query != "" && projection.MatchCount(m.input.tab) == 0 {
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

// Init starts source and theme-watch commands; all later source results cross
// source.Accept.
func (m Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, 2)
	if m.source != nil {
		cmds = append(cmds, m.source.Init(m.projection().projectPlaces()))
	}
	cmds = append(cmds, m.theme.Tick())
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
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
		if msg.kind == "delete-preflight" {
			if msg.generation != 0 && msg.generation != m.input.actionGeneration {
				return m, nil
			}
			if m.input.deleteForm != nil {
				m.input.deleteForm.finishAction(msg)
			}
			m.input.actionInFlight = false
			m.rebuild(false)
			var focus tea.Cmd
			if m.input.form != nil {
				focus = m.input.form.focusCurrent()
			}
			return m, focus
		}
		if msg.kind == "clone" || msg.kind == "mkdir" || msg.kind == "extract" || msg.kind == "delete" {
			if msg.generation != 0 && msg.generation != m.input.actionGeneration {
				return m, nil
			}
			if m.input.form != nil {
				m.input.form.finish(msg.err)
			}
			if msg.err != nil {
				m.input.actionInFlight = false
				return m, nil
			}
			m.input.form = nil
			m.input.deleteForm = nil
			m.input.actionInFlight = false
			m.input.setQuery("")
			if msg.kind == "delete" {
				m.selectAfterRefresh = ""
			} else {
				m.selectAfterRefresh = projectPathIdentity(msg.destination)
			}
			cmd := m.source.ReloadProjects()
			if cmd == nil {
				m.selectAfterRefresh = ""
			}
			m.rebuild(false)
			return m, cmd
		}
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
	case pullRequestRefreshMsg:
		cmds := m.source.duePullRequestCommands(m.visibleProjectPlaces())
		if tick := m.source.pullRequestTick(); tick != nil {
			cmds = append(cmds, tick)
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)
	case themeTickMsg:
		return m, m.theme.Tick()
	case themeReloadMsg:
		if msg.theme != "" {
			configureTheme(msg.theme)
		}
		if msg.toast != "" {
			m.toast = msg.toast
		}
		return m, nil
	case picker.GitContextMsg:
		m.applyGit(msg)
		if msg.Err != nil {
			return m, nil
		}
		return m, m.source.duePullRequestCommand(msg.Path)
	case tea.KeyMsg:
		m.autoTab = false
		m.userInteracted = true
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
		m.userInteracted = true
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
	case remoteNewMsg:
		if m.input.form == nil || msg.generation == 0 || msg.generation != m.input.actionGeneration {
			return m, nil
		}
		target := msg.target
		m.input.selectedRemoteTarget = &target
		m.input.selectedRemoteName = msg.name
		m.input.form = nil
		m.cancelBackgroundLoads()
		return m, tea.Quit
	case stagedFormMsg:
		if m.input.form == nil || msg.generation != m.input.form.generation {
			return m, nil
		}
		effect := m.input.form.updateMsg(msg)
		if effect.Changed {
			m.rebuild(false)
		}
		return m, effect.Cmd
	}
	if update, ok := m.source.Accept(msg); ok {
		if _, projectError := msg.(projectsMsg); projectError && update.projects == nil {
			m.selectAfterRefresh = ""
		}
		if update.toast != "" {
			m.toast = update.toast
		}
		if update.reloadDone {
			m.input.actionInFlight = false
		}
		tabChanged := false
		if initialAgents, ok := msg.(agentsMsg); ok && m.autoTab {
			if initialAgents.err != nil {
				if m.input.tab != ContainersTab {
					m.input.tab = ContainersTab
					tabChanged = true
				}
			} else {
				selected := defaultTab(initialAgents.agents)
				if m.input.tab != selected {
					m.input.tab = selected
					tabChanged = true
				}
			}
			m.autoTab = false
		}
		if update.projectsUpdated {
			m.deps.Projects = append([]picker.Place(nil), update.projects...)
			if m.selectAfterRefresh != "" {
				m.input.revealProjectAncestors(update.projects, m.selectAfterRefresh)
			}
		}
		if update.pullRequest != nil {
			m.applyPullRequest(*update.pullRequest)
		}
		if update.changed || tabChanged {
			m.rebuildSource()
		}
		if update.projectsUpdated && m.selectAfterRefresh != "" {
			selected := false
			for _, value := range m.rows {
				if value.kind == itemRow && value.itemKind == "project" && projectPathIdentity(value.place.Path) == m.selectAfterRefresh {
					m.input.cursor = value.id
					selected = true
					break
				}
			}
			m.selectAfterRefresh = ""
			if !selected {
				m.input.cursor = ""
				m.input.reconcileSelection(nil, m.rows)
			}
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

// applyPullRequest decorates one project place with the accepted pull
// request result. A zero request, a failed command, and a missing candidate
// all clear the badge; failures never reach the footer.
func (m *Model) applyPullRequest(msg pullRequestMsg) {
	for i := range m.deps.Projects {
		if m.deps.Projects[i].Path != msg.path {
			continue
		}
		if msg.pullRequest.Number <= 0 || msg.pullRequest.State == "" {
			m.deps.Projects[i].PullRequest = nil
			continue
		}
		value := msg.pullRequest
		m.deps.Projects[i].PullRequest = &value
	}
	if m.catalog != nil {
		m.catalog.SetProjects(m.deps.Projects)
	}
}

// visibleProjectPlaces returns the project rows around the cursor, one
// viewport either side. Revalidation passes look only at what the user can
// reach, so a large catalog does not turn a tick into a query per row.
func (m Model) visibleProjectPlaces() []picker.Place {
	cursor := -1
	for index, candidate := range m.rows {
		if candidate.cursorable && candidate.id == m.input.cursor {
			cursor = index
			break
		}
	}
	if cursor < 0 {
		return nil
	}
	from, to := max(0, cursor-m.height), min(len(m.rows), cursor+m.height+1)
	places := make([]picker.Place, 0, to-from)
	seen := make(map[string]bool, to-from)
	for _, candidate := range m.rows[from:to] {
		if candidate.kind != itemRow || candidate.itemKind != "project" || !candidate.place.Git || candidate.place.Path == "" || seen[candidate.place.Path] {
			continue
		}
		seen[candidate.place.Path] = true
		places = append(places, candidate.place)
	}
	return places
}

func (m *Model) applyGit(msg picker.GitContextMsg) {
	if msg.Err != nil {
		m.gitErrors[msg.Path] = msg.Err.Error()
		m.rebuildSource()
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
	m.rebuildSource()
}

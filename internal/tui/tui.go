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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
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

// Init starts independent asynchronous loads. Slow loads never block key
// handling or the first frame.
func (m Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, 3+len(m.deps.RemoteCommands))
	if m.deps.AgentSource != nil {
		if m.refreshInFlight == nil {
			m.refreshInFlight = new(bool)
		}
		*m.refreshInFlight = true
		cmds = append(cmds, m.refreshAgents(), m.tick())
	}
	for _, command := range m.deps.RemoteCommands {
		cmds = append(cmds, m.remoteCommand(command, m.remoteGeneration()))
	}
	if m.deps.GitLoader != nil {
		seenGit := map[string]bool{}
		for _, place := range m.projectPlaces() {
			if !place.Git || place.Path == "" || seenGit[place.Path] {
				continue
			}
			seenGit[place.Path] = true
			place := place
			cmds = append(cmds, func() tea.Msg {
				select {
				case m.gitSlots <- struct{}{}:
					if m.ctx.Err() != nil {
						<-m.gitSlots
						return nil
					}
				case <-m.ctx.Done():
					return nil
				}
				defer func() { <-m.gitSlots }()
				value, err := m.deps.GitLoader.Load(m.ctx, place.Path)
				return picker.GitContextMsg{Path: place.Path, Context: value, Err: err}
			})
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) cancelBackgroundLoads() {
	if m.cancelLoads != nil {
		m.cancelLoads()
		m.cancelLoads = nil
	}
}

func (m Model) generationState() *sourceGenerations {
	if m.generations == nil {
		return &sourceGenerations{}
	}
	return m.generations
}

func (m Model) agentsGeneration() int     { return m.generationState().agents }
func (m Model) containersGeneration() int { return m.generationState().containers }
func (m Model) remoteGeneration() int     { return m.generationState().remotes }

func (m Model) refreshAgents() tea.Cmd {
	return m.refreshAgentsAt(m.agentsGeneration())
}

func (m Model) refreshAgentsAt(generation int) tea.Cmd {
	return func() tea.Msg {
		agents, err := m.deps.AgentSource.ListAgents(m.ctx)
		return agentsMsg{generation: generation, agents: agents, err: err}
	}
}

func (m Model) refreshContainersAt(generation int) tea.Cmd {
	return func() tea.Msg {
		containers, err := m.deps.ContainerSource.ListContainers(m.ctx)
		return containersMsg{generation: generation, containers: containers, err: err}
	}
}

func (m Model) remoteCommand(command tea.Cmd, generation int) tea.Cmd {
	return func() tea.Msg {
		msg := command()
		batch, ok := msg.(picker.RemoteBatch)
		if !ok {
			return msg
		}
		return remoteMsg{generation: generation, batch: batch}
	}
}

func (m *Model) reload() tea.Cmd {
	state := sourceGenerations{}
	if m.generations == nil {
		m.generations = &state
	}
	pending := &reloadState{}
	cmds := make([]tea.Cmd, 0, 2+len(m.deps.RemoteCommands))
	if m.deps.ContainerSource != nil {
		m.generations.containers++
		pending.containers = 1
		cmds = append(cmds, m.refreshContainersAt(m.generations.containers))
	}
	if m.deps.AgentSource != nil {
		m.generations.agents++
		pending.agents = 1
		if m.refreshInFlight == nil {
			m.refreshInFlight = new(bool)
		}
		*m.refreshInFlight = true
		cmds = append(cmds, m.refreshAgentsAt(m.generations.agents))
	}
	if len(m.deps.RemoteCommands) > 0 {
		m.generations.remotes++
		pending.remotes = len(m.deps.RemoteCommands)
		for machine := range m.remoteLoaded {
			m.remoteLoaded[machine] = false
			delete(m.groupErrors, machine)
			delete(m.groupWarnings, machine)
		}
		m.rebuild(false)
		for _, command := range m.deps.RemoteCommands {
			cmds = append(cmds, m.remoteCommand(command, m.generations.remotes))
		}
	}
	if pending.done() {
		m.pendingReload = nil
		m.actionInFlight = false
		return nil
	}
	m.pendingReload = pending
	return tea.Batch(cmds...)
}

func (m *Model) finishReload(source string) {
	if m.pendingReload == nil {
		return
	}
	switch source {
	case "containers":
		m.pendingReload.containers = 0
	case "agents":
		m.pendingReload.agents = 0
	case "remotes":
		if m.pendingReload.remotes > 0 {
			m.pendingReload.remotes--
		}
	}
	if m.pendingReload.done() {
		m.pendingReload = nil
		m.actionInFlight = false
	}
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.deps.AgentRefreshInterval, func(time.Time) tea.Msg { return refreshTickMsg{} })
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
	m.rows = m.rowsForTab(m.tab)
	if string(m.query) != "" && m.matchCount(m.tab) == 0 {
		for _, tab := range []Tab{AgentsTab, ContainersTab, ProjectsTab} {
			if tab == m.tab || m.matchCount(tab) == 0 {
				continue
			}
			m.tab = tab
			m.rows = m.rowsForTab(tab)
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

func (m Model) sourcePlaces() []picker.Place {
	if m.catalog == nil {
		return append([]picker.Place(nil), m.deps.Projects...)
	}
	return append([]picker.Place(nil), m.catalog.Snapshot().Places...)
}
func (m Model) projectPlaces() []picker.Place {
	places := m.sourcePlaces()
	out := places[:0]
	for _, place := range places {
		if place.Kind == picker.ProjectKind {
			out = append(out, place)
		}
	}
	return out
}
func (m Model) matchAgent(record agentRecord) (picker.Match, bool) {
	agent := record.agent
	return picker.Rank(string(m.query), strings.Join([]string{sanitize(agent.Harness), sanitize(agent.Container), sanitize(record.machine), sanitize(agent.Target), sanitize(agent.Branch)}, " "))
}

func (m Model) matchContainer(place picker.Place) (picker.Match, bool) {
	text := strings.Join([]string{sanitize(place.ContainerName), sanitize(place.Machine)}, " ")
	return picker.Rank(string(m.query), text)
}

func (m Model) matchProject(place picker.Place) (picker.Match, bool) {
	query := string(m.query)
	if query == "" {
		return picker.Match{}, true
	}

	// A project name is the strongest signal.  Ranking one combined string
	// lets a long checkout path beat an exact project name, which is especially
	// surprising for short queries.  Keep the fields separate and make that
	// precedence explicit instead.
	name := projectLabel(place)
	if match, ok := picker.Rank(query, name); ok {
		match.Score += 100000
		return match, true
	}
	if branch := gitIdentity(place); branch != "" {
		if match, ok := picker.Rank(query, sanitize(branch)); ok {
			match.Score += 50000
			return match, true
		}
	}
	path := sanitize(place.SearchText)
	if path == "" {
		path = sanitize(place.Path)
	}
	if match, ok := picker.Rank(query, path); ok {
		// A sparse match spanning a full path is noise.  Contiguous path
		// matches remain useful when the project name itself is unavailable.
		if match.Score < len([]rune(query))*10 {
			return picker.Match{}, false
		}
		return match, true
	}
	return picker.Match{}, false
}

func gitIdentity(place picker.Place) string {
	if place.GitContext == nil {
		return ""
	}
	return place.GitContext.Identity
}

func (m Model) matchCount(tab Tab) int {
	switch tab {
	case AgentsTab:
		n := 0
		for _, agent := range m.allAgents() {
			if _, ok := m.matchAgent(agent); ok {
				n++
			}
		}
		return n
	case ContainersTab:
		n := 0
		for _, place := range m.containerPlaces() {
			if _, ok := m.matchContainer(place); ok {
				n++
			}
		}
		return n
	default:
		n := 0
		for _, place := range m.projectPlaces() {
			if _, ok := m.matchProject(place); ok {
				n++
			}
		}
		return n
	}
}

func (m Model) containerPlaces() []picker.Place {
	places := m.sourcePlaces()
	out := places[:0]
	for _, place := range places {
		if place.Kind == picker.ContainerKind {
			out = append(out, place)
		}
	}
	return out
}

func (m Model) agentsFor(place picker.Place) []capabilities.Agent {
	if m.catalog == nil {
		return nil
	}
	return m.catalog.AgentsFor(place)
}

func (m Model) rowsForTab(tab Tab) []row {
	switch tab {
	case AgentsTab:
		return m.agentRows()
	case ContainersTab:
		return m.containerRows()
	default:
		return m.projectRows()
	}
}

func (m Model) agentRows() []row {
	rows := make([]row, 0)
	machines := m.machineNames()
	for _, machine := range machines {
		children := make([]row, 0)
		for _, record := range m.allAgents() {
			if record.machine != machine {
				continue
			}
			match, ok := m.matchAgent(record)
			if !ok {
				continue
			}
			agent := record.agent
			children = append(children, row{id: agentIdentity(record), kind: itemRow, itemKind: "agent", label: agent.Harness, machine: machine, group: machine, depth: 2, cursorable: true, matched: true, score: match.Score, agent: agent, agentMachine: record.machine, agentBackend: record.backend})
		}
		sort.SliceStable(children, func(i, j int) bool {
			ri, rj := attentionRank(children[i].agent), attentionRank(children[j].agent)
			if ri != rj {
				return ri < rj
			}
			if ri == 0 && children[i].agent.StoppedSince != nil && children[j].agent.StoppedSince != nil && !children[i].agent.StoppedSince.Equal(*children[j].agent.StoppedSince) {
				return children[i].agent.StoppedSince.Before(*children[j].agent.StoppedSince)
			}
			if string(m.query) != "" && children[i].score != children[j].score {
				return children[i].score > children[j].score
			}
			return children[i].id < children[j].id
		})
		remoteErr := m.groupErrors[machine]
		remoteWarn := m.groupWarnings[machine]
		isRemote := contains(m.remoteMachines, machine)
		_, machineMatch := picker.Rank(string(m.query), sanitize(machine))
		if len(children) == 0 && !machineMatch && remoteErr == "" && remoteWarn == "" && !isRemote && string(m.query) != "" {
			continue
		}
		rows = append(rows, row{id: "machine:" + machine, kind: machineRow, label: shortenMachine(machine), group: machine, machine: machine, count: len(children), cursorable: true, remoteErr: remoteErr, remoteWarn: remoteWarn, loading: isRemote && !m.remoteLoaded[machine]})
		rows = append(rows, children...)
		if machine != machines[len(machines)-1] {
			rows = append(rows, row{kind: separatorRow})
		}
	}
	return rows
}

func attentionRank(agent capabilities.Agent) int { return picker.AttentionRank(agent) }
func agentIdentity(record agentRecord) string {
	identity := picker.AgentIdentity(record.agent)
	return "agent:" + record.machine + ":" + identity
}

func (m Model) machineNames() []string {
	if m.catalog == nil {
		return []string{picker.LocalMachine()}
	}
	return append([]string(nil), m.catalog.Snapshot().Machines...)
}

func (m Model) containerRows() []row {
	rows := []row{}
	machines := m.machineNames()
	if string(m.query) != "" {
		sort.SliceStable(machines, func(i, j int) bool {
			left, right := m.machineMatchScore(machines[i]), m.machineMatchScore(machines[j])
			if left != right {
				return left > right
			}
			return machines[i] < machines[j]
		})
	}
	for machineIndex, machine := range machines {
		children := []row{}
		for _, place := range m.containerPlaces() {
			if place.Machine != machine {
				continue
			}
			match, ok := m.matchContainer(place)
			if !ok {
				continue
			}
			children = append(children, row{id: containerIdentity(place), kind: itemRow, itemKind: "container", label: place.ContainerName, machine: machine, group: machine, depth: 2, cursorable: true, matched: true, score: match.Score, place: place, activity: place.Activity})
		}
		if string(m.query) != "" {
			sort.SliceStable(children, func(i, j int) bool {
				if children[i].score != children[j].score {
					return children[i].score > children[j].score
				}
				return children[i].label < children[j].label
			})
		} else {
			sort.SliceStable(children, func(i, j int) bool { return m.containerBefore(children[i], children[j]) })
		}
		nameWidth := 0
		for _, child := range children {
			if width := shell.StringWidth(sanitize(child.place.ContainerName)); width > nameWidth {
				nameWidth = width
			}
		}
		capWidth := m.width * 40 / 100
		if capWidth <= 0 {
			capWidth = 40
		}
		if nameWidth > capWidth {
			nameWidth = capWidth
		}
		for i := range children {
			children[i].nameWidth = nameWidth
		}
		_, machineMatch := picker.Rank(string(m.query), sanitize(machine))
		remoteErr := m.groupErrors[machine]
		remoteWarn := m.groupWarnings[machine]
		isRemote := contains(m.remoteMachines, machine)
		if len(children) == 0 && !machineMatch && remoteErr == "" && !isRemote && string(m.query) != "" {
			continue
		}
		rows = append(rows, row{id: "machine:" + machine, kind: machineRow, label: shortenMachine(machine), group: machine, machine: machine, count: len(children), cursorable: true, remoteErr: remoteErr, remoteWarn: remoteWarn, loading: isRemote && !m.remoteLoaded[machine]})
		rows = append(rows, children...)
		if machineIndex < len(machines)-1 {
			rows = append(rows, row{kind: separatorRow})
		}
	}
	if string(m.query) != "" {
		rows = append(rows, row{id: "new:local", kind: newContainerRow, label: "+ new container…", depth: 1})
	}
	return rows
}

func (m Model) machineMatchScore(machine string) int {
	best := -1
	for _, place := range m.containerPlaces() {
		if place.Machine != machine {
			continue
		}
		if match, ok := m.matchContainer(place); ok && match.Score > best {
			best = match.Score
		}
	}
	if match, ok := picker.Rank(string(m.query), sanitize(machine)); ok && match.Score > best {
		best = match.Score
	}
	return best
}

func (m Model) containerBefore(left, right row) bool {
	leftUrgent := hasUnseenStopped(m.containerAgents(left.place))
	rightUrgent := hasUnseenStopped(m.containerAgents(right.place))
	if leftUrgent != rightUrgent {
		return leftUrgent
	}
	if left.activity == nil || right.activity == nil {
		if left.activity == nil && right.activity == nil {
			return left.label < right.label
		}
		return left.activity != nil
	}
	if !left.activity.Equal(*right.activity) {
		return left.activity.After(*right.activity)
	}
	return left.label < right.label
}
func (m Model) containerAgents(place picker.Place) []capabilities.Agent { return m.agentsFor(place) }
func hasUnseenStopped(agents []capabilities.Agent) bool {
	for _, agent := range agents {
		if strings.EqualFold(sanitize(agent.State), "stopped") && !agent.Seen {
			return true
		}
	}
	return false
}
func shortenMachine(machine string) string {
	machine = sanitize(machine)
	if index := strings.IndexByte(machine, '.'); index >= 0 {
		machine = machine[:index]
	}
	return strings.TrimSuffix(machine, ".local")
}

func containerIdentity(place picker.Place) string {
	return "container:" + place.Machine + ":" + place.ContainerID
}

func (m Model) projectRows() []row {
	places := m.projectPlaces()
	query := string(m.query)
	if query != "" {
		return m.flatProjectRows(places)
	}

	groups := make([]string, 0)
	seenGroups := map[string]bool{}
	for _, place := range places {
		if !seenGroups[place.Group] {
			seenGroups[place.Group] = true
			groups = append(groups, place.Group)
		}
	}
	rows := make([]row, 0, len(places)+len(groups))
	for _, group := range groups {
		groupPlaces := make([]picker.Place, 0)
		for _, place := range places {
			if place.Group == group {
				groupPlaces = append(groupPlaces, place)
			}
		}
		key := "group:" + group
		// Empty-query browsing is an overview of folders: every folder and every
		// checkout starts folded. Right unfolds a row explicitly.
		expanded := m.expanded[key] && !m.folded[key]
		rows = append(rows, row{id: key, kind: groupRow, label: group, group: group, count: len(groupPlaces), cursorable: true, expanded: expanded, foldable: true})
		if !expanded {
			continue
		}
		roots := make([]picker.Place, 0)
		for _, place := range groupPlaces {
			if place.ParentPath == "" || !containsPlacePath(groupPlaces, place.ParentPath) {
				roots = append(roots, place)
			}
		}
		for _, root := range roots {
			project := m.projectRow(root, group, 0, "", true, groupPlaces)
			rows = append(rows, project)
			if project.expanded {
				rows = append(rows, m.projectChildRows(root, groupPlaces, group)...)
			}
		}
	}
	return rows
}

func (m Model) flatProjectRows(places []picker.Place) []row {
	rows := make([]row, 0, len(places))
	for _, place := range places {
		match, ok := m.matchProject(place)
		if !ok {
			continue
		}
		rows = append(rows, row{
			id: projectIdentity(place), kind: itemRow, itemKind: "project", label: projectLabel(place),
			group: place.Group, depth: 0, cursorable: true, matched: true, score: match.Score, place: place,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return projectMatchBefore(rows[i], rows[j]) })
	return rows
}

// projectMatchBefore orders ranked search results. Scores already encode
// field precedence (name, then branch, then path). Equal scores fall back to
// a stable reading order: the shorter name first, a checkout before its
// worktrees, then the name itself.
func projectMatchBefore(left, right row) bool {
	if left.score != right.score {
		return left.score > right.score
	}
	leftName, rightName := []rune(left.label), []rune(right.label)
	if len(leftName) != len(rightName) {
		return len(leftName) < len(rightName)
	}
	leftRoot, rightRoot := left.place.ParentPath == "", right.place.ParentPath == ""
	if leftRoot != rightRoot {
		return leftRoot
	}
	return left.label < right.label
}

func (m Model) projectRow(place picker.Place, group string, depth int, treePrefix string, cursorable bool, all []picker.Place) row {
	match, _ := picker.Rank("", projectLabel(place))
	children := m.children(place, all)
	expanded := len(children) == 0 || (m.expanded[projectIdentity(place)] && !m.folded[projectIdentity(place)])
	return row{id: projectIdentity(place), kind: itemRow, itemKind: "project", label: projectLabel(place), group: group, depth: depth, count: len(children), cursorable: cursorable, matched: true, score: match.Score, place: place, expanded: expanded, foldable: len(children) > 0, treePrefix: treePrefix}
}

func (m Model) projectChildRows(parent picker.Place, all []picker.Place, group string) []row {
	children := m.children(parent, all)
	rows := make([]row, 0, len(children))
	for index, child := range children {
		prefix := "├ "
		if index == len(children)-1 {
			prefix = "└ "
		}
		project := m.projectRow(child, group, 1, prefix, true, all)
		rows = append(rows, project)
		if project.expanded {
			rows = append(rows, m.projectChildRows(child, all, group)...)
		}
	}
	return rows
}

func containsPlacePath(places []picker.Place, path string) bool {
	for _, place := range places {
		if place.Path == path {
			return true
		}
	}
	return false
}

func (m Model) children(place picker.Place, all []picker.Place) []picker.Place {
	children := []picker.Place{}
	for _, candidate := range all {
		if candidate.ParentPath == place.Path {
			children = append(children, candidate)
		}
	}
	return children
}
func projectIdentity(place picker.Place) string { return "project:" + place.Path }
func projectLabel(place picker.Place) string {
	if place.DisplayRemainder != "" {
		return sanitize(place.DisplayRemainder)
	}
	return sanitize(place.Path)
}

func abbreviateHome(value string) string {
	value = sanitize(value)
	home := sanitize(os.Getenv("HOME"))
	if home == "" {
		if detected, err := os.UserHomeDir(); err == nil {
			home = sanitize(detected)
		}
	}
	if home == "" {
		return value
	}
	home = strings.TrimRight(filepath.ToSlash(home), "/")
	value = filepath.ToSlash(value)
	if value == home {
		return "~"
	}
	if strings.HasPrefix(value, home+"/") {
		return "~" + strings.TrimPrefix(value, home)
	}
	return value
}

func (m Model) allAgents() []agentRecord {
	agents := append([]agentRecord(nil), m.agents...)
	return append(agents, m.remoteAgents...)
}

func newAgentRecords(agents []capabilities.Agent, machine string) []agentRecord {
	if machine == "" {
		machine = picker.LocalMachine()
	}
	result := make([]agentRecord, 0, len(agents))
	for _, agent := range agents {
		result = append(result, agentRecord{agent: agent, machine: machine})
	}
	return result
}

func (m Model) currentRow() (row, bool) {
	for _, row := range m.rows {
		if row.id == m.cursor && row.cursorable {
			return row, true
		}
	}
	return row{}, false
}
func (m Model) currentItem() (row, bool) {
	row, ok := m.currentRow()
	return row, ok && row.kind == itemRow
}

func (m Model) markedTargets() []picker.ContainerRef {
	if m.catalog == nil {
		return nil
	}
	return m.catalog.Marked(m.marked)
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

func (m *Model) applyGit(msg picker.GitContextMsg) {
	if msg.Err != nil {
		m.gitErrors[msg.Path] = msg.Err.Error()
		m.rebuild(false)
		return
	}
	delete(m.gitErrors, msg.Path)
	selectedPath := ""
	if current, ok := m.currentItem(); ok && current.place.Kind == picker.ProjectKind {
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
	// A linked worktree belongs beside its primary checkout, even when
	// discovery assigned the path to another configured parent.
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
		m.revealProjectAncestors(msg.Path)
	}
	if m.catalog != nil {
		m.catalog.SetProjects(m.deps.Projects)
	}
	m.rebuild(false)
}
func (m *Model) revealProjectAncestors(path string) {
	for path != "" {
		var found *picker.Place
		for i := range m.deps.Projects {
			if m.deps.Projects[i].Path == path {
				found = &m.deps.Projects[i]
				break
			}
		}
		if found == nil || found.ParentPath == "" {
			return
		}
		for _, key := range []string{projectIdentity(*found), "project:" + found.ParentPath, "group:" + found.Group} {
			m.expanded[key] = true
			delete(m.folded, key)
		}
		path = found.ParentPath
	}
}

func (m *Model) applyRemote(msg picker.RemoteBatch) {
	if m.catalog == nil {
		return
	}
	m.catalog.ApplyRemote(msg)
	m.syncCatalog()
	m.rebuild(false)
}

// syncCatalog projects one catalog snapshot into the TUI's row-oriented cache.
// The catalog remains the policy module; these records only avoid teaching row
// rendering about source batches and machine normalization.
func (m *Model) syncCatalog() {
	if m.catalog == nil {
		return
	}
	snapshot := m.catalog.Snapshot()
	local := picker.LocalMachine()
	m.agents = nil
	m.remoteAgents = nil
	for _, record := range snapshot.Agents {
		value := agentRecord{agent: record.Agent, machine: record.Machine, backend: record.Backend}
		if record.Machine == local {
			m.agents = append(m.agents, value)
		} else {
			m.remoteAgents = append(m.remoteAgents, value)
		}
	}
	m.remoteMachines = nil
	m.remoteLoaded = make(map[string]bool)
	m.groupErrors = make(map[string]string)
	m.groupWarnings = make(map[string]string)
	for _, machine := range snapshot.Machines {
		if machine == local {
			continue
		}
		m.remoteMachines = append(m.remoteMachines, machine)
		status := snapshot.Status[machine]
		m.remoteLoaded[machine] = status.Loaded
		m.groupErrors[machine] = sanitize(status.Error)
		m.groupWarnings[machine] = sanitize(status.Warning)
	}
}
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
		width := shell.StringWidth(item.name) + shell.StringWidth(fmt.Sprintf(" %d", m.matchCount(item.tab)))
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

// foldRight unfolds the selected folder or checkout and keeps it selected.
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

// foldLeft collapses the selected tree row. An expanded group or project with
// worktrees folds itself and keeps the cursor; any other row folds its parent
// and moves the cursor there. Search rows are flat, so folding keys only act
// while browsing folders.
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

// foldParent reports the row that Left folds from the selected row when the
// row itself cannot fold, or the empty string when Left has nothing to fold.
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

// View renders exactly the terminal dimensions once Bubble Tea has sent a
// size. The selected row is the only element with a background colour.
func (m Model) View() string {
	if !m.sized || m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := []string{m.tabLine(), m.searchLine()}
	bodyHeight := m.height - 3
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	body := m.renderBody(bodyHeight)
	lines = append(lines, body...)
	lines = append(lines, m.footer())
	for len(lines) < m.height {
		lines = append(lines, fit("", m.width))
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

func (m Model) tabLine() string {
	parts := make([]string, 0, 3)
	for _, item := range []struct {
		name string
		tab  Tab
	}{{"Agents", AgentsTab}, {"Containers", ContainersTab}, {"Projects", ProjectsTab}} {
		count := m.matchCount(item.tab)
		label := item.name
		countText := fmt.Sprintf(" %d", count)
		if item.tab == m.tab {
			label = accent().Bold(true).Underline(true).Render(label)
		} else {
			label = normal().Render(label)
		}
		parts = append(parts, label+muted().Render(countText))
	}
	return fit(strings.Join(parts, "   "), m.width)
}
func (m Model) searchLine() string {
	count := ""
	if len(m.query) > 0 {
		matches := m.matchCount(m.tab)
		label := "matches"
		if matches == 1 {
			label = "match"
		}
		count = muted().Render(fmt.Sprintf("%d %s", matches, label))
	}
	countWidth := shell.StringWidth(count)
	promptWidth := m.width - countWidth - 1
	if promptWidth < 1 {
		promptWidth = 1
	}
	prompt := shell.PromptLine(promptWidth, accent().Render("⌕ "), string(m.query), accent().Render("▌"))
	gap := m.width - shell.StringWidth(prompt) - countWidth
	if gap < 0 {
		count = shell.Truncate(count, max(0, m.width-shell.StringWidth(prompt)), "")
		countWidth = shell.StringWidth(count)
		gap = m.width - shell.StringWidth(prompt) - countWidth
	}
	return fit(prompt+strings.Repeat(" ", max(0, gap))+count, m.width)
}

type bodyLine struct {
	text  string
	rowID string
}

func (m Model) bodyLayout(height int) ([]bodyLine, int) {
	lines := []bodyLine{}
	selectedLine := -1
	for _, row := range m.rows {
		if row.cursorable && row.id == m.cursor {
			selectedLine = len(lines)
		}
		primary, secondary := m.rowText(row)
		rowID := ""
		if row.cursorable {
			rowID = row.id
		}
		lines = append(lines, bodyLine{text: primary, rowID: rowID})
		if height >= 2 && secondary != "" && row.kind == itemRow {
			lines = append(lines, bodyLine{text: secondary, rowID: rowID})
		}
	}
	if height <= 0 {
		return nil, 0
	}
	start := shell.ViewportStart(len(lines), height, selectedLine)
	if height >= 2 && selectedLine >= 0 && selectedLine+1 < len(lines) && start+height <= selectedLine+1 {
		start = selectedLine + 2 - height
	}
	if start < 0 {
		start = 0
	}
	if start+height > len(lines) {
		start = len(lines) - height
	}
	if start < 0 {
		start = 0
	}
	return lines, start
}

func (m Model) renderBody(height int) []string {
	lines, start := m.bodyLayout(height)
	if height <= 0 {
		return nil
	}
	out := []string{}
	for i := start; i < len(lines) && len(out) < height; i++ {
		value := fit(lines[i].text, m.width)
		if lines[i].rowID != "" && lines[i].rowID == m.cursor {
			value = selectedRowBackground(value)
		}
		out = append(out, value)
	}
	for len(out) < height {
		out = append(out, fit("", m.width))
	}
	return out
}
func (m Model) rowText(row row) (string, string) {
	selected := row.cursorable && row.id == m.cursor
	marker := "  "
	// Secondary agent details may occupy a second line, but the selection
	// accent belongs to the row's first line only. This keeps its height
	// constant while moving across every tab.
	switch row.kind {
	case machineRow:
		labelStyle := bold()
		if selected {
			labelStyle = normal()
		}
		value := strings.Repeat("  ", row.depth) + labelStyle.Render(highlightText(sanitize(row.label), string(m.query), selected)) + muted().Render(fmt.Sprintf("  %d", row.count))
		if row.remoteErr != "" {
			value += muted().Render("  " + sanitize(row.remoteErr))
		} else if row.remoteWarn != "" {
			value += yellow().Render("  " + sanitize(row.remoteWarn))
		} else if row.loading {
			value += muted().Render("  loading…")
		}
		return marker + value, ""
	case groupRow:
		labelStyle := bold()
		if selected {
			labelStyle = normal()
		}
		return marker + foldPrefixStyled(row) + labelStyle.Render(abbreviateHome(row.label)) + muted().Render(fmt.Sprintf("  %d", row.count)), ""
	case newContainerRow:
		return "  " + strings.Repeat("  ", row.depth) + muted().Render(sanitize(row.label)), ""
	case separatorRow:
		return "", ""
	case itemRow:
		if row.itemKind == "agent" {
			return m.agentText(row, marker, selected)
		}
		if row.itemKind == "container" {
			return m.containerText(row, marker, selected)
		}
		return m.placeText(row, marker, selected)
	default:
		return "", ""
	}
}

func (m Model) agentText(row row, marker string, selected bool) (string, string) {
	a := row.agent
	container := a.Container
	nameStyle := bold()
	if selected {
		nameStyle = normal()
	}
	primary := nameStyle.Render(highlightText(sanitize(a.Harness), string(m.query), selected)) + muted().Render("  "+highlightText(sanitize(container), string(m.query), selected))
	state := strings.ToLower(strings.TrimSpace(sanitize(a.State)))
	stateLabel := "● " + state
	if state == "stopped" && a.StoppedSince != nil {
		stateLabel += " " + stoppedAge(m.now(), *a.StoppedSince)
	}
	left := stateText(a, stateLabel, selected)
	if a.Branch != "" {
		branch := highlightText("⎇ "+sanitize(a.Branch), string(m.query), selected)
		if !strings.Contains(branch, "\x1b[") {
			branch = muted().Render(branch)
		}
		left += "   " + branch
	}
	targetText := highlightText(sanitize(a.Target), string(m.query), selected)
	target := targetText
	if !strings.Contains(targetText, "\x1b[") {
		target = muted().Render(targetText)
	}
	indent := strings.Repeat("  ", row.depth)
	gap := m.width - shell.StringWidth(marker) - shell.StringWidth(indent) - shell.StringWidth(left) - shell.StringWidth(target)
	if gap < 2 {
		gap = 2
	}
	secondary := marker + indent + left + strings.Repeat(" ", gap) + target
	return marker + indent + primary, secondary
}
func stateText(agent capabilities.Agent, text string, selected bool) string {
	if !selected {
		return stateColor(agent).Render(text)
	}
	return selectedStateMarkerStart + selectedStateColor(agent).Render(text) + selectedStateMarkerEnd
}
func (m Model) containerText(row row, marker string, selected bool) (string, string) {
	name := sanitize(row.place.ContainerName)
	if row.nameWidth > 0 {
		name = shell.Truncate(name, row.nameWidth, "…")
		name += strings.Repeat(" ", max(0, row.nameWidth-shell.StringWidth(name)))
	}
	nameGutter := "  "
	marked := m.marked[picker.ContainerRef{Machine: row.place.Machine, ID: row.place.ContainerID}]
	if marked {
		nameGutter = accent().Render("◆ ")
		name = highlightMarkedText(name, string(m.query), selected)
	} else {
		name = highlightText(name, string(m.query), selected)
	}
	name = nameGutter + name
	agents := m.containerAgents(row.place)
	dots := make([]string, 0, len(agents))
	for _, agent := range agents {
		dots = append(dots, stateText(agent, "●", selected))
	}
	line := name
	if len(dots) > 0 {
		line += " " + strings.Join(dots, " ")
	}
	available := max(1, m.width-shell.StringWidth(marker))
	if shell.StringWidth(line) > available {
		line = shell.Truncate(line, available, "…")
	}
	return marker + line, ""
}

type projectColumns struct {
	name, git, path int
	search          bool
}

const (
	// projectMinName is the floor the name column may shrink to before a row
	// stops being readable.
	projectMinName = 16
	// projectMinBranch is the floor the Git column may shrink to before the
	// name column gives up any of its own cells.
	projectMinBranch = 12
	// projectMinPath is the narrowest folder column worth rendering: enough for
	// a leading ellipsis and a path segment.
	projectMinPath = 6
	// projectNameShare is the share of the row the name column may claim
	// before it is capped. Names are what the user scans, so they lead.
	projectNameShare = 0.45
)

// projectLayout sizes the project columns from the content of the visible
// rows. Names lead: the longest visible name claims its share of the row up
// front, the branch and its Git summary share one column, and the flat-mode
// folder on the left takes only what is left over. The folder is left-
// truncated and dropped before the name gives up cells; the branch truncates
// before its Git summary, which stays visible.
func (m Model) projectLayout() projectColumns {
	layout := projectColumns{search: string(m.query) != ""}
	maxName, maxGit, maxSummary, maxPath := 0, 0, 0, 0
	for _, candidate := range m.rows {
		if candidate.kind != itemRow || candidate.itemKind != "project" {
			continue
		}
		if width := projectNameWidth(candidate); width > maxName {
			maxName = width
		}
		branch := shell.StringWidth(projectBranch(candidate.place))
		summary := shell.StringWidth(m.projectSummary(candidate.place))
		git := branch
		if summary > 0 {
			if branch > 0 {
				git += 2
			}
			git += summary
		}
		if git > maxGit {
			maxGit = git
		}
		if summary > maxSummary {
			maxSummary = summary
		}
		if layout.search {
			// The trailing slash is part of the folder cell.
			if width := shell.StringWidth(abbreviateHome(candidate.group)) + 1; width > maxPath {
				maxPath = width
			}
		}
	}

	// The marker and the gutter between the name and the Git column are fixed
	// costs. The folder column takes only what the name and Git column leave
	// unused, so it never costs the name a cell.
	base := max(0, m.width-(2+2))

	// The name and the Git column are sized on their own, without the folder.
	// A row that already fits keeps its natural widths.
	if maxName+maxGit <= base {
		layout.name, layout.git = maxName, maxGit
	} else {
		// The name leads. It claims the longest visible name up to its share
		// of the row, but never drops below its floor.
		nameFloor := min(projectMinName, maxName)
		nameCap := int(float64(m.width) * projectNameShare)
		if nameCap < projectMinName {
			nameCap = projectMinName
		}
		nameTarget := min(maxName, nameCap)
		if nameTarget < nameFloor {
			nameTarget = nameFloor
		}
		if nameTarget > base {
			nameTarget = base
		}

		// The Git summary never truncates; the branch gives up cells first
		// and the name shrinks only after the summary floor is protected.
		gitFloor := max(maxSummary, min(maxGit, projectMinBranch))
		git := min(maxGit, max(base-nameTarget, 0))
		if git < gitFloor {
			nameTarget = min(nameTarget, max(nameFloor, base-gitFloor))
			git = min(maxGit, max(base-nameTarget, 0))
			if git < gitFloor {
				git = gitFloor
			}
		}
		layout.name, layout.git = nameTarget, git
	}

	// The folder is lowest priority: it is right-aligned and left-truncated,
	// so it can stop short of its natural width without moving the name, and
	// it is dropped whole before the name loses a cell or the branch is cut.
	if layout.search && maxPath > 0 {
		if leftover := base - layout.name - layout.git; leftover >= projectMinPath {
			layout.path = min(maxPath, leftover)
		}
	}

	layout.name = max(layout.name, 0)
	layout.git = max(layout.git, 0)
	return layout
}

func projectNameWidth(candidate row) int {
	width := shell.StringWidth(foldPrefix(candidate) + projectLabel(candidate.place))
	if candidate.foldable && !candidate.expanded {
		width += shell.StringWidth(fmt.Sprintf("  %d", candidate.count))
	}
	return width
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m Model) placeText(row row, marker string, selected bool) (string, string) {
	if row.place.Kind != picker.ProjectKind {
		return marker + highlightText(projectLabel(row.place), string(m.query), selected), ""
	}
	layout := m.projectLayout()
	name := foldPrefix(row) + projectLabel(row.place)
	count := ""
	if row.foldable && !row.expanded {
		count = fmt.Sprintf("  %d", row.count)
	}
	name = shell.Truncate(name, max(0, layout.name-shell.StringWidth(count)), "…")
	nameText := highlightText(name, string(m.query), selected)
	if prefix := foldPrefix(row); prefix != "" {
		// Tree gutters and fold chevrons are structural, not part of fuzzy matching.
		nameText = foldPrefixStyled(row) + highlightText(strings.TrimPrefix(name, prefix), string(m.query), selected)
	}
	if count != "" {
		nameText += muted().Render(count)
	}
	nameCell := fit(nameText, layout.name)
	gitCell := fit(m.projectGitStyled(row.place, layout.git, selected), layout.git)
	line := ""
	if layout.search && layout.path > 0 {
		line = m.projectPathCell(row.group, layout.path, selected)
	}
	line += nameCell + "  " + gitCell
	return marker + line, ""
}

// projectGitStyled renders the branch and its Git summary as one cell: the
// branch first, two spaces, then the summary. The branch truncates before the
// summary so change counts and divergence stay visible.
func (m Model) projectGitStyled(place picker.Place, width int, selected bool) string {
	if width <= 0 {
		return ""
	}
	branch := highlightText(projectBranch(place), string(m.query), selected)
	if !strings.Contains(branch, "\x1b[") {
		branch = muted().Render(branch)
	}
	summary := m.projectSummaryStyled(place)
	if summary == "" {
		return shell.Truncate(branch, width, "")
	}
	summaryWidth := shell.StringWidth(summary)
	if summaryWidth >= width {
		return shell.Truncate(summary, width, "")
	}
	if projectBranch(place) == "" {
		return summary
	}
	branchWidth := width - summaryWidth - 2
	if branchWidth <= 0 {
		return summary
	}
	return shell.Truncate(branch, branchWidth, "") + "  " + summary
}

// projectPathCell renders the flat-search folder as a muted path segment
// ending in a slash. It is right-aligned and truncated from the left so the
// deep end of the path stays readable while names stay aligned.
func (m Model) projectPathCell(group string, width int, selected bool) string {
	path := shell.TruncateLeft(abbreviateHome(group)+"/", width, "…")
	pathText := highlightText(path, string(m.query), selected)
	if !strings.Contains(pathText, "\x1b[") {
		pathText = muted().Render(pathText)
	}
	return strings.Repeat(" ", max(0, width-shell.StringWidth(path))) + pathText
}

func projectBranch(place picker.Place) string {
	if place.GitContext == nil {
		if place.Git {
			return "…"
		}
		return ""
	}
	if identity := sanitize(place.GitContext.Identity); identity != "" {
		return "⎇ " + identity
	}
	return ""
}

func (m Model) projectSummary(place picker.Place) string {
	if errText := sanitize(m.gitErrors[place.Path]); errText != "" {
		return "git: error " + errText
	}
	if place.GitContext == nil {
		return ""
	}
	g := *place.GitContext
	parts := []string{}
	if g.Adds > 0 {
		parts = append(parts, "+"+itoa(g.Adds))
	}
	if g.Deletes > 0 {
		parts = append(parts, "−"+itoa(g.Deletes))
	}
	if g.Dirty {
		parts = append(parts, "*")
	}
	if g.Ahead > 0 {
		parts = append(parts, "↑"+itoa(g.Ahead))
	}
	if g.Behind > 0 {
		parts = append(parts, "↓"+itoa(g.Behind))
	}
	return strings.Join(parts, " ")
}

func (m Model) projectSummaryStyled(place picker.Place) string {
	if errText := sanitize(m.gitErrors[place.Path]); errText != "" {
		return muted().Render("git: error " + errText)
	}
	if place.GitContext == nil {
		return ""
	}
	g := *place.GitContext
	parts := []string{}
	if g.Adds > 0 {
		parts = append(parts, brightGreen().Render("+"+itoa(g.Adds)))
	}
	if g.Deletes > 0 {
		parts = append(parts, brightRed().Render("−"+itoa(g.Deletes)))
	}
	if g.Dirty {
		parts = append(parts, yellow().Render("*"))
	}
	if g.Ahead > 0 {
		parts = append(parts, divergence().Render("↑"+itoa(g.Ahead)))
	}
	if g.Behind > 0 {
		parts = append(parts, divergence().Render("↓"+itoa(g.Behind)))
	}
	return strings.Join(parts, " ")
}

func (m Model) footer() string {
	if m.toast != "" {
		return fit(accent().Render(sanitize(m.toast)), m.width)
	}
	if m.confirmKill {
		return fit(accent().Render("kill selected containers? ")+accent().Render("y")+muted().Render(" confirm  ")+accent().Render("n")+muted().Render(" cancel"), m.width)
	}
	if m.renameMode {
		suffix := muted().Render("  Enter save  Esc cancel")
		promptWidth := m.width - shell.StringWidth(suffix)
		if promptWidth < 1 {
			promptWidth = 1
		}
		prompt := shell.PromptLine(promptWidth, accent().Render("rename: "), string(m.renameInput), accent().Render("▌"))
		return fit(prompt+suffix, m.width)
	}
	if m.refreshErr != nil {
		return fit(muted().Render("agent refresh: "+sanitize(m.refreshErr.Error())), m.width)
	}
	left := []string{}
	left = append(left, hint("↑↓", "move"))
	if _, ok := m.currentItem(); ok {
		left = append(left, hint("Enter", "open"))
	}
	left = append(left, hint("Tab", "tabs"))
	if m.tab == ProjectsTab {
		if current, ok := m.currentRow(); ok && (current.foldable || foldParent(current) != "") {
			left = append(left, hint("←→", "fold"))
		}
	}
	if m.currentContainer() || len(m.markedTargets()) > 0 {
		left = append(left, hint("Ctrl-X", "kill"))
	}
	if m.currentContainer() {
		left = append(left, hint("Ctrl-R", "rename"), hint("Ctrl-T", "mark"))
	}
	right := ""
	if count := len(m.markedTargets()); count > 0 {
		right = accent().Render(fmt.Sprintf("%d marked", count))
	}
	available := m.width
	if right != "" {
		available -= shell.StringWidth(right) + 1
	}
	leftText := wholeHints(left, available)
	if right != "" {
		return fit(leftText+strings.Repeat(" ", max(1, m.width-shell.StringWidth(leftText)-shell.StringWidth(right)))+right, m.width)
	}
	return fit(leftText, m.width)
}
func wholeHints(hints []string, width int) string {
	if width <= 0 {
		return ""
	}
	parts := make([]string, 0, len(hints))
	used := 0
	for _, value := range hints {
		separator := 0
		if len(parts) > 0 {
			separator = 2
		}
		partWidth := shell.StringWidth(value)
		if used+separator+partWidth > width {
			break
		}
		parts = append(parts, value)
		used += separator + partWidth
	}
	return strings.Join(parts, "  ")
}
func hint(keyValue, label string) string {
	return accent().Bold(true).Render(keyValue) + " " + muted().Render(label)
}

func sanitize(value string) string { return shell.Plain(value) }

func fit(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = stripBackgroundResets(value)
	value = shell.Truncate(value, width, "")
	return value + strings.Repeat(" ", max(0, width-shell.StringWidth(value)))
}

func selectedRowBackground(value string) string {
	// Selected rows use one foreground everywhere except explicitly marked
	// agent-state segments. Strip every other per-segment foreground (and faint)
	// first, then restore the selection start after each reset. Bold and
	// underline survive, so matched letters and marked names stay distinguishable.
	value = stripForegroundMarked(value)
	style := lipgloss.NewStyle().Background(selectionBackground()).Foreground(selectionForeground())
	start := strings.TrimSuffix(style.Render(""), "\x1b[0m")
	if start == "" {
		return value
	}
	value = strings.ReplaceAll(value, "\x1b[0m", "\x1b[0m"+start)
	return start + value + "\x1b[0m"
}

// stripForegroundMarked drops every unmarked foreground colour and the faint
// attribute from styled text while keeping the remaining SGR attributes
// (reset, bold, underline, …).
func stripForegroundMarked(value string) string {
	marked := false
	out := strings.Builder{}
	for index := 0; index < len(value); {
		if strings.HasPrefix(value[index:], selectedStateMarkerStart) {
			marked = true
			index += len(selectedStateMarkerStart)
			continue
		}
		if strings.HasPrefix(value[index:], selectedStateMarkerEnd) {
			marked = false
			index += len(selectedStateMarkerEnd)
			continue
		}
		if value[index] != '\x1b' || index+1 >= len(value) || value[index+1] != '[' {
			out.WriteByte(value[index])
			index++
			continue
		}
		end := strings.IndexByte(value[index+2:], 'm')
		if end < 0 {
			out.WriteString(value[index:])
			break
		}
		end += index + 2
		if kept := keptMarkedAttributes(value[index+2:end], marked); kept != "" {
			out.WriteString("\x1b[")
			out.WriteString(kept)
			out.WriteByte('m')
		}
		index = end + 1
	}
	return out.String()
}

func keptMarkedAttributes(params string, marked bool) string {
	if marked {
		return params
	}
	return keptAttributes(params)
}

// keptAttributes removes the foreground colour and faint parameters from an
// SGR parameter list and returns the rest joined as an SGR list.
func keptAttributes(params string) string {
	parts := strings.Split(params, ";")
	kept := make([]string, 0, len(parts))
	for position := 0; position < len(parts); position++ {
		code, err := strconv.Atoi(parts[position])
		if err != nil {
			if parts[position] != "" {
				kept = append(kept, parts[position])
			}
			continue
		}
		switch {
		case code == 2, code == 39:
			// Faint and the default foreground both fight the selection.
		case code == 38:
			// Skip the extended foreground payload (38;5;n or 38;2;r;g;b).
			if position+1 < len(parts) {
				switch parts[position+1] {
				case "5":
					position += 2
				case "2":
					position += 4
				}
			}
		case code >= 30 && code <= 37, code >= 90 && code <= 97:
			// Standard and bright foreground colours.
		default:
			kept = append(kept, parts[position])
		}
	}
	return strings.Join(kept, ";")
}

// selectionBackground and selectionForeground are chosen together so the row's
// text always contrasts with its own background in either terminal theme.
func selectionBackground() lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: "#d9eaf7", Dark: "#31516f"}
}

func selectionForeground() lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: "#0b1b28", Dark: "#f4f8fc"}
}

func stripBackgroundResets(value string) string {
	return strings.ReplaceAll(value, "\x1b[49m", "")
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
func itoa(value int) string                  { return fmt.Sprintf("%d", value) }
func stoppedAge(now, since time.Time) string { return picker.StoppedAge(now, since) }
func stateColor(agent capabilities.Agent) lipgloss.Style {
	switch {
	case strings.EqualFold(strings.TrimSpace(agent.State), "stopped") && !agent.Seen:
		return red()
	case strings.EqualFold(strings.TrimSpace(agent.State), "stopped"):
		return green()
	default:
		return yellow()
	}
}

func selectedStateForeground(agent capabilities.Agent) lipgloss.AdaptiveColor {
	switch {
	case strings.EqualFold(strings.TrimSpace(agent.State), "stopped") && !agent.Seen:
		return lipgloss.AdaptiveColor{Light: "#9b1c1c", Dark: "#ffb3ba"}
	case strings.EqualFold(strings.TrimSpace(agent.State), "stopped"):
		return lipgloss.AdaptiveColor{Light: "#166534", Dark: "#86efac"}
	default:
		return lipgloss.AdaptiveColor{Light: "#8a4b08", Dark: "#fbbf24"}
	}
}

func selectedStateColor(agent capabilities.Agent) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(selectedStateForeground(agent))
}

const (
	selectedStateMarkerStart = "\x1b[58;5;0m"
	selectedStateMarkerEnd   = "\x1b[59m"
)

// palette holds the brand colours of assets/logo.svg. Ground, tile and
// tileBorder are the icon frame; the TUI never paints a background, so on
// screen only Fg, Muted and Accent are used. Every entry carries a light and a
// dark variant, because the lime accent is unreadable on a light terminal: the
// light variants are an olive accent and a near-black foreground.
var palette = struct {
	Ground     lipgloss.AdaptiveColor
	Tile       lipgloss.AdaptiveColor
	TileBorder lipgloss.AdaptiveColor
	Fg         lipgloss.AdaptiveColor
	Muted      lipgloss.AdaptiveColor
	Accent     lipgloss.AdaptiveColor
}{
	Ground:     lipgloss.AdaptiveColor{Light: "#0F1115", Dark: "#0F1115"},
	Tile:       lipgloss.AdaptiveColor{Light: "#171A21", Dark: "#171A21"},
	TileBorder: lipgloss.AdaptiveColor{Light: "#2A2F3A", Dark: "#2A2F3A"},
	Fg:         lipgloss.AdaptiveColor{Light: "#1F2329", Dark: "#E8EAED"},
	Muted:      lipgloss.AdaptiveColor{Light: "#5F6774", Dark: "#8A919E"},
	Accent:     lipgloss.AdaptiveColor{Light: "#4E7A00", Dark: "#B8F15A"},
}

func normal() lipgloss.Style    { return lipgloss.NewStyle().Foreground(palette.Fg) }
func muted() lipgloss.Style     { return lipgloss.NewStyle().Foreground(palette.Muted) }
func foldStyle() lipgloss.Style { return muted() }
func accent() lipgloss.Style    { return lipgloss.NewStyle().Foreground(palette.Accent) }

// Semantic colours stay terminal ANSI codes, so agent state, Git summary and
// errors follow the user's terminal theme instead of the brand palette.
func divergence() lipgloss.Style  { return lipgloss.NewStyle().Foreground(lipgloss.Color("6")) }
func red() lipgloss.Style         { return lipgloss.NewStyle().Foreground(lipgloss.Color("1")) }
func green() lipgloss.Style       { return lipgloss.NewStyle().Foreground(lipgloss.Color("2")) }
func yellow() lipgloss.Style      { return lipgloss.NewStyle().Foreground(lipgloss.Color("3")) }
func brightGreen() lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color("10")) }
func brightRed() lipgloss.Style   { return lipgloss.NewStyle().Foreground(lipgloss.Color("9")) }
func bold() lipgloss.Style        { return normal().Bold(true) }
func highlightText(text, query string, selected bool) string {
	match, ok := picker.Rank(query, text)
	if !ok || len(match.Positions) == 0 {
		return text
	}
	positions := map[int]bool{}
	for _, pos := range match.Positions {
		positions[pos] = true
	}
	runes := []rune(text)
	out := strings.Builder{}
	for i, r := range runes {
		if positions[i] {
			style := accent().Bold(true)
			if selected {
				// The selected row has no per-segment colour, so the match
				// relies on bold and underline alone to stay visible.
				style = style.Underline(true)
			}
			out.WriteString(style.Render(string(r)))
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// highlightMarkedText paints every name character of a marked row with the
// accent colour. Matching characters add bold and underline, so the query
// stays visible without a second colour. A selected row drops the accent and
// keeps the mark cue as bold weight instead.
func highlightMarkedText(text, query string, selected bool) string {
	match, ok := picker.Rank(query, text)
	if !ok || len(match.Positions) == 0 {
		if selected {
			return bold().Render(text)
		}
		return accent().Render(text)
	}
	positions := map[int]bool{}
	for _, pos := range match.Positions {
		positions[pos] = true
	}
	out := strings.Builder{}
	for i, r := range []rune(text) {
		style := accent()
		if selected {
			// The selected row keeps the mark cue as weight rather than colour.
			style = bold()
		}
		if positions[i] {
			style = style.Bold(true).Underline(true)
		}
		out.WriteString(style.Render(string(r)))
	}
	return out.String()
}

func (m *Model) SetAgents(agents []capabilities.Agent) {
	m.agents = newAgentRecords(agents, "")
	if m.catalog != nil {
		m.catalog.ReplaceLocalAgents(agents)
		m.syncCatalog()
	}
	m.rebuild(false)
}

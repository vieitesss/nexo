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
	"strings"
	"time"

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
	Backend    string
	Projects   []picker.Place
	Containers []capabilities.Container
	Agents     []capabilities.Agent

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
	model := NewModel(ctx, deps, tab)
	input := deps.Input
	if input == nil {
		input = os.Stdin
	}
	output := deps.Output
	if output == nil {
		output = os.Stdout
	}
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen())
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
	loading      bool
	activity     *time.Time
	nameWidth    int
	treePrefix   string
}

type agentsMsg struct {
	agents []capabilities.Agent
	err    error
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
	ctx    context.Context
	deps   Deps
	tab    Tab
	query  []rune
	rows   []row
	cursor string

	width, height   int
	sized           bool
	forceFirst      bool
	folded          map[string]bool
	expanded        map[string]bool
	marked          map[picker.ContainerRef]bool
	remotePlaces    []picker.Place
	remoteMachines  []string
	remoteLoaded    map[string]bool
	groupErrors     map[string]string
	agents          []agentRecord
	remoteAgents    []agentRecord
	gitErrors       map[string]string
	refreshInFlight *bool
	refreshErr      error
	gitSlots        chan struct{}
	cancelLoads     context.CancelFunc

	confirmKill      bool
	killReady        bool
	killTargets      []picker.ContainerRef
	renameMode       bool
	renameInput      []rune
	renameReady      bool
	renameTarget     picker.ContainerRef
	renameTargetSet  bool
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
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	m := Model{
		ctx: loadCtx, cancelLoads: cancelLoads, deps: deps, tab: tab, agents: newAgentRecords(deps.Agents, ""),
		folded: make(map[string]bool), expanded: make(map[string]bool), marked: make(map[picker.ContainerRef]bool),
		groupErrors: make(map[string]string), gitErrors: make(map[string]string), now: now, refreshErr: deps.AgentError,
		remoteMachines: append([]string(nil), deps.RemoteMachines...), remoteLoaded: make(map[string]bool), refreshInFlight: new(bool), gitSlots: make(chan struct{}, gitConcurrency(deps.GitLoader)),
	}
	for _, machine := range deps.RemoteMachines {
		m.remoteLoaded[machine] = false
	}
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
	cmds = append(cmds, m.deps.RemoteCommands...)
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

func (m Model) refreshAgents() tea.Cmd {
	return func() tea.Msg {
		agents, err := m.deps.AgentSource.ListAgents(m.ctx)
		return agentsMsg{agents: agents, err: err}
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

// KillTargetsRequest reports the confirmed containers, if any.
func (m Model) KillTargetsRequest() ([]picker.ContainerRef, bool) {
	if !m.killReady {
		return nil, false
	}
	refs := append([]picker.ContainerRef(nil), m.killTargets...)
	if len(refs) == 0 && m.killReady {
		refs = m.markedTargets()
	}
	return refs, len(refs) > 0
}

// RenameTargetRequest reports a confirmed rename, if any.
func (m Model) RenameTargetRequest() (picker.ContainerRef, string, bool) {
	if !m.renameReady {
		return picker.ContainerRef{}, "", false
	}
	if !m.renameTargetSet {
		return picker.ContainerRef{}, "", false
	}
	return m.renameTarget, string(m.renameInput), true
}

func (m Model) execute(ctx context.Context) error {
	if m.cancelled {
		return nil
	}
	if refs, ok := m.KillTargetsRequest(); ok {
		if m.deps.KillContainers == nil {
			return errors.New("container killing is not supported")
		}
		return m.deps.KillContainers(ctx, refs)
	}
	if ref, name, ok := m.RenameTargetRequest(); ok {
		if m.deps.RenameContainer == nil {
			return errors.New("container renaming is not supported")
		}
		return m.deps.RenameContainer(ctx, ref, name)
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

func (m Model) sourcePlaces() []picker.Place {
	places := append([]picker.Place(nil), m.deps.Projects...)
	places = append(places, picker.ContainerPlaces(m.deps.Containers)...)
	places = append(places, m.remotePlaces...)
	return places
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
	places := picker.MergeContainerPlaces(picker.ContainerPlaces(m.deps.Containers), m.remotePlaces)
	for i := range places {
		if places[i].Kind == picker.ContainerKind {
			places[i].Machine = normalizedMachine(places[i].Machine)
			places[i].Group = places[i].Machine
		}
		if places[i].Kind != picker.ContainerKind {
			continue
		}
		places[i].Agents = m.agentsFor(places[i])
	}
	return places
}

func (m Model) agentsFor(place picker.Place) []capabilities.Agent {
	if place.Kind == "" {
		place.Kind = picker.ContainerKind
	}
	all := make([]capabilities.Agent, 0)
	for _, record := range m.allAgents() {
		if record.machine != place.Machine {
			continue
		}
		all = append(all, record.agent)
	}
	// Keep association semantics in the shared picker helper: IDs are only
	// meaningful inside the source machine, and label fallback is scoped too.
	updated := picker.ApplyAgentDotsForMachine([]picker.Place{place}, all, place.Machine)
	if len(updated) == 1 {
		return updated[0].Agents
	}
	return nil
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
	agents := append([]agentRecord(nil), m.allAgents()...)
	sort.SliceStable(agents, func(i, j int) bool {
		ri, rj := attentionRank(agents[i].agent), attentionRank(agents[j].agent)
		if ri != rj {
			return ri < rj
		}
		if ri == 0 && agents[i].agent.StoppedSince != nil && agents[j].agent.StoppedSince != nil && !agents[i].agent.StoppedSince.Equal(*agents[j].agent.StoppedSince) {
			return agents[i].agent.StoppedSince.Before(*agents[j].agent.StoppedSince)
		}
		return agentIdentity(agents[i]) < agentIdentity(agents[j])
	})
	rows := make([]row, 0, len(agents))
	for _, record := range agents {
		match, ok := m.matchAgent(record)
		if !ok {
			continue
		}
		agent := record.agent
		rows = append(rows, row{id: agentIdentity(record), kind: itemRow, itemKind: "agent", label: agent.Harness, group: agent.Container, cursorable: true, matched: true, score: match.Score, agent: agent, agentMachine: record.machine, agentBackend: record.backend})
	}
	if string(m.query) != "" {
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].score != rows[j].score {
				return rows[i].score > rows[j].score
			}
			return rows[i].label < rows[j].label
		})
	}
	return rows
}

func attentionRank(agent capabilities.Agent) int { return picker.AttentionRank(agent) }
func agentIdentity(record agentRecord) string {
	identity := picker.AgentIdentity(record.agent)
	return "agent:" + record.machine + ":" + identity
}

func (m Model) machineNames() []string {
	seen := map[string]bool{picker.LocalMachine(): true}
	for _, place := range m.containerPlaces() {
		if place.Machine != "" {
			seen[place.Machine] = true
		}
	}
	for _, machine := range m.remoteMachines {
		if machine != "" {
			seen[machine] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		local := picker.LocalMachine()
		if names[i] == local {
			return names[j] != local
		}
		if names[j] == local {
			return false
		}
		return names[i] < names[j]
	})
	return names
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
		isRemote := contains(m.remoteMachines, machine)
		if len(children) == 0 && !machineMatch && remoteErr == "" && !isRemote && string(m.query) != "" {
			continue
		}
		rows = append(rows, row{id: "machine:" + machine, kind: machineRow, label: shortenMachine(machine), group: machine, machine: machine, count: len(children), cursorable: true, remoteErr: remoteErr, loading: isRemote && !m.remoteLoaded[machine]})
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
	return picker.MarkedContainerRefs(m.containerPlaces(), m.marked)
}

// Update implements tea.Model. Printable keys are intentionally handled last
// and always become search input; there are no printable action shortcuts.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil
	case agentsMsg:
		if m.refreshInFlight != nil {
			*m.refreshInFlight = false
		}
		if msg.err != nil {
			m.refreshErr = msg.err
		} else {
			m.refreshErr = nil
			m.agents = newAgentRecords(msg.agents, "")
			m.rebuild(false)
		}
		return m, nil
	case picker.AgentBatch:
		// Keep the existing picker command seam compatible while callers move
		// to AgentSource. A batch without machine provenance is local.
		if msg.Error == "" {
			m.agents = newAgentRecords(msg.Agents, "")
			m.rebuild(false)
		}
		return m, nil
	case refreshTickMsg:
		if m.deps.AgentSource == nil {
			return m, nil
		}
		if m.refreshInFlight != nil && *m.refreshInFlight {
			return m, m.tick()
		}
		if m.refreshInFlight == nil {
			m.refreshInFlight = new(bool)
		}
		*m.refreshInFlight = true
		return m, tea.Batch(m.refreshAgents(), m.tick())
	case picker.GitContextMsg:
		m.applyGit(msg)
		return m, nil
	case picker.RemoteBatch:
		m.applyRemote(msg)
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
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
	for i := range m.remotePlaces {
		if m.remotePlaces[i].Path == msg.Path {
			m.remotePlaces[i].GitContext = &value
			m.remotePlaces[i].ParentPath = value.PrimaryPath
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
	if msg.Machine == "" {
		return
	}
	m.groupErrors[msg.Machine] = sanitize(msg.Error)
	m.remoteLoaded[msg.Machine] = true
	seen := false
	for _, machine := range m.remoteMachines {
		if machine == msg.Machine {
			seen = true
		}
	}
	if !seen {
		m.remoteMachines = append(m.remoteMachines, msg.Machine)
	}
	updatedPlaces := m.remotePlaces[:0]
	for _, place := range m.remotePlaces {
		if place.Machine != msg.Machine {
			updatedPlaces = append(updatedPlaces, place)
		}
	}
	for i := range msg.Places {
		if msg.Places[i].Machine == "" {
			msg.Places[i].Machine = msg.Machine
		}
	}
	m.remotePlaces = append(updatedPlaces, msg.Places...)
	updatedAgents := make([]agentRecord, 0, len(m.remoteAgents)+len(msg.Agents))
	for _, record := range m.remoteAgents {
		if record.machine != msg.Machine {
			updatedAgents = append(updatedAgents, record)
		}
	}
	for _, agent := range msg.Agents {
		updatedAgents = append(updatedAgents, agentRecord{agent: agent, machine: msg.Machine, backend: msg.Backend})
	}
	m.remoteAgents = updatedAgents
	m.rebuild(false)
}
func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		m.cancelled = true
		m.confirmKill = false
		m.renameMode = false
		m.killTargets = nil
		m.renameTargetSet = false
		m.cancelBackgroundLoads()
		return m, tea.Quit
	}
	if m.confirmKill {
		switch strings.ToLower(msg.String()) {
		case "y":
			m.confirmKill = false
			m.killReady = true
			m.cancelBackgroundLoads()
			return m, tea.Quit
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
			m.renameTargetSet = false
		case tea.KeyEnter:
			if strings.TrimSpace(string(m.renameInput)) != "" {
				m.renameMode = false
				m.renameReady = true
				m.cancelBackgroundLoads()
				return m, tea.Quit
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
			m.renameTargetSet = true
		}
		return m, nil
	case tea.KeyBackspace, tea.KeyDelete:
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

// foldLeft folds the selected row's parent and moves the selection there. A
// worktree folds its checkout, a project folds its folder, and an expanded
// folder folds itself. A folded folder is already collapsed and does nothing.
// Search rows are flat, so folding keys only act while browsing folders.
func (m *Model) foldLeft() {
	if m.tab != ProjectsTab || string(m.query) != "" {
		return
	}
	current, ok := m.currentRow()
	if !ok {
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

// foldParent reports the row that Left folds from the selected row, or the
// empty string when Left has nothing to fold.
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
// size. No style in this package sets a background colour.
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
func (m Model) renderBody(height int) []string {
	lines := []string{}
	selectedLine := -1
	for _, row := range m.rows {
		if row.cursorable && row.id == m.cursor {
			selectedLine = len(lines)
		}
		primary, secondary := m.rowText(row)
		lines = append(lines, primary)
		if height >= 2 && secondary != "" && row.kind == itemRow {
			lines = append(lines, secondary)
		}
	}
	if height <= 0 {
		return nil
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
	out := []string{}
	for i := start; i < len(lines) && len(out) < height; i++ {
		out = append(out, fit(lines[i], m.width))
	}
	for len(out) < height {
		out = append(out, fit("", m.width))
	}
	return out
}
func (m Model) rowText(row row) (string, string) {
	selected := row.cursorable && row.id == m.cursor
	marker := "  "
	if selected {
		marker = accent().Render("▌") + " "
	}
	// Secondary agent details may occupy a second line, but the selection
	// accent belongs to the row's first line only. This keeps its height
	// constant while moving across every tab.
	switch row.kind {
	case machineRow:
		value := strings.Repeat("  ", row.depth) + bold().Render(highlightText(sanitize(row.label), string(m.query))) + muted().Render(fmt.Sprintf("  %d", row.count))
		if row.remoteErr != "" {
			value += muted().Render("  " + sanitize(row.remoteErr))
		} else if row.loading {
			value += muted().Faint(true).Render("  loading…")
		}
		return marker + value, ""
	case groupRow:
		arrow := "▸"
		if row.expanded {
			arrow = "⌄"
		}
		return marker + strings.Repeat("  ", row.depth) + bold().Render(arrow+"  "+abbreviateHome(row.label)) + muted().Render(fmt.Sprintf("  %d", row.count)), ""
	case newContainerRow:
		return "  " + strings.Repeat("  ", row.depth) + muted().Render(sanitize(row.label)), ""
	case separatorRow:
		return "", ""
	case itemRow:
		if row.itemKind == "agent" {
			return m.agentText(row, marker)
		}
		if row.itemKind == "container" {
			return m.containerText(row, marker)
		}
		return m.placeText(row, marker)
	default:
		return "", ""
	}
}

func (m Model) agentText(row row, marker string) (string, string) {
	a := row.agent
	container := displayContainer(a, row.agentMachine)
	primary := bold().Render(highlightText(sanitize(a.Harness), string(m.query))) + muted().Render("  "+highlightText(sanitize(container), string(m.query)))
	if row.id == m.cursor {
		primary = bold().Render(primary)
	}
	state := strings.ToLower(strings.TrimSpace(sanitize(a.State)))
	stateText := "● " + state
	if state == "stopped" && a.StoppedSince != nil {
		stateText += " " + stoppedAge(m.now(), *a.StoppedSince)
	}
	stateStyle := stateColor(a)
	left := stateStyle.Render(stateText)
	if a.Branch != "" {
		branch := highlightText("⎇ "+sanitize(a.Branch), string(m.query))
		if !strings.Contains(branch, "\x1b[") {
			branch = muted().Render(branch)
		}
		left += "   " + branch
	}
	targetText := highlightText(sanitize(a.Target), string(m.query))
	target := targetText
	if !strings.Contains(targetText, "\x1b[") {
		target = muted().Render(targetText)
	}
	gap := m.width - shell.StringWidth("  ") - shell.StringWidth(left) - shell.StringWidth(target)
	if gap < 2 {
		gap = 2
	}
	secondary := "  " + left + strings.Repeat(" ", gap) + target
	return marker + primary, secondary
}
func displayContainer(a capabilities.Agent, machine string) string {
	if machine != "" && machine != picker.LocalMachine() {
		return machine + "/" + a.Container
	}
	return a.Container
}
func (m Model) containerText(row row, marker string) (string, string) {
	name := sanitize(row.place.ContainerName)
	if row.nameWidth > 0 {
		name = shell.Truncate(name, row.nameWidth, "…")
		name += strings.Repeat(" ", max(0, row.nameWidth-shell.StringWidth(name)))
	}
	nameGutter := "  "
	marked := m.marked[picker.ContainerRef{Machine: row.place.Machine, ID: row.place.ContainerID}]
	if marked {
		nameGutter = magenta().Render("◆ ")
		name = highlightMarkedText(name, string(m.query))
	} else {
		name = highlightText(name, string(m.query))
	}
	name = nameGutter + name
	agents := m.containerAgents(row.place)
	full := make([]string, 0, len(agents))
	dots := ""
	for _, agent := range agents {
		state := strings.ToLower(strings.TrimSpace(sanitize(agent.State)))
		age := ""
		if state == "stopped" && agent.StoppedSince != nil {
			age = " " + stoppedAge(m.now(), *agent.StoppedSince)
		}
		full = append(full, stateColor(agent).Render("●")+" "+muted().Render(sanitize(agent.Harness)+" "+state+age))
		dots += stateColor(agent).Render("●")
	}
	activity := ""
	if row.activity != nil {
		activity = activityAge(m.now(), *row.activity)
	}
	text := name
	if row.place.Proxy {
		text += "  " + muted().Render("local proxy")
	}
	lineWidth := max(1, m.width-shell.StringWidth(marker))
	join := func(pills string, age string) string {
		value := text
		if pills != "" {
			value += "  " + pills
		}
		if age != "" {
			value += strings.Repeat(" ", max(2, lineWidth-shell.StringWidth(value)-shell.StringWidth(age))) + muted().Render(age)
		}
		return value
	}
	pills := strings.Join(full, "  ")
	line := join(pills, activity)
	if shell.StringWidth(line)+shell.StringWidth(marker) > m.width {
		line = join(dots, activity)
	}
	if shell.StringWidth(line)+shell.StringWidth(marker) > m.width {
		line = join("", activity)
	}
	if shell.StringWidth(line)+shell.StringWidth(marker) > m.width {
		line = join("", "")
	}
	available := max(1, m.width-shell.StringWidth(marker))
	if shell.StringWidth(line) > available {
		line = shell.Truncate(line, available, "…")
	}
	if row.id == m.cursor {
		line = bold().Render(line)
	}
	return marker + line, ""
}

func activityAge(now, activity time.Time) string { return stoppedAge(now, activity) }

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
	// costs; the folder column pays for its own gutter when it survives.
	base := max(0, m.width-(2+2))

	// Do not cap a row that already fits naturally.
	if maxName+maxGit <= base && (!layout.search || maxPath == 0 || maxName+maxGit+maxPath+2 <= base) {
		layout.name, layout.git = maxName, maxGit
		if layout.search && maxPath > 0 {
			layout.path = maxPath
		}
		return layout
	}

	// The name leads. It claims the longest visible name up to its share of
	// the row, but never drops below its floor.
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

	// The Git summary never truncates; the branch gives up cells first and the
	// name shrinks only after the summary floor is protected.
	gitFloor := max(maxSummary, min(maxGit, projectMinBranch))
	git := min(maxGit, max(base-nameTarget, 0))
	if git < gitFloor {
		nameTarget = min(nameTarget, max(nameFloor, base-gitFloor))
		git = min(maxGit, max(base-nameTarget, 0))
		if git < gitFloor {
			git = gitFloor
		}
	}

	// The Git column takes what it needs first; the folder gets only what is
	// left. It is right-aligned and left-truncated, so it can stop short of its
	// natural width without moving the name, and it is dropped before the name
	// gives up cells or the branch is cut.
	pathCost := 0
	if layout.search && maxPath > 0 {
		if leftover := base - nameTarget - git; leftover >= projectMinPath+2 {
			layout.path = min(maxPath, leftover-2)
			pathCost = layout.path + 2
		}
	}

	// The name keeps its target unless protecting the summary floor leaves it
	// with less room.
	layout.name = min(nameTarget, max(nameFloor, base-pathCost-git))
	layout.git = git
	layout.name = max(layout.name, 0)
	layout.git = max(layout.git, 0)
	return layout
}

func projectNameWidth(candidate row) int {
	width := shell.StringWidth(candidate.treePrefix + projectLabel(candidate.place))
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

func (m Model) placeText(row row, marker string) (string, string) {
	if row.place.Kind != picker.ProjectKind {
		return marker + highlightText(projectLabel(row.place), string(m.query)), ""
	}
	layout := m.projectLayout()
	name := row.treePrefix + projectLabel(row.place)
	count := ""
	if row.foldable && !row.expanded {
		count = fmt.Sprintf("  %d", row.count)
	}
	name = shell.Truncate(name, max(0, layout.name-shell.StringWidth(count)), "…")
	nameText := highlightText(name, string(m.query))
	if row.treePrefix != "" {
		// Tree gutters are structural, not part of fuzzy matching.
		nameText = muted().Render(row.treePrefix) + highlightText(strings.TrimPrefix(name, row.treePrefix), string(m.query))
	}
	if count != "" {
		nameText += muted().Render(count)
	}
	if row.id == m.cursor {
		nameText = bold().Render(nameText)
	}
	nameCell := fit(nameText, layout.name)
	gitCell := fit(m.projectGitStyled(row.place, layout.git), layout.git)
	line := ""
	if layout.search && layout.path > 0 {
		line = m.projectPathCell(row.group, layout.path) + "  "
	}
	line += nameCell + "  " + gitCell
	return marker + line, ""
}

// projectGitStyled renders the branch and its Git summary as one cell: the
// branch first, two spaces, then the summary. The branch truncates before the
// summary so change counts and divergence stay visible.
func (m Model) projectGitStyled(place picker.Place, width int) string {
	if width <= 0 {
		return ""
	}
	branch := highlightText(projectBranch(place), string(m.query))
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
func (m Model) projectPathCell(group string, width int) string {
	path := shell.TruncateLeft(abbreviateHome(group)+"/", width, "…")
	pathText := highlightText(path, string(m.query))
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
		right = magenta().Render(fmt.Sprintf("%d marked", count))
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
	return accent().Bold(true).Render(keyValue) + " " + muted().Faint(true).Render(label)
}

func sanitize(value string) string { return shell.Plain(value) }

func normalizedMachine(machine string) string {
	machine = strings.TrimSpace(sanitize(machine))
	if machine == "" || strings.EqualFold(machine, "local") {
		return picker.LocalMachine()
	}
	return machine
}

func fit(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = stripBackgroundResets(value)
	value = shell.Truncate(value, width, "")
	return value + strings.Repeat(" ", max(0, width-shell.StringWidth(value)))
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
func normal() lipgloss.Style      { return lipgloss.NewStyle().Foreground(lipgloss.Color("default")) }
func muted() lipgloss.Style       { return normal().Faint(true) }
func accent() lipgloss.Style      { return lipgloss.NewStyle().Foreground(lipgloss.Color("6")) }
func divergence() lipgloss.Style  { return lipgloss.NewStyle().Foreground(lipgloss.Color("6")) }
func magenta() lipgloss.Style     { return lipgloss.NewStyle().Foreground(lipgloss.Color("5")) }
func red() lipgloss.Style         { return lipgloss.NewStyle().Foreground(lipgloss.Color("1")) }
func green() lipgloss.Style       { return lipgloss.NewStyle().Foreground(lipgloss.Color("2")) }
func yellow() lipgloss.Style      { return lipgloss.NewStyle().Foreground(lipgloss.Color("3")) }
func brightGreen() lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color("10")) }
func brightRed() lipgloss.Style   { return lipgloss.NewStyle().Foreground(lipgloss.Color("9")) }
func bold() lipgloss.Style        { return normal().Bold(true) }
func highlightText(text, query string) string {
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
			out.WriteString(yellow().Bold(true).Render(string(r)))
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// highlightMarkedText keeps the required magenta foreground for every name
// character. Matching characters use bold and underline instead of a yellow
// foreground so the query remains visible without changing the mark colour.
func highlightMarkedText(text, query string) string {
	match, ok := picker.Rank(query, text)
	if !ok || len(match.Positions) == 0 {
		return magenta().Render(text)
	}
	positions := map[int]bool{}
	for _, pos := range match.Positions {
		positions[pos] = true
	}
	out := strings.Builder{}
	for i, r := range []rune(text) {
		style := magenta()
		if positions[i] {
			style = style.Bold(true).Underline(true)
		}
		out.WriteString(style.Render(string(r)))
	}
	return out.String()
}

func (m *Model) SetAgents(agents []capabilities.Agent) {
	m.agents = newAgentRecords(agents, "")
	m.rebuild(false)
}

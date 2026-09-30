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
	PreviewCommand       tea.Cmd
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
		ctx: ctx, deps: deps, tab: tab, agents: newAgentRecords(deps.Agents, ""),
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
	if m.deps.PreviewCommand != nil {
		cmds = append(cmds, m.deps.PreviewCommand)
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
				m.gitSlots <- struct{}{}
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

// SetQuery is useful to embedding callers and model tests. A changed query
// always selects the first matching item, never a group or machine header.
func (m *Model) SetQuery(query string) {
	m.query = []rune(query)
	m.resetSearchFolds()
	m.rebuild(false)
}

func (m *Model) resetSearchFolds() {
	m.folded = make(map[string]bool)
	m.expanded = make(map[string]bool)
	m.forceFirst = true
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
	text := strings.Join([]string{sanitize(place.ContainerName), sanitize(place.Machine), sanitize(place.Preview)}, " ")
	return picker.Rank(string(m.query), text)
}

func (m Model) matchProject(place picker.Place) (picker.Match, bool) {
	text := strings.Join([]string{sanitize(place.DisplayRemainder), sanitize(place.Path), sanitize(gitIdentity(place))}, " ")
	return picker.Rank(string(m.query), text)
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
	places := picker.ContainerPlaces(m.deps.Containers)
	places = append(places, m.remotePlaces...)
	filtered := places[:0]
	for i := range places {
		if places[i].Kind == picker.ContainerKind && places[i].Proxy {
			continue
		}
		filtered = append(filtered, places[i])
	}
	places = filtered
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
		sort.SliceStable(children, func(i, j int) bool { return m.containerBefore(children[i], children[j]) })
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

func (m Model) containerBefore(left, right row) bool {
	la, ra := m.containerAgents(left.place), m.containerAgents(right.place)
	lu, ru := oldestUnseen(la), oldestUnseen(ra)
	if !lu.IsZero() || !ru.IsZero() {
		if lu.IsZero() {
			return false
		}
		if ru.IsZero() {
			return true
		}
		return lu.Before(ru)
	}
	if left.activity == nil || right.activity == nil {
		if left.activity == nil && right.activity == nil {
			return left.label < right.label
		}
		return right.activity != nil
	}
	return left.activity.After(*right.activity)
}
func (m Model) containerAgents(place picker.Place) []capabilities.Agent { return m.agentsFor(place) }
func oldestUnseen(agents []capabilities.Agent) time.Time {
	var oldest time.Time
	for _, agent := range agents {
		if strings.EqualFold(sanitize(agent.State), "stopped") && !agent.Seen && agent.StoppedSince != nil && (oldest.IsZero() || agent.StoppedSince.Before(oldest)) {
			oldest = *agent.StoppedSince
		}
	}
	return oldest
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
	groups := make([]string, 0)
	seenGroups := map[string]bool{}
	for _, place := range places {
		if !seenGroups[place.Group] {
			seenGroups[place.Group] = true
			groups = append(groups, place.Group)
		}
	}
	rows := []row{}
	query := string(m.query)
	for _, group := range groups {
		roots := []picker.Place{}
		for _, place := range places {
			if place.Group == group && place.ParentPath == "" {
				roots = append(roots, place)
			}
		}
		matchedRoots := []picker.Place{}
		for _, root := range roots {
			if _, ok := m.matchProject(root); ok || m.hasMatchingChild(root, places) {
				matchedRoots = append(matchedRoots, root)
			}
		}
		sort.SliceStable(matchedRoots, func(i, j int) bool {
			return m.projectMatchScore(matchedRoots[i], places) > m.projectMatchScore(matchedRoots[j], places)
		})
		_, groupMatch := picker.Rank(query, sanitize(group))
		if query != "" && len(matchedRoots) == 0 && !groupMatch {
			continue
		}
		key := "group:" + group
		expanded := m.expanded[key]
		// Project groups start folded. A matching query unfolds only the
		// matching group, without changing the user's explicit fold state.
		if query != "" && len(matchedRoots) > 0 && !m.folded[key] {
			expanded = true
		}
		rows = append(rows, row{id: key, kind: groupRow, label: group, group: group, count: len(matchedRoots), cursorable: true, expanded: expanded, foldable: true})
		if !expanded {
			continue
		}
		for _, root := range matchedRoots {
			match, direct := m.matchProject(root)
			childMatch := m.hasMatchingChild(root, places)
			if !direct && query != "" && !childMatch {
				continue
			}
			children := m.children(root, places)
			fold := len(children) > 0
			projectKey := projectIdentity(root)
			projectExpanded := m.expanded[projectKey]
			if query != "" && childMatch && !m.folded[projectKey] {
				projectExpanded = true
			}
			rows = append(rows, row{id: projectKey, kind: itemRow, itemKind: "project", label: projectLabel(root), group: group, depth: 1, cursorable: true, matched: direct, score: match.Score, place: root, expanded: projectExpanded, foldable: fold})
			if !projectExpanded || (!childMatch && !direct) {
				continue
			}
			if !m.projectExpanded(root, query, childMatch) {
				continue
			}
			sort.SliceStable(children, func(i, j int) bool {
				return m.projectMatchScore(children[i], places) > m.projectMatchScore(children[j], places)
			})
			for _, child := range children {
				childMatchValue, childOK := m.matchProject(child)
				if query != "" && !childOK {
					continue
				}
				rows = append(rows, row{id: projectIdentity(child), kind: itemRow, itemKind: "project", label: projectLabel(child), group: group, depth: 2, cursorable: true, matched: childOK, score: childMatchValue.Score, place: child, foldable: len(m.children(child, places)) > 0, expanded: false})
			}
		}
	}
	return rows
}

func (m Model) projectExpanded(place picker.Place, query string, childMatch bool) bool {
	id := projectIdentity(place)
	if m.expanded[id] {
		return true
	}
	return query != "" && childMatch && !m.folded[id]
}
func (m Model) projectMatchScore(place picker.Place, all []picker.Place) int {
	best := -1
	if match, ok := m.matchProject(place); ok {
		best = match.Score
	}
	for _, child := range all {
		if child.ParentPath != place.Path {
			continue
		}
		if score := m.projectMatchScore(child, all); score > best {
			best = score
		}
	}
	return best
}

func (m Model) hasMatchingChild(place picker.Place, all []picker.Place) bool {
	for _, child := range all {
		if child.ParentPath == place.Path {
			if _, ok := m.matchProject(child); ok || m.hasMatchingChild(child, all) {
				return true
			}
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
	case picker.PreviewBatch:
		m.applyPreviews(msg)
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
		m.expanded[projectIdentity(*found)] = true
		m.expanded["project:"+found.ParentPath] = true
		m.expanded["group:"+found.Group] = true
		path = found.ParentPath
	}
}

func (m *Model) applyPreviews(msg picker.PreviewBatch) {
	for i := range m.deps.Containers {
		ref := picker.ContainerRef{Machine: normalizedMachine(m.deps.Containers[i].Machine), ID: m.deps.Containers[i].ID}
		if value, ok := msg.Previews[ref]; ok {
			m.deps.Containers[i].Preview = value
		}
	}
	for i := range m.remotePlaces {
		ref := picker.ContainerRef{Machine: normalizedMachine(m.remotePlaces[i].Machine), ID: m.remotePlaces[i].ContainerID}
		if value, ok := msg.Previews[ref]; ok {
			m.remotePlaces[i].Preview = value
		}
	}
	m.rebuild(false)
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
		return m, tea.Quit
	}
	if m.confirmKill {
		switch strings.ToLower(msg.String()) {
		case "y":
			m.confirmKill = false
			m.killReady = true
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
		return m, tea.Quit
	case tea.KeyEsc:
		if len(m.query) > 0 {
			m.query = nil
			m.resetSearchFolds()
			m.rebuild(false)
			return m, nil
		}
		m.cancelled = true
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
			m.query = m.query[:len(m.query)-1]
			m.resetSearchFolds()
			m.rebuild(false)
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
		m.toggleFold(false)
		return m, nil
	case tea.KeyRight:
		m.toggleFold(true)
		return m, nil
	case tea.KeyEnter:
		return m.accept()
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			m.query = append(m.query, msg.Runes...)
			m.resetSearchFolds()
			m.rebuild(false)
		}
		return m, nil
	case tea.KeySpace:
		m.query = append(m.query, ' ')
		m.resetSearchFolds()
		m.rebuild(false)
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
func (m *Model) toggleFold(expand bool) {
	current, ok := m.currentRow()
	if !ok || m.tab != ProjectsTab || !current.foldable {
		return
	}
	key := current.id
	if expand {
		delete(m.folded, key)
		m.expanded[key] = true
	} else {
		m.folded[key] = true
		delete(m.expanded, key)
	}
	m.rebuild(false)
	m.cursor = key
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
	secondaryMarker := "  "
	if selected {
		secondaryMarker = accent().Render("▌") + " "
	}
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
		return marker + strings.Repeat("  ", row.depth) + bold().Render(arrow+"  "+sanitize(row.label)) + muted().Render(fmt.Sprintf("  %d", row.count)), ""
	case newContainerRow:
		return "  " + strings.Repeat("  ", row.depth) + muted().Render(sanitize(row.label)), ""
	case separatorRow:
		return "", ""
	case itemRow:
		if row.itemKind == "agent" {
			return m.agentText(row, marker, secondaryMarker)
		}
		if row.itemKind == "container" {
			return m.containerText(row, marker)
		}
		return m.placeText(row, marker, secondaryMarker)
	default:
		return "", ""
	}
}

func (m Model) agentText(row row, marker, secondaryMarker string) (string, string) {
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
		left += "   " + muted().Render("⎇ "+highlightText(sanitize(a.Branch), string(m.query)))
	}
	target := muted().Render(highlightText(sanitize(a.Target), string(m.query)))
	gap := m.width - shell.StringWidth(secondaryMarker) - shell.StringWidth(left) - shell.StringWidth(target)
	if gap < 2 {
		gap = 2
	}
	secondary := secondaryMarker + left + strings.Repeat(" ", gap) + target
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

func (m Model) placeText(row row, marker, secondaryMarker string) (string, string) {
	place := row.place
	name := highlightText(projectLabel(place), string(m.query))
	if place.Kind == picker.ContainerKind && m.marked[picker.ContainerRef{Machine: place.Machine, ID: place.ContainerID}] {
		name = magenta().Render("◆ " + name)
	} else if row.depth > 1 {
		name = muted().Render("└ ") + name
	}
	primary := strings.Repeat("  ", row.depth-1) + name
	if row.id == m.cursor {
		primary = bold().Render(primary)
	}
	secondary := ""
	switch place.Kind {
	case picker.ContainerKind:
		secondary = secondaryMarker + m.containerSecondary(place)
	case picker.ProjectKind:
		secondary = secondaryMarker + m.gitSecondary(place)
	}
	return marker + primary, secondary
}
func (m Model) containerSecondary(place picker.Place) string {
	agents := m.agentsFor(place)
	if len(agents) == 0 {
		if preview := previewLine(place.Preview); preview != "" {
			return muted().Render(highlightText(preview, string(m.query)))
		}
		return muted().Faint(true).Render("no agents")
	}
	parts := []string{}
	for _, agent := range agents {
		state := strings.ToLower(strings.TrimSpace(sanitize(agent.State)))
		text := sanitize(strings.TrimSpace(agent.Harness)) + " " + sanitize(state)
		if state == "stopped" && agent.StoppedSince != nil {
			text += " " + stoppedAge(m.now(), *agent.StoppedSince)
		}
		parts = append(parts, stateColor(agent).Render("●")+" "+muted().Render(text))
	}
	return strings.Join(parts, "  ")
}
func (m Model) gitSecondary(place picker.Place) string {
	if errText := sanitize(m.gitErrors[place.Path]); errText != "" {
		return muted().Render("git: error " + errText)
	}
	if place.GitContext == nil {
		if place.Git {
			return muted().Faint(true).Render("git context loading…")
		}
		return ""
	}
	g := *place.GitContext
	parts := []string{}
	if g.Identity != "" {
		parts = append(parts, muted().Render("⎇ "+highlightText(sanitize(g.Identity), string(m.query))))
	}
	if g.Adds > 0 {
		parts = append(parts, brightGreen().Render("+"+itoa(g.Adds)))
	}
	if g.Deletes > 0 {
		parts = append(parts, brightRed().Render("-"+itoa(g.Deletes)))
	}
	if g.Dirty && g.Adds == 0 && g.Deletes == 0 {
		parts = append(parts, yellow().Render("*"))
	}
	if g.Ahead > 0 {
		parts = append(parts, muted().Render("↑"+itoa(g.Ahead)))
	}
	if g.Behind > 0 {
		parts = append(parts, muted().Render("↓"+itoa(g.Behind)))
	}
	if len(parts) == 0 {
		return muted().Faint(true).Render("clean")
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
		if current, ok := m.currentRow(); ok && current.foldable {
			left = append(left, hint("←→", "fold"))
		}
	}
	if m.currentContainer() || len(m.markedTargets()) > 0 {
		left = append(left, hint("Ctrl-X", "kill"))
	}
	if m.currentContainer() {
		left = append(left, hint("Ctrl-R", "rename"), hint("Ctrl-T", "mark"))
	}
	leftText := strings.Join(left, "  ")
	right := ""
	if count := len(m.markedTargets()); count > 0 {
		right = magenta().Render(fmt.Sprintf("%d marked", count))
	}
	if right != "" {
		maxLeft := m.width - shell.StringWidth(right) - 1
		leftText = shell.Truncate(leftText, maxLeft, "…")
		return fit(leftText+strings.Repeat(" ", max(1, m.width-shell.StringWidth(leftText)-shell.StringWidth(right)))+right, m.width)
	}
	return fit(leftText, m.width)
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
func itoa(value int) string { return fmt.Sprintf("%d", value) }
func previewLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(sanitize(line))
		if line != "" {
			return line
		}
	}
	return ""
}
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

func (m *Model) SetAgents(agents []capabilities.Agent) {
	m.agents = newAgentRecords(agents, "")
	m.rebuild(false)
}

package tui

// Source loading is the TUI's source/reload module. Its interface is the set
// of Bubble Tea commands and source messages consumed by the coordinator; the
// implementation keeps generation checks and replacement sequencing local.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/picker"
)

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
		for _, place := range m.projection().projectPlaces() {
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

func (m Model) agentsGeneration() int { return m.generationState().agents }

func (m Model) containersGeneration() int { return m.generationState().containers }

func (m Model) remoteGeneration() int { return m.generationState().remotes }

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

// syncCatalog projects one catalog snapshot into the source module's
// row-oriented cache. The catalog remains the policy module; these records
// keep source replacement and machine normalization local to this seam.
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

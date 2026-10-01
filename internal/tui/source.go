package tui

// Source loading is a deep module behind the coordinator's result seam. It
// owns generations, in-flight state, loading presentation, and replacement
// sequencing. The coordinator only asks it for commands and applies the
// accepted change by rebuilding its rows.

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/startup"
)

type sourceGenerations struct {
	projects   int
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

type sourceUpdate struct {
	changed         bool
	reloadDone      bool
	toast           string
	cmd             tea.Cmd
	projects        []picker.Place
	projectsUpdated bool
	pullRequest     *pullRequestMsg
}

// sourceState owns all asynchronous source state. It deliberately keeps the
// catalog as its replacement target: callers cannot accidentally apply a
// result without the generation check that belongs to the source.
type sourceState struct {
	ctx          context.Context
	catalog      *picker.Catalog
	agents       []agentRecord
	remoteAgents []agentRecord

	projectSource        func(context.Context) ([]picker.Place, error)
	agentSource          capabilities.AgentSource
	containerSource      capabilities.ContainerLister
	agentRefreshInterval time.Duration
	gitLoader            picker.GitLoader
	pullRequestLoader    picker.PullRequestLoader
	remoteCommands       []tea.Cmd

	remoteMachines []string
	remoteLoaded   map[string]bool
	groupErrors    map[string]string
	groupWarnings  map[string]string
	refreshErr     error

	generations     sourceGenerations
	refreshing      bool
	pendingReload   reloadState
	cancelLoads     context.CancelFunc
	gitSlots        chan struct{}
	prSlots         chan struct{}
	trace           *startup.Trace
	projectWarnings func() []string
}

func newSource(ctx context.Context, catalog *picker.Catalog, deps Deps, cancel context.CancelFunc, remoteMachines []string) *sourceState {
	if deps.AgentRefreshInterval <= 0 {
		deps.AgentRefreshInterval = 2 * time.Second
	}
	return &sourceState{
		ctx: ctx, catalog: catalog, agents: newAgentRecords(deps.Agents, ""),
		projectSource: deps.ProjectSource, agentSource: deps.AgentSource, containerSource: deps.ContainerSource,
		agentRefreshInterval: deps.AgentRefreshInterval, gitLoader: deps.GitLoader,
		pullRequestLoader: deps.PullRequestLoader,
		remoteCommands:    append([]tea.Cmd(nil), deps.RemoteCommands...),
		remoteMachines:    append([]string(nil), remoteMachines...),
		remoteLoaded:      make(map[string]bool), groupErrors: make(map[string]string),
		groupWarnings: make(map[string]string), refreshErr: deps.AgentError,
		cancelLoads: cancel, gitSlots: make(chan struct{}, gitConcurrency(deps.GitLoader)),
		prSlots: make(chan struct{}, pullRequestConcurrency(deps.PullRequestLoader)), trace: deps.StartupTrace,
		projectWarnings: deps.ProjectWarnings,
	}
}

// Init creates the initial source commands. Git discovery receives the same
// project snapshot as the projection, while source results still come through
// Accept so stale generations have one home.
func (s *sourceState) Init(projects []picker.Place) tea.Cmd {
	cmds := make([]tea.Cmd, 0, 4+len(s.remoteCommands))
	if s.projectSource != nil {
		s.generations.projects++
		cmds = append(cmds, s.refreshProjectsAt(s.generations.projects))
	}
	if s.containerSource != nil {
		cmds = append(cmds, s.refreshContainersAt(s.generations.containers))
	}
	if s.agentSource != nil {
		s.refreshing = true
		cmds = append(cmds, s.refreshAgentsAt(s.generations.agents), s.tick())
	}
	for _, command := range s.remoteCommands {
		cmds = append(cmds, s.remoteCommand(command, s.generations.remotes))
	}
	cmds = append(cmds, s.gitCommands(projects)...)
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (s *sourceState) cancel() {
	if s.cancelLoads != nil {
		s.cancelLoads()
		s.cancelLoads = nil
	}
}

func (s *sourceState) refreshProjectsAt(generation int) tea.Cmd {
	return func() tea.Msg {
		s.trace.Mark("projects-start")
		projects, err := s.projectSource(s.ctx)
		var warnings []string
		if s.projectWarnings != nil {
			warnings = s.projectWarnings()
		}
		s.trace.Mark("projects-done")
		return projectsMsg{generation: generation, projects: projects, warnings: warnings, err: err}
	}
}

func (s *sourceState) refreshAgentsAt(generation int) tea.Cmd {
	return func() tea.Msg {
		s.trace.Mark("agents-start")
		agents, err := s.agentSource.ListAgents(s.ctx)
		s.trace.Mark("agents-done")
		return agentsMsg{generation: generation, agents: agents, err: err}
	}
}

func (s *sourceState) refreshContainersAt(generation int) tea.Cmd {
	return func() tea.Msg {
		s.trace.Mark("containers-start")
		containers, err := s.containerSource.ListContainers(s.ctx)
		s.trace.Mark("containers-done")
		return containersMsg{generation: generation, containers: containers, err: err}
	}
}

func (s *sourceState) remoteCommand(command tea.Cmd, generation int) tea.Cmd {
	return func() tea.Msg {
		s.trace.Mark("remote-start")
		msg := command()
		s.trace.Mark("remote-done")
		batch, ok := msg.(picker.RemoteBatch)
		if !ok {
			return msg
		}
		return remoteMsg{generation: generation, batch: batch}
	}
}

// Reload starts a coordinated replacement. Every command carries the
// generation captured here; Accept is the only place that can consume it.
func (s *sourceState) Reload() tea.Cmd {
	pending := reloadState{}
	cmds := make([]tea.Cmd, 0, 2+len(s.remoteCommands))
	if s.containerSource != nil {
		s.generations.containers++
		pending.containers = 1
		cmds = append(cmds, s.refreshContainersAt(s.generations.containers))
	}
	if s.agentSource != nil {
		s.generations.agents++
		pending.agents = 1
		s.refreshing = true
		cmds = append(cmds, s.refreshAgentsAt(s.generations.agents))
	}
	if len(s.remoteCommands) > 0 {
		s.generations.remotes++
		pending.remotes = len(s.remoteCommands)
		for machine := range s.remoteLoaded {
			s.remoteLoaded[machine] = false
			delete(s.groupErrors, machine)
			delete(s.groupWarnings, machine)
		}
		for _, command := range s.remoteCommands {
			cmds = append(cmds, s.remoteCommand(command, s.generations.remotes))
		}
	}
	if pending.done() {
		s.pendingReload = reloadState{}
		return nil
	}
	s.pendingReload = pending
	return tea.Batch(cmds...)
}

func (s *sourceState) finishReload(source string) {
	switch source {
	case "containers":
		s.pendingReload.containers = 0
	case "agents":
		s.pendingReload.agents = 0
	case "remotes":
		if s.pendingReload.remotes > 0 {
			s.pendingReload.remotes--
		}
	}
	if s.pendingReload.done() {
		s.pendingReload = reloadState{}
	}
}

func (s *sourceState) pending() bool { return !s.pendingReload.done() }

func (s *sourceState) tick() tea.Cmd {
	return tea.Tick(s.agentRefreshInterval, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

// Tick starts a refresh only when no coordinated reload or refresh is active.
func (s *sourceState) Tick() tea.Cmd {
	if s.agentSource == nil {
		return nil
	}
	if s.pending() || s.refreshing {
		return s.tick()
	}
	s.generations.agents++
	s.refreshing = true
	return tea.Batch(s.refreshAgentsAt(s.generations.agents), s.tick())
}

// Accept is the source result-acceptance interface. A false result means the
// message was stale or unrelated; generation and in-flight handling never
// leaks into Model.Update.
func (s *sourceState) Accept(msg tea.Msg) (sourceUpdate, bool) {
	switch msg := msg.(type) {
	case projectsMsg:
		if msg.generation != s.generations.projects {
			return sourceUpdate{}, false
		}
		if msg.err != nil {
			return sourceUpdate{toast: "project discovery: " + msg.err.Error()}, true
		}
		if s.catalog != nil {
			s.catalog.SetProjects(msg.projects)
			s.syncCatalog()
		}
		toast := ""
		if len(msg.warnings) > 0 {
			toast = "project warning: " + msg.warnings[0]
			if len(msg.warnings) > 1 {
				toast += fmt.Sprintf(" (+%d more)", len(msg.warnings)-1)
			}
		}
		return sourceUpdate{changed: true, toast: toast, projects: msg.projects, projectsUpdated: true, cmd: tea.Batch(s.gitCommands(msg.projects)...)}, true
	case agentsMsg:
		pendingBefore := s.pending()
		if msg.generation != s.generations.agents {
			return sourceUpdate{}, false
		}
		s.refreshing = false
		if msg.err != nil {
			s.refreshErr = msg.err
			s.finishReload("agents")
			return sourceUpdate{reloadDone: pendingBefore && !s.pending()}, true
		}
		s.refreshErr = nil
		s.agents = newAgentRecords(msg.agents, "")
		if s.catalog != nil {
			s.catalog.ReplaceLocalAgents(msg.agents)
			s.syncCatalog()
		}
		s.finishReload("agents")
		return sourceUpdate{changed: true, reloadDone: pendingBefore && !s.pending()}, true
	case containersMsg:
		pendingBefore := s.pending()
		if msg.generation != s.generations.containers {
			return sourceUpdate{}, false
		}
		if msg.err != nil {
			s.finishReload("containers")
			return sourceUpdate{reloadDone: pendingBefore && !s.pending(), toast: "container refresh: " + msg.err.Error()}, true
		}
		if s.catalog != nil {
			s.catalog.ReplaceContainers(msg.containers)
			s.syncCatalog()
		}
		s.finishReload("containers")
		return sourceUpdate{changed: true, reloadDone: pendingBefore && !s.pending()}, true
	case remoteMsg:
		pendingBefore := s.pending()
		if msg.generation != s.generations.remotes {
			return sourceUpdate{}, false
		}
		s.applyRemote(msg.batch)
		s.finishReload("remotes")
		return sourceUpdate{changed: true, reloadDone: pendingBefore && !s.pending()}, true
	case pullRequestMsg:
		if msg.generation != s.generations.projects {
			return sourceUpdate{}, false
		}
		// A zero request and a failed command are both silent: the accepted
		// update clears any badge the row still carries.
		return sourceUpdate{pullRequest: &msg, changed: true}, true
	case picker.RemoteBatch:
		pendingBefore := s.pending()
		s.applyRemote(msg)
		s.finishReload("remotes")
		return sourceUpdate{changed: true, reloadDone: pendingBefore && !s.pending()}, true
	default:
		return sourceUpdate{}, false
	}
}

func (s *sourceState) applyRemote(msg picker.RemoteBatch) {
	if s.catalog == nil {
		return
	}
	s.catalog.ApplyRemote(msg)
	s.syncCatalog()
}

// syncCatalog projects one catalog snapshot into source-owned records and
// loading presentation. The catalog remains the policy module.
func (s *sourceState) syncCatalog() {
	if s.catalog == nil {
		return
	}
	snapshot := s.catalog.Snapshot()
	local := picker.LocalMachine()
	s.agents = nil
	s.remoteAgents = nil
	for _, record := range snapshot.Agents {
		value := agentRecord{agent: record.Agent, machine: record.Machine, backend: record.Backend}
		if record.Machine == local {
			s.agents = append(s.agents, value)
		} else {
			s.remoteAgents = append(s.remoteAgents, value)
		}
	}
	s.remoteMachines = nil
	s.remoteLoaded = make(map[string]bool)
	s.groupErrors = make(map[string]string)
	s.groupWarnings = make(map[string]string)
	for _, machine := range snapshot.Machines {
		if machine == local {
			continue
		}
		s.remoteMachines = append(s.remoteMachines, machine)
		status := snapshot.Status[machine]
		s.remoteLoaded[machine] = status.Loaded
		s.groupErrors[machine] = sanitize(status.Error)
		s.groupWarnings[machine] = sanitize(status.Warning)
	}
}

// pullRequestCommand loads the pull request decoration for one checkout. It
// is scheduled only after that checkout's Git context was accepted, runs
// within its own bounded slot pool, and carries the project generation so a
// stale answer cannot decorate a replaced project snapshot.
func (s *sourceState) pullRequestCommand(path string) tea.Cmd {
	if s.pullRequestLoader == nil || path == "" {
		return nil
	}
	generation := s.generations.projects
	return func() tea.Msg {
		s.trace.Mark("pull-request-start")
		select {
		case s.prSlots <- struct{}{}:
			if s.ctx.Err() != nil {
				<-s.prSlots
				return nil
			}
		case <-s.ctx.Done():
			return nil
		}
		defer func() { <-s.prSlots }()
		value, err := s.pullRequestLoader.Load(s.ctx, path)
		s.trace.Mark("pull-request-done")
		return pullRequestMsg{generation: generation, path: path, pullRequest: value, err: err}
	}
}

func (s *sourceState) gitCommands(projects []picker.Place) []tea.Cmd {
	if s.gitLoader == nil {
		return nil
	}
	seenGit := map[string]bool{}
	cmds := make([]tea.Cmd, 0, len(projects))
	for _, place := range projects {
		if !place.Git || place.Path == "" || seenGit[place.Path] {
			continue
		}
		seenGit[place.Path] = true
		place := place
		cmds = append(cmds, func() tea.Msg {
			s.trace.Mark("git-start")
			select {
			case s.gitSlots <- struct{}{}:
				if s.ctx.Err() != nil {
					<-s.gitSlots
					return nil
				}
			case <-s.ctx.Done():
				return nil
			}
			defer func() { <-s.gitSlots }()
			value, err := s.gitLoader.Load(s.ctx, place.Path)
			s.trace.Mark("git-done")
			return picker.GitContextMsg{Path: place.Path, Context: value, Err: err}
		})
	}
	return cmds
}

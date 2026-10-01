package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

func TestStandaloneSpaceIsSearchAndRenameInput(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ContainersTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if model.Query() != " " {
		t.Fatalf("space query = %q", model.Query())
	}
	model = NewModel(context.Background(), richDeps(), ContainersTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlR})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if got := string(model.renameInput); got != " x" {
		t.Fatalf("rename input = %q", got)
	}
}

func TestSearchRevealsPreviouslyFoldedProjects(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	model.SetQuery("nexo")
	if !strings.HasPrefix(model.Cursor(), "project:") {
		t.Fatalf("cursor = %q, rows = %v", model.Cursor(), model.Rows())
	}
}

func TestSearchIsFlatAndIgnoresGroupFolding(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ProjectsTab)
	model.SetQuery("nexo-wt")
	if got := model.Rows(); len(got) != 1 || got[0] != "project:/work/nexo-wt" {
		t.Fatalf("query rows = %v, want the matching worktree only", got)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.Rows(); len(got) != 1 || got[0] != "project:/work/nexo-wt" {
		t.Fatalf("fold key changed flat search rows: %v", got)
	}
	model.SetQuery("")
	if got := model.Rows(); len(got) != 1 || got[0] != "group:/work" {
		t.Fatalf("empty-query rows = %v, want folded folder", got)
	}
	// Right unfolds the folder; Left on the expanded folder folds it again.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if got := model.Rows(); len(got) != 3 {
		t.Fatalf("empty-query unfold rows = %v", got)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.Rows(); len(got) != 1 || got[0] != "group:/work" {
		t.Fatalf("empty-query re-fold rows = %v", got)
	}
}

func TestGitLoadPreservesExpandedGroupState(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if got := model.Rows(); len(got) != 3 {
		t.Fatalf("initial rows = %v, want unfolded folder", got)
	}
	model = updateModel(t, model, picker.GitContextMsg{Path: "/work/nexo", Context: picker.GitContext{Identity: "main"}})
	if got := model.Rows(); len(got) != 3 {
		t.Fatalf("Git load changed expanded group: %v", got)
	}
}

func TestGitDiscoveryRevealsSelectedWorktreeAndRendersStates(t *testing.T) {
	model := NewModel(context.Background(), Deps{Projects: []picker.Place{
		{Kind: picker.ProjectKind, Path: "/p", Group: "/work", DisplayRemainder: "primary", Git: true},
		{Kind: picker.ProjectKind, Path: "/w", Group: "/work", DisplayRemainder: "linked", Git: true},
	}}, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.Cursor() != "project:/w" {
		t.Fatalf("selected before discovery = %q", model.Cursor())
	}
	model = updateModel(t, model, picker.GitContextMsg{Path: "/w", Context: picker.GitContext{PrimaryPath: "/p"}})
	if model.Cursor() != "project:/w" {
		t.Fatalf("selected after discovery = %q, rows=%v", model.Cursor(), model.Rows())
	}
	model.SetSize(100, 8)
	model = updateModel(t, model, picker.GitContextMsg{Path: "/w", Err: errors.New("denied")})
	if !strings.Contains(shell.Plain(model.View()), "git: error denied") {
		t.Fatalf("Git error not rendered: %q", model.View())
	}
	model = updateModel(t, model, picker.GitContextMsg{Path: "/w", Context: picker.GitContext{Identity: "main", Dirty: true}})
	if !strings.Contains(shell.Plain(model.View()), "*") {
		t.Fatalf("dirty marker not rendered: %q", model.View())
	}
}

func TestReloadDropsOlderSourceResults(t *testing.T) {
	local := picker.LocalMachine()
	model := NewModel(context.Background(), Deps{
		Containers:     []capabilities.Container{{ID: "old", Name: "old", Machine: local}},
		Agents:         []capabilities.Agent{{PaneID: "old", Target: "old", Harness: "pi", State: "working"}},
		RemoteMachines: []string{"dev"},
	}, ContainersTab)
	model.generations.containers = 1
	model = updateModel(t, model, containersMsg{generation: 1, containers: []capabilities.Container{{ID: "new", Name: "new", Machine: local}}})
	model = updateModel(t, model, containersMsg{generation: 0, containers: []capabilities.Container{{ID: "old", Name: "old", Machine: local}}})
	if !strings.Contains(strings.Join(model.Rows(), "\x00"), "new") || strings.Contains(strings.Join(model.Rows(), "\x00"), "old") {
		t.Fatalf("stale container result replaced newer snapshot: %v", model.Rows())
	}

	model.generations.agents = 1
	model = updateModel(t, model, agentsMsg{generation: 1, agents: []capabilities.Agent{{PaneID: "new", Target: "new", Harness: "pi", State: "working"}}})
	model = updateModel(t, model, agentsMsg{generation: 0, agents: []capabilities.Agent{{PaneID: "old", Target: "old", Harness: "pi", State: "working"}}})
	if len(model.agents) != 1 || model.agents[0].agent.Target != "new" {
		t.Fatalf("stale agent result replaced newer snapshot: %#v", model.agents)
	}

	model.generations.remotes = 1
	model = updateModel(t, model, remoteMsg{generation: 1, batch: picker.RemoteBatch{Machine: "dev", Agents: []capabilities.Agent{{PaneID: "new", Target: "new", State: "working"}}}})
	model = updateModel(t, model, remoteMsg{generation: 0, batch: picker.RemoteBatch{Machine: "dev", Agents: []capabilities.Agent{{PaneID: "old", Target: "old", State: "working"}}}})
	if len(model.remoteAgents) != 1 || model.remoteAgents[0].agent.Target != "new" {
		t.Fatalf("stale remote result replaced newer snapshot: %#v", model.remoteAgents)
	}
}

type fixedContainerSource struct {
	containers []capabilities.Container
	err        error
}

func (source fixedContainerSource) ListContainers(context.Context) ([]capabilities.Container, error) {
	return source.containers, source.err
}

func TestContainerRefreshErrorAfterKillIsRetryable(t *testing.T) {
	local := picker.LocalMachine()
	deps := Deps{
		Containers:      []capabilities.Container{{ID: "one", Name: "one", Machine: local}},
		ContainerSource: fixedContainerSource{},
		KillContainers: func(context.Context, []picker.ContainerRef) error {
			return nil
		},
	}
	model := NewModel(context.Background(), deps, ContainersTab)
	updated, _ := model.Update(keyMsg(tea.KeyCtrlX))
	model = updated.(Model)
	updated, action := model.Update(keyMsg(tea.KeyRunes, 'y'))
	model = updated.(Model)
	if action == nil {
		t.Fatal("kill command missing")
	}
	updated, reload := model.Update(action())
	model = updated.(Model)
	if reload == nil || !model.actionInFlight {
		t.Fatalf("post-kill reload not in flight: action=%t command=%v", model.actionInFlight, reload)
	}
	model = updateModel(t, model, containersMsg{generation: model.containersGeneration(), err: errors.New("backend unavailable")})
	if model.actionInFlight {
		t.Fatal("action stayed blocked after refresh error")
	}
	if !strings.Contains(model.toast, "container refresh: backend unavailable") {
		t.Fatalf("refresh error toast = %q", model.toast)
	}
	model = updateModel(t, model, keyMsg(tea.KeyCtrlX))
	if !model.confirmKill {
		t.Fatal("kill was not retryable after refresh error")
	}
}

func TestBulkKillAtBottomKeepsNearestRemainingCursor(t *testing.T) {
	local := picker.LocalMachine()
	deps := Deps{
		Containers: []capabilities.Container{
			{ID: "one", Name: "one", Machine: local},
			{ID: "two", Name: "two", Machine: local},
			{ID: "three", Name: "three", Machine: local},
		},
		ContainerSource: fixedContainerSource{},
		KillContainers:  func(context.Context, []picker.ContainerRef) error { return nil },
	}
	model := NewModel(context.Background(), deps, ContainersTab)
	model.cursor = containerIdentity(picker.Place{Machine: local, ContainerID: "three"})
	updated, _ := model.Update(keyMsg(tea.KeyCtrlX))
	model = updated.(Model)
	updated, action := model.Update(keyMsg(tea.KeyRunes, 'y'))
	model = updated.(Model)
	updated, _ = model.Update(action())
	model = updated.(Model)
	model = updateModel(t, model, containersMsg{generation: model.containersGeneration(), containers: deps.Containers[:2]})
	want := containerIdentity(picker.Place{Machine: local, ContainerID: "two"})
	if model.Cursor() != want {
		t.Fatalf("cursor after bottom bulk kill = %q, want %q; rows=%v", model.Cursor(), want, model.Rows())
	}
}

func TestRemoteReloadShowsLoadingHeader(t *testing.T) {
	model := NewModel(context.Background(), Deps{RemoteMachines: []string{"dev"}}, ContainersTab)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Places: picker.ContainerPlaces([]capabilities.Container{{ID: "one", Name: "one", Machine: "dev"}})})
	model.deps.RemoteCommands = []tea.Cmd{func() tea.Msg {
		return picker.RemoteBatch{Machine: "dev"}
	}}
	model.SetSize(100, 10)
	model = updateModel(t, model, actionMsg{kind: "kill"})
	if !strings.Contains(shell.Plain(model.View()), "loading…") {
		t.Fatalf("remote reload header did not show loading: %q", model.View())
	}
}

func TestRemoteSnapshotsReplaceOpaqueAgentRecords(t *testing.T) {
	model := NewModel(context.Background(), Deps{}, AgentsTab)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Agents: []capabilities.Agent{{PaneID: "%7", Target: "opaque-a", State: "working"}}})
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Agents: []capabilities.Agent{{PaneID: "%7", Target: "opaque-b", State: "stopped"}}})
	if len(model.remoteAgents) != 1 || model.remoteAgents[0].agent.Target != "opaque-b" || model.remoteAgents[0].agent.State != "stopped" {
		t.Fatalf("remote snapshot retained stale record: %#v", model.remoteAgents)
	}
}

// snapshotAgents reads agent association from the materialized place snapshot
// the container rows render from.
func snapshotAgents(model Model, machine, id string) []capabilities.Agent {
	for _, candidate := range model.projection().Rows(ContainersTab) {
		if candidate.itemKind == "container" && candidate.place.Machine == machine && candidate.place.ContainerID == id {
			return candidate.place.Agents
		}
	}
	return nil
}

func TestRemoteLoadCompletionAndMachineScopedAgents(t *testing.T) {
	model := NewModel(context.Background(), Deps{
		Containers:     []capabilities.Container{{ID: "1", Name: "local"}, {ID: "1", Name: "remote", Machine: "dev"}},
		Agents:         []capabilities.Agent{{PaneID: "local-pane", ContainerID: "1", Container: "local", Harness: "pi", State: "working"}},
		RemoteMachines: []string{"dev"},
	}, ContainersTab)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Agents: []capabilities.Agent{{PaneID: "%1", Target: "old-target", ContainerID: "1", Container: "remote", Harness: "claude", State: "working"}}})
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Agents: []capabilities.Agent{{PaneID: "%1", ContainerID: "1", Container: "remote", Harness: "claude", State: "stopped"}}})
	if len(model.remoteAgents) != 1 || model.remoteAgents[0].agent.State != "stopped" {
		t.Fatalf("nonempty remote snapshot was not replaced: %#v", model.remoteAgents)
	}
	if got := len(snapshotAgents(model, "dev", "1")); got != 1 {
		t.Fatalf("remote association count = %d", got)
	}
	if got := len(snapshotAgents(model, picker.LocalMachine(), "1")); got != 1 {
		t.Fatalf("local association count = %d", got)
	}
	model.SetSize(100, 12)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev"})
	if strings.Contains(shell.Plain(model.View()), "loading…") {
		t.Fatal("successful empty remote listing stayed loading")
	}
	model.SetQuery("does-not-match")
	if strings.Contains(shell.Plain(model.View()), "loading…") {
		t.Fatal("loaded filtered remote machine showed loading")
	}
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Agents: []capabilities.Agent{{PaneID: "%1", Target: "raw-target", Harness: "claude", State: "stopped"}}})
	if len(model.remoteAgents) != 1 || model.remoteAgents[0].agent.State != "stopped" {
		t.Fatalf("remote snapshot = %#v", model.remoteAgents)
	}
}

func TestAgentsAreGroupedByMachineAndSearchKeepsHeader(t *testing.T) {
	model := NewModel(context.Background(), Deps{
		Agents: []capabilities.Agent{
			{PaneID: "remote", Container: "remote-box", Harness: "pi", State: "working"},
			{PaneID: "local", Container: "local-box", Harness: "claude", State: "working"},
		},
		RemoteMachines: []string{"dev"},
	}, AgentsTab)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Agents: []capabilities.Agent{{PaneID: "remote", Container: "remote-box", Harness: "pi", State: "working"}}})
	rows := model.Rows()
	if rows[0] != "machine:"+picker.LocalMachine() || !strings.Contains(strings.Join(rows, "\x00"), "machine:dev") {
		t.Fatalf("machine order = %v", rows)
	}
	model.SetQuery("remote-box")
	if !strings.Contains(strings.Join(model.Rows(), "\x00"), "machine:dev") {
		t.Fatalf("matching machine header missing = %v", model.Rows())
	}
	model.SetSize(100, 12)
	plain := shell.Plain(model.View())
	if strings.Contains(plain, "dev/remote-box") {
		t.Fatalf("agent container retained machine prefix: %q", plain)
	}
}

func TestAgentBatchCompatibility(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), AgentsTab)
	model = updateModel(t, model, picker.AgentBatch{Agents: []capabilities.Agent{{PaneID: "new", Target: "new:1", Harness: "codex", State: "working"}}})
	if !strings.Contains(strings.Join(model.Rows(), "\x00"), "new") {
		t.Fatalf("AgentBatch was ignored: %v", model.Rows())
	}
}

func TestRemoteAgentFocusReferenceKeepsMachineAndBackend(t *testing.T) {
	model := NewModel(context.Background(), Deps{Agents: []capabilities.Agent{{PaneID: "%7", Target: "local:1", Harness: "pi", State: "working"}}}, AgentsTab)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Backend: "herdr", Agents: []capabilities.Agent{{PaneID: "%7", Target: "opaque", ContainerID: "remote", Container: "remote", Harness: "claude", State: "working"}}})
	ids := model.Rows()
	for _, id := range ids {
		if strings.HasPrefix(id, "agent:dev:") {
			model.cursor = id
		}
	}
	if !strings.HasPrefix(model.cursor, "agent:dev:") {
		t.Fatalf("remote agent rows = %v", ids)
	}
	accepted, _ := model.accept()
	model = accepted.(Model)
	if model.selectedAgentRef.Machine != "dev" || model.selectedAgentRef.Backend != "herdr" || model.selectedAgentRef.Agent.PaneID != "%7" {
		t.Fatalf("agent ref = %#v", model.selectedAgentRef)
	}
}

func TestRemoteAgentIdentityIncludesMachine(t *testing.T) {
	model := NewModel(context.Background(), Deps{Agents: []capabilities.Agent{{PaneID: "%1", Target: "local:1", Harness: "pi", State: "working"}}}, AgentsTab)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Agents: []capabilities.Agent{{PaneID: "%1", Target: "raw-target", Harness: "claude", State: "working"}}})
	rows := model.Rows()
	agents := make([]string, 0, 2)
	for _, row := range rows {
		if strings.HasPrefix(row, "agent:") {
			agents = append(agents, row)
		}
	}
	if len(agents) != 2 || agents[0] == agents[1] {
		t.Fatalf("agent identities = %v", rows)
	}
}

func TestCtrlCClosesModals(t *testing.T) {
	for _, start := range []tea.KeyType{tea.KeyCtrlR, tea.KeyCtrlX} {
		model := NewModel(context.Background(), richDeps(), ContainersTab)
		model = updateModel(t, model, tea.KeyMsg{Type: start})
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlC})
		if !model.Cancelled() || model.renameMode || model.confirmKill {
			t.Fatalf("Ctrl-C left modal open: %#v", model)
		}
	}
}

func TestHeightFourKeepsSelectedPrimaryVisible(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ContainersTab)
	model.SetSize(80, 4)
	lines := strings.Split(model.View(), "\n")
	if len(lines) < 3 || !strings.Contains(shell.Plain(lines[2]), "one") {
		t.Fatalf("selected primary hidden at height four: %q", model.View())
	}
}

func TestLongSearchAndRenameKeepCursorVisible(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ContainersTab)
	model.SetQuery(strings.Repeat("x", 90))
	model.SetSize(40, 6)
	search := strings.Split(model.View(), "\n")[1]
	if !strings.Contains(search, "▌") || !strings.Contains(search, "matches") {
		t.Fatalf("long search line = %q", search)
	}
	model = NewModel(context.Background(), richDeps(), ContainersTab)
	model.SetSize(40, 6)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlR})
	for _, value := range strings.Repeat("x", 90) {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{value}})
	}
	lines := strings.Split(model.View(), "\n")
	if !strings.Contains(shell.Plain(lines[len(lines)-1]), "▌") {
		t.Fatalf("long rename footer lost cursor = %q", model.View())
	}
}

func TestPreviewNeverAppearsInContainersTab(t *testing.T) {
	model := NewModel(context.Background(), Deps{Containers: []capabilities.Container{{ID: "one", Name: "one", Preview: "preview loaded"}}}, ContainersTab)
	model.SetSize(80, 8)
	if strings.Contains(shell.Plain(model.View()), "preview loaded") {
		t.Fatalf("preview leaked into container row: %q", model.View())
	}
}

type blockingAgentSource struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (source *blockingAgentSource) ListAgents(context.Context) ([]capabilities.Agent, error) {
	source.calls.Add(1)
	select {
	case source.started <- struct{}{}:
	default:
	}
	<-source.release
	return nil, errors.New("source failed")
}

func TestAgentRefreshProtectsInFlightAndDisplaysError(t *testing.T) {
	source := &blockingAgentSource{started: make(chan struct{}, 1), release: make(chan struct{})}
	model := NewModel(context.Background(), Deps{AgentSource: source, AgentRefreshInterval: time.Nanosecond}, AgentsTab)
	model.Init()
	if model.refreshInFlight == nil || !*model.refreshInFlight {
		t.Fatal("initial refresh was not marked in flight")
	}
	first := model.refreshAgents()
	done := make(chan struct{})
	go func() { first(); close(done) }()
	<-source.started
	updated, cmd := model.Update(refreshTickMsg{})
	model = updated.(Model)
	if cmd == nil || source.calls.Load() != 1 {
		t.Fatalf("in-flight tick launched another source call: calls=%d", source.calls.Load())
	}
	if _, ok := cmd().(refreshTickMsg); !ok {
		t.Fatalf("in-flight tick command = %T, want only the next tick", cmd())
	}
	close(source.release)
	<-done
	model = updateModel(t, model, agentsMsg{err: errors.New("source failed")})
	model.SetSize(100, 8)
	if !strings.Contains(shell.Plain(model.footer()), "agent refresh: source failed") {
		t.Fatal("refresh error was not rendered")
	}
}

func TestAgentRankingAndAttentionOrder(t *testing.T) {
	agents := []capabilities.Agent{
		{PaneID: "unseen", Harness: "slow", Container: "one", Target: "one:1", State: "stopped", Seen: false},
		{PaneID: "a-working", Harness: "slow", Container: "one", Target: "one:2", State: "working"},
		{PaneID: "seen", Harness: "slow", Container: "one", Target: "one:3", State: "stopped", Seen: true},
		{PaneID: "a-target", Harness: "slow", Container: "one", Target: "priority-target", State: "working"},
		{PaneID: "z-harness", Harness: "priority", Container: "one", Target: "one:4", State: "working"},
	}
	model := NewModel(context.Background(), Deps{Agents: agents}, AgentsTab)
	want := []string{"agent:" + picker.LocalMachine() + ":unseen", "agent:" + picker.LocalMachine() + ":a-target", "agent:" + picker.LocalMachine() + ":a-working"}
	got := make([]string, 0, len(want))
	for _, row := range model.Rows() {
		if strings.HasPrefix(row, "agent:") {
			got = append(got, row)
		}
	}
	if !slices.Equal(got[:3], want) {
		t.Fatalf("empty-query attention order = %v, want prefix %v", got, want)
	}
	model.SetQuery("priority")
	got = got[:0]
	for _, row := range model.Rows() {
		if strings.HasPrefix(row, "agent:") {
			got = append(got, row)
		}
	}
	want = []string{"agent:" + picker.LocalMachine() + ":z-harness", "agent:" + picker.LocalMachine() + ":a-target"}
	if !slices.Equal(got, want) {
		t.Fatalf("query-ranked agents = %v, want %v", got, want)
	}
}

func TestContainerRankingKeepsMarks(t *testing.T) {
	containers := []capabilities.Container{
		{ID: "devbox", Name: "shell", Machine: "devbox"},
		{ID: "node", Name: "device", Machine: "node"},
		{ID: "alpha", Name: "mydev", Machine: "alpha"},
	}
	model := NewModel(context.Background(), Deps{Containers: containers}, ContainersTab)
	model.SetQuery("dev")
	want := []string{"machine:devbox", "container:devbox:devbox", "", "machine:node", "container:node:node", "", "machine:alpha", "container:alpha:alpha", "", "new:local"}
	if got := model.Rows(); !slices.Equal(got, want) {
		t.Fatalf("query-ranked containers = %v, want %v", got, want)
	}
	model = updateModel(t, model, keyMsg(tea.KeyCtrlT))
	if got := model.Marked(); !slices.Equal(got, []string{"devbox"}) {
		t.Fatalf("marked ranked container = %v", got)
	}
	model.SetQuery("dev")
	if got := model.Marked(); !slices.Equal(got, []string{"devbox"}) {
		t.Fatalf("mark lost during query rebuild = %v", got)
	}
}

func TestPlaceRankingAndVisibleAgentHighlights(t *testing.T) {
	model := NewModel(context.Background(), Deps{Containers: []capabilities.Container{{ID: "a", Name: "foobar"}, {ID: "b", Name: "bar"}}}, ContainersTab)
	model.SetQuery("bar")
	if !strings.HasSuffix(model.Cursor(), ":b") {
		t.Fatalf("best container was not first: %q", model.Cursor())
	}
	model = NewModel(context.Background(), Deps{Agents: []capabilities.Agent{{PaneID: "a", Container: "one", Target: "one:1", Harness: "pi", State: "working", Branch: "branch"}}}, AgentsTab)
	model.SetQuery("branch")
	model.SetSize(100, 8)
	// Inspect the row unselected so the accent match colour survives.
	model.cursor = ""
	lipgloss.SetColorProfile(termenv.ANSI)
	if !strings.Contains(model.View(), "branch") || !strings.Contains(model.View(), accent().Bold(true).Render("b")) {
		t.Fatalf("branch match not highlighted: %q", model.View())
	}
	projects := NewModel(context.Background(), Deps{Projects: []picker.Place{
		{Kind: picker.ProjectKind, Path: "/foobar", Group: "/work", DisplayRemainder: "foobar"},
		{Kind: picker.ProjectKind, Path: "/bar", Group: "/work", DisplayRemainder: "bar"},
	}}, ProjectsTab)
	projects.SetQuery("bar")
	if projects.Cursor() != "project:/bar" {
		t.Fatalf("best project was not first: %q", projects.Cursor())
	}
}

func TestModalTargetsAreFrozen(t *testing.T) {
	deps := richDeps()
	var killed []picker.ContainerRef
	deps.KillContainers = func(_ context.Context, refs []picker.ContainerRef) error {
		killed = append([]picker.ContainerRef(nil), refs...)
		return nil
	}
	model := NewModel(context.Background(), deps, ContainersTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlX})
	model.cursor = containerIdentity(picker.ContainerPlaces([]capabilities.Container{{ID: "remote-two", Name: "two", Machine: "remote"}})[0])
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("kill command missing")
	}
	model = updateModel(t, model, cmd())
	if len(killed) != 1 || killed[0].ID != "local-one" {
		t.Fatalf("frozen kill target = %v", killed)
	}

	deps = richDeps()
	var renamed picker.ContainerRef
	deps.RenameContainer = func(_ context.Context, ref picker.ContainerRef, _ string) error {
		renamed = ref
		return nil
	}
	rename := NewModel(context.Background(), deps, ContainersTab)
	rename = updateModel(t, rename, tea.KeyMsg{Type: tea.KeyCtrlR})
	rename.cursor = containerIdentity(picker.ContainerPlaces([]capabilities.Container{{ID: "remote-two", Name: "two", Machine: "remote"}})[0])
	for _, value := range []rune{'n', 'e', 'w'} {
		rename = updateModel(t, rename, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{value}})
	}
	updated, cmd = rename.Update(tea.KeyMsg{Type: tea.KeyEnter})
	rename = updated.(Model)
	if cmd == nil {
		t.Fatal("rename command missing")
	}
	rename = updateModel(t, rename, cmd())
	if renamed.ID != "local-one" {
		t.Fatalf("frozen rename target = %v", renamed)
	}
}

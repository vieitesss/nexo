package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

func TestRemoteLoadCompletionAndMachineScopedAgents(t *testing.T) {
	model := NewModel(context.Background(), Deps{
		Containers:     []capabilities.Container{{ID: "1", Name: "local"}, {ID: "1", Name: "remote", Machine: "dev"}},
		Agents:         []capabilities.Agent{{PaneID: "local-pane", ContainerID: "1", Container: "local", Harness: "pi", State: "working"}},
		RemoteMachines: []string{"dev"},
	}, ContainersTab)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Agents: []capabilities.Agent{{PaneID: "%1", ContainerID: "1", Container: "remote", Harness: "claude", State: "working"}}})
	if got := len(model.agentsFor(picker.Place{Kind: picker.ContainerKind, Machine: "dev", ContainerID: "1", ContainerName: "remote"})); got != 1 {
		t.Fatalf("remote association count = %d", got)
	}
	if got := len(model.agentsFor(picker.Place{Kind: picker.ContainerKind, Machine: picker.LocalMachine(), ContainerID: "1", ContainerName: "local"})); got != 1 {
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

func TestRemoteAgentIdentityIncludesMachine(t *testing.T) {
	model := NewModel(context.Background(), Deps{Agents: []capabilities.Agent{{PaneID: "%1", Target: "local:1", Harness: "pi", State: "working"}}}, AgentsTab)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Agents: []capabilities.Agent{{PaneID: "%1", Target: "raw-target", Harness: "claude", State: "working"}}})
	rows := model.Rows()
	if len(rows) != 2 || rows[0] == rows[1] {
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
	if !strings.Contains(strings.Split(model.View(), "\n")[3], "▌") {
		t.Fatalf("long rename line = %q", model.View())
	}
}

func TestPreviewNormalizesEmptyMachine(t *testing.T) {
	model := NewModel(context.Background(), Deps{Containers: []capabilities.Container{{ID: "one", Name: "one"}}}, ContainersTab)
	model.SetSize(80, 8)
	model = updateModel(t, model, picker.PreviewBatch{Previews: map[picker.ContainerRef]string{{Machine: picker.LocalMachine(), ID: "one"}: "preview loaded"}})
	if !strings.Contains(shell.Plain(model.View()), "preview loaded") {
		t.Fatalf("preview was not applied: %q", model.View())
	}
}

type blockingAgentSource struct{}

func (blockingAgentSource) ListAgents(context.Context) ([]capabilities.Agent, error) {
	return nil, errors.New("source failed")
}

func TestAgentRefreshProtectsInFlightAndDisplaysError(t *testing.T) {
	model := NewModel(context.Background(), Deps{AgentSource: blockingAgentSource{}, AgentRefreshInterval: time.Hour}, AgentsTab)
	model.Init()
	if model.refreshInFlight == nil || !*model.refreshInFlight {
		t.Fatal("initial refresh was not marked in flight")
	}
	updated, cmd := model.Update(refreshTickMsg{})
	model = updated.(Model)
	if cmd == nil || model.refreshInFlight == nil || !*model.refreshInFlight {
		t.Fatal("in-flight refresh was launched again")
	}
	model = updateModel(t, model, agentsMsg{err: errors.New("source failed")})
	model.SetSize(100, 8)
	if !strings.Contains(shell.Plain(model.footer()), "agent refresh: source failed") {
		t.Fatal("refresh error was not rendered")
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
	if !strings.Contains(model.View(), "branch") {
		t.Fatalf("branch match not visible: %q", model.View())
	}
}

func TestModalTargetsAreFrozen(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ContainersTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlX})
	model = updateModel(t, model, picker.RemoteBatch{Machine: "remote", Places: picker.ContainerPlaces([]capabilities.Container{{ID: "new", Name: "new", Machine: "remote"}})})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	refs, ok := model.KillTargetsRequest()
	if !ok || len(refs) != 1 || refs[0].ID != "local-one" {
		t.Fatalf("frozen kill target = %v, %v", refs, ok)
	}
}

package herdr

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestAgentSourceMapsNativeStatuses(t *testing.T) {
	clock := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	runner := &fakeRunner{outputs: map[string][]byte{
		"agent list": []byte(`{"id":"cli:agent:list","result":{"type":"agent_list","agents":[
			{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"working","focused":false,"state_change_seq":1},
			{"pane_id":"w:p2","workspace_id":"w","tab_id":"t","agent":"claude","agent_status":"idle","focused":false,"state_change_seq":2},
			{"pane_id":"w:p3","workspace_id":"w","tab_id":"t","agent":"codex","agent_status":"blocked","focused":false,"state_change_seq":3},
			{"pane_id":"w:p4","workspace_id":"w","tab_id":"t","agent":"aider","agent_status":"done","focused":false,"state_change_seq":4},
			{"pane_id":"w:p5","workspace_id":"w","tab_id":"t","agent":"unknown","agent_status":"unknown","focused":false,"state_change_seq":5}
		]}}`),
	}}
	source := NewAgentSource(runner)
	source.SetStateDir(t.TempDir())
	source.SetClock(func() time.Time { return clock })

	got, err := source.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("ListAgents() returned %d agents, want 4: %#v", len(got), got)
	}
	want := []capabilities.Agent{
		{PaneID: "w:p1", Target: "w:p1", ContainerID: "w", Harness: "pi", State: "working"},
		{PaneID: "w:p2", Target: "w:p2", ContainerID: "w", Harness: "claude", State: "stopped", Seen: true, StoppedSince: timePtr(clock)},
		{PaneID: "w:p3", Target: "w:p3", ContainerID: "w", Harness: "codex", State: "stopped", StoppedSince: timePtr(clock)},
		{PaneID: "w:p4", Target: "w:p4", ContainerID: "w", Harness: "aider", State: "stopped", StoppedSince: timePtr(clock)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agents = %#v, want %#v", got, want)
	}
	wantCalls := []call{
		{name: "herdr", args: []string{"agent", "list"}},
		{name: "herdr", args: []string{"workspace", "list"}},
		{name: "herdr", args: []string{"tab", "list"}},
	}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("calls = %#v, want read-only location lists: %#v", runner.calls, wantCalls)
	}
}

func TestAgentSourceFillsLocationLabels(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"agent list":     []byte(`{"result":{"type":"agent_list","agents":[{"pane_id":"w:p1","workspace_id":"w1","tab_id":"w1:t1","agent":"pi","agent_status":"working","state_change_seq":1}]}}`),
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"project"}]}}`),
		"tab list":       []byte(`{"result":{"type":"tab_list","tabs":[{"workspace_id":"w1","tab_id":"w1:t1","label":"editor"}]}}`),
	}}
	source := NewAgentSource(runner)
	source.SetStateDir(t.TempDir())
	got, err := source.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if got[0].Container != "project" || got[0].Window != "editor" {
		t.Fatalf("location = %#v, want project/editor", got[0])
	}
}

func TestAgentSourceBlockedTransitionBecomesUnseen(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"agent list": []byte(`{"result":{"type":"agent_list","agents":[{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"idle","focused":false,"state_change_seq":1}]}}`),
	}}
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	source := NewAgentSource(runner)
	source.SetStateDir(t.TempDir())
	source.SetClock(func() time.Time { return now })
	first, err := source.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first[0].Seen {
		t.Fatalf("first observation = %#v, want idle agent seen", first[0])
	}

	now = now.Add(time.Minute)
	runner.outputs["agent list"] = []byte(`{"result":{"type":"agent_list","agents":[{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"blocked","focused":false,"state_change_seq":2}]}}`)
	second, err := source.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Seen || !second[0].StoppedSince.Equal(now) {
		t.Fatalf("second observation = %#v, want newly stopped unseen agent", second[0])
	}
}

func TestAgentSourcePersistsStoppedSinceAcrossInvocations(t *testing.T) {
	stateDir := t.TempDir()
	runner := &fakeRunner{outputs: map[string][]byte{
		"agent list": []byte(`{"result":{"type":"agent_list","agents":[{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"done","focused":false,"state_change_seq":7}]}}`),
	}}
	firstTime := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	first := NewAgentSource(runner)
	first.SetStateDir(stateDir)
	first.SetClock(func() time.Time { return firstTime })
	got, err := first.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	secondTime := firstTime.Add(time.Hour)
	second := NewAgentSource(runner)
	second.SetStateDir(stateDir)
	second.SetClock(func() time.Time { return secondTime })
	again, err := second.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !again[0].StoppedSince.Equal(*got[0].StoppedSince) {
		t.Fatalf("stopped_since = %v, want persisted %v", again[0].StoppedSince, got[0].StoppedSince)
	}

	runner.outputs["agent list"] = []byte(`{"result":{"type":"agent_list","agents":[{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"working","focused":false,"state_change_seq":8}]}}`)
	if _, err := second.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.outputs["agent list"] = []byte(`{"result":{"type":"agent_list","agents":[{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"done","focused":false,"state_change_seq":9}]}}`)
	third, err := second.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !third[0].StoppedSince.After(*got[0].StoppedSince) {
		t.Fatalf("new stopped_since = %v, want reset after working transition", third[0].StoppedSince)
	}
}

func TestAgentSourcePrunesPanesAbsentFromSnapshot(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"agent list": []byte(`{"result":{"type":"agent_list","agents":[
			{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"done","state_change_seq":1},
			{"pane_id":"w:p2","workspace_id":"w","tab_id":"t","agent":"codex","agent_status":"done","state_change_seq":1}
		]}}`),
	}}
	source := NewAgentSource(runner)
	source.SetStateDir(t.TempDir())
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.outputs["agent list"] = []byte(`{"result":{"type":"agent_list","agents":[{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"done","state_change_seq":1}]}}`)
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	document, err := source.ledger().Read()
	if err != nil {
		t.Fatal(err)
	}
	states := document.Agents
	if _, ok := states["w:p2"]; ok {
		t.Fatalf("persisted states = %#v, want disappeared pane pruned", states)
	}
	if _, ok := states["w:p1"]; !ok {
		t.Fatalf("persisted states = %#v, want current pane retained", states)
	}
}

func TestAgentSourceKeysDefaultServerByConfigPath(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", "")
	stateDir := t.TempDir()
	runner := &fakeRunner{outputs: map[string][]byte{
		"agent list": []byte(`{"result":{"type":"agent_list","agents":[{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"done","state_change_seq":1}]}}`),
	}}
	first := NewAgentSource(runner)
	first.SetStateDir(stateDir)
	t.Setenv("HERDR_CONFIG_PATH", "/tmp/herdr-one/config.toml")
	if _, err := first.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := NewAgentSource(runner)
	second.SetStateDir(stateDir)
	t.Setenv("HERDR_CONFIG_PATH", "/tmp/herdr-two/config.toml")
	if _, err := second.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if first.ledger().Path("json") == second.ledger().Path("json") {
		t.Fatalf("state paths = %q, want distinct paths for distinct config paths", first.ledger().Path("json"))
	}
}

func TestAgentSourceFillsBranchFromForegroundCWD(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"agent list": []byte(`{"result":{"type":"agent_list","agents":[{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"working","focused":false,"cwd":"/work/repo","foreground_cwd":"/work/repo/worktree","state_change_seq":1}]}}`),
		"-C /work/repo/worktree branch --show-current": []byte("feature/herdr\n"),
	}}
	source := NewAgentSource(runner)
	source.SetStateDir(t.TempDir())
	got, err := source.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Branch != "feature/herdr" {
		t.Fatalf("branch = %q, want feature/herdr", got[0].Branch)
	}
	wantCalls := []call{
		{name: "herdr", args: []string{"agent", "list"}},
		{name: "herdr", args: []string{"workspace", "list"}},
		{name: "herdr", args: []string{"tab", "list"}},
		{name: "git", args: []string{"-C", "/work/repo/worktree", "branch", "--show-current"}},
	}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, wantCalls)
	}
}

func timePtr(value time.Time) *time.Time { return &value }

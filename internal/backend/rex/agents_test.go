package rex

import (
	"context"
	"strings"
	"testing"
)

// agentFixture is one fake block's native evidence. A status is the
// program-status report the block returns; a process and screen are only
// needed by blocks that report no recognized status.
type agentFixture struct {
	records string
	process string
	screen  string
}

// agentBackend builds a backend with one session holding one terminal block.
func agentBackend(t *testing.T, fixture agentFixture) (*Backend, *fakeRunner) {
	t.Helper()
	process := fixture.process
	if process == "" {
		process = `{}`
	}
	outputs := map[string]string{
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.list", ""): sessionsJSON(sessionJSON("session:1", "api")),
		api("session.list_blocks", `{"session_id":"session:1"}`):                          blocksJSON(terminalBlockJSON("block:1", "host-1")),
		api("block.method", blockMethodPayload("session:1", "block:1", "program_status")): fixture.records,
		api("block.method", blockMethodPayload("session:1", "block:1", "process")):        process,
	}
	if fixture.screen != "" {
		outputs[api("block.method", blockCallPayload("session:1", "block:1", "format", `{"format":"text","trim":true}`))] = fixture.screen
	}
	runner := &fakeRunner{outputs: outputs}
	return newTestBackend(t, runner, nil), runner
}

// statusJSON is one program-status report for a single app.
func statusJSON(state, app, updatedAt string) string {
	return `{"records":[{"state":"` + state + `","app":"` + app + `","effective_app":"` + app + `","updated_at":"` + updatedAt + `"}]}`
}

func TestListAgentsMapsEveryNativeState(t *testing.T) {
	cases := []struct {
		state       string
		want        string
		wantSeen    bool
		wantStopped bool
	}{
		{state: "working", want: "working", wantSeen: false, wantStopped: false},
		{state: "idle", want: "stopped", wantSeen: true, wantStopped: true},
		{state: "blocked", want: "stopped", wantSeen: false, wantStopped: true},
		{state: "done", want: "stopped", wantSeen: false, wantStopped: true},
		{state: "error", want: "stopped", wantSeen: false, wantStopped: true},
		// A state a newer Rex reports is documented to mean idle.
		{state: "waiting", want: "stopped", wantSeen: true, wantStopped: true},
	}
	for _, test := range cases {
		t.Run(test.state, func(t *testing.T) {
			backend, _ := agentBackend(t, agentFixture{records: statusJSON(test.state, "claude-code", "2026-10-22T09:48:34+02:00")})

			agents, err := backend.ListAgents(context.Background())
			if err != nil {
				t.Fatalf("ListAgents() error = %v", err)
			}
			if len(agents) != 1 {
				t.Fatalf("agents = %#v, want one agent for the block", agents)
			}
			agent := agents[0]
			if agent.State != test.want {
				t.Fatalf("State = %q, want %q", agent.State, test.want)
			}
			if agent.Seen != test.wantSeen {
				t.Fatalf("Seen = %v, want %v", agent.Seen, test.wantSeen)
			}
			if (agent.StoppedSince != nil) != test.wantStopped {
				t.Fatalf("StoppedSince = %v, want stopped = %v", agent.StoppedSince, test.wantStopped)
			}
			if agent.Harness != "claude" {
				t.Fatalf("Harness = %q, want the configured harness claude", agent.Harness)
			}
			if agent.PaneID != "block:1" || agent.Target != "block:1" || agent.ContainerID != "session:1" || agent.Container != "api" {
				t.Fatalf("agent = %#v, want the block and its session identities", agent)
			}
		})
	}
}

func TestListAgentsIgnoresStatusFromNonAgentPrograms(t *testing.T) {
	backend, _ := agentBackend(t, agentFixture{records: statusJSON("working", "cargo", "2026-10-22T09:48:34+02:00")})

	agents, err := backend.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("agents = %#v, want a cargo build to stay out of the agent list", agents)
	}
}

// blockCallPayload is the fake-runner payload of a block method call that
// carries its own arguments.
func blockCallPayload(sessionID, blockID, method, args string) string {
	return `{"session_id":"` + sessionID + `","block_id":"` + blockID + `","method":"` + method + `","args":` + args + `}`
}

func TestListAgentsFallsBackToProcessAndScreenObservation(t *testing.T) {
	backend, runner := agentBackend(t, agentFixture{
		records: `{"records":[]}`,
		process: `{"foreground":{"pid":42,"name":"node","script":"/opt/homebrew/bin/claude","cwd":"/tmp/project"}}`,
		screen:  `{"content":"⠋ Working… (esc to interrupt)"}`,
	})
	runner.outputs["-C /tmp/project branch --show-current"] = "feat/rex-backend\n"

	agents, err := backend.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents = %#v, want one agent for the block", agents)
	}
	agent := agents[0]
	if agent.Harness != "claude" || agent.State != "working" {
		t.Fatalf("agent = %#v, want a working claude detected from the process and screen", agent)
	}
	if agent.Branch != "feat/rex-backend" {
		t.Fatalf("Branch = %q, want the foreground directory's branch", agent.Branch)
	}
}

func TestListAgentsKeepsStoppedAgeAcrossRepeatedReports(t *testing.T) {
	statusPayload := api("block.method", blockMethodPayload("session:1", "block:1", "program_status"))
	backend, runner := agentBackend(t, agentFixture{records: statusJSON("blocked", "claude-code", "2026-10-22T09:48:34+02:00")})

	first, err := backend.ListAgents(context.Background())
	if err != nil || len(first) != 1 || first[0].StoppedSince == nil {
		t.Fatalf("ListAgents() = %#v, %v; want one stopped agent", first, err)
	}
	// Rex replaces updated_at on every report, so a re-report of the same state
	// is not a new stop.
	runner.outputs[statusPayload] = statusJSON("blocked", "claude-code", "2026-10-22T09:52:00+02:00")
	second, err := backend.ListAgents(context.Background())
	if err != nil || len(second) != 1 {
		t.Fatalf("ListAgents() = %#v, %v; want one stopped agent", second, err)
	}
	if !first[0].StoppedSince.Equal(*second[0].StoppedSince) {
		t.Fatalf("StoppedSince moved from %v to %v on a repeated report", first[0].StoppedSince, second[0].StoppedSince)
	}

	runner.outputs[statusPayload] = statusJSON("done", "claude-code", "2026-10-22T09:53:00+02:00")
	third, err := backend.ListAgents(context.Background())
	if err != nil || len(third) != 1 {
		t.Fatalf("ListAgents() = %#v, %v; want one stopped agent", third, err)
	}
	if !third[0].StoppedSince.After(*second[0].StoppedSince) {
		t.Fatalf("StoppedSince = %v, want a new stop for a new state", third[0].StoppedSince)
	}
}

func TestListAgentsReportsOneRowForABlockWithSeveralRecords(t *testing.T) {
	cases := []struct {
		name    string
		records string
		want    string
		seen    bool
	}{
		{
			name: "blocked child outranks idle root",
			records: `{"records":[` +
				`{"state":"idle","app":"claude-code","effective_app":"claude-code","updated_at":"2026-10-22T09:48:34+02:00"},` +
				`{"id":"task","state":"blocked","kind":"question","effective_app":"claude-code","updated_at":"2026-10-22T09:48:36+02:00"}]}`,
			want: "stopped",
			seen: false,
		},
		{
			name: "working child outranks idle root",
			records: `{"records":[` +
				`{"state":"idle","app":"claude-code","effective_app":"claude-code","updated_at":"2026-10-22T09:48:34+02:00"},` +
				`{"id":"task","state":"working","effective_app":"claude-code","updated_at":"2026-10-22T09:48:36+02:00"}]}`,
			want: "working",
			seen: false,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			backend, _ := agentBackend(t, agentFixture{records: test.records})

			agents, err := backend.ListAgents(context.Background())
			if err != nil {
				t.Fatalf("ListAgents() error = %v", err)
			}
			if len(agents) != 1 {
				t.Fatalf("agents = %#v, want one row per block", agents)
			}
			if agents[0].State != test.want || agents[0].Seen != test.seen {
				t.Fatalf("agent = %#v, want state %q and seen %v", agents[0], test.want, test.seen)
			}
		})
	}
}

func TestReadScreenReturnsTheBlockText(t *testing.T) {
	backend, _ := agentBackend(t, agentFixture{screen: `{"content":"$ echo hello\nhello"}`})

	got, err := backend.ReadScreen(context.Background(), "block:1")
	if err != nil {
		t.Fatalf("ReadScreen() error = %v", err)
	}
	if got != "$ echo hello\nhello" {
		t.Fatalf("ReadScreen() = %q, want the block's text", got)
	}
}

func TestFocusPaneFocusesTheBlockThenSelectsTheSession(t *testing.T) {
	backend, runner := agentBackend(t, agentFixture{records: statusJSON("blocked", "claude-code", "2026-10-22T09:48:34+02:00")})
	runner.outputs[api("session.focus_block", `{"session_id":"session:1","block_id":"block:1"}`)] = `{"revision":2}`
	runner.outputs[api("client.list", "")] = oneApp
	if _, err := backend.ListAgents(context.Background()); err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	runner.outputs[api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:1"},"origin":"cli","timeout_ms":3000}`)] = `{"performed_by":"client:1"}`

	if err := backend.FocusPane(context.Background(), "block:1"); err != nil {
		t.Fatalf("FocusPane() error = %v", err)
	}
	keys := runner.keys()
	focusIndex, performIndex := -1, -1
	for index, key := range keys {
		switch {
		case strings.Contains(key, "session.focus_block"):
			focusIndex = index
		case strings.Contains(key, "client.perform"):
			performIndex = index
		}
	}
	if focusIndex < 0 || performIndex < 0 {
		t.Fatalf("commands = %#v, want a block focus and an app selection", keys)
	}
	if focusIndex > performIndex {
		t.Fatalf("commands = %#v, want the block focus before the app selection", keys)
	}

	agents, err := backend.ListAgents(context.Background())
	if err != nil || len(agents) != 1 {
		t.Fatalf("ListAgents() = %#v, %v; want the focused agent", agents, err)
	}
	if !agents[0].Seen {
		t.Fatalf("agent = %#v, want the focused agent marked seen", agents[0])
	}
}

func TestFocusPaneWithoutAConnectedAppMarksNothingSeen(t *testing.T) {
	var messages strings.Builder
	backend, runner := agentBackend(t, agentFixture{records: statusJSON("blocked", "claude-code", "2026-10-22T09:48:34+02:00")})
	backend.messages = &messages
	runner.outputs[api("session.focus_block", `{"session_id":"session:1","block_id":"block:1"}`)] = `{"revision":2}`
	runner.outputs[api("client.list", "")] = `{"clients":[]}`
	if _, err := backend.ListAgents(context.Background()); err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}

	if err := backend.FocusPane(context.Background(), "block:1"); err != nil {
		t.Fatalf("FocusPane() error = %v", err)
	}
	for _, key := range runner.keys() {
		if strings.Contains(key, "client.perform") {
			t.Fatalf("commands = %#v, want no app action without a connected app", runner.keys())
		}
	}
	if !strings.Contains(messages.String(), "was not selected") {
		t.Fatalf("messages = %q, want an informational notice", messages.String())
	}

	agents, err := backend.ListAgents(context.Background())
	if err != nil || len(agents) != 1 {
		t.Fatalf("ListAgents() = %#v, %v; want the agent", agents, err)
	}
	if agents[0].Seen {
		t.Fatalf("agent = %#v, want an unselected focus to leave the agent unseen", agents[0])
	}
}

package rex

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
)

// agentFixture is one fake block's native evidence. A status is the
// program-status report the block returns; a process and screen are only
// needed by blocks that report no recognized status.
type agentFixture struct {
	records string
	process string
	screen  string
	// viewport is the block's VT capture: the scrollback Rex still holds
	// followed by every row the terminal is showing. The working check reads it.
	viewport string
	// size is the block's terminal size. A fixture with a screen or a viewport
	// reports a 24-row terminal unless it says otherwise.
	size string
}

// vtCapture is one Rex VT capture: transcript rows joined by CRLF, blank rows
// completing the terminal, and the cursor escape Rex writes after the last row.
func vtCapture(rows int, transcript ...string) string {
	frame := append([]string(nil), transcript...)
	for len(frame) < rows {
		frame = append(frame, "")
	}
	frame[len(frame)-1] += "\x1b[1;1H"
	content, err := json.Marshal(screenResult{Content: strings.Join(frame, "\r\n")})
	if err != nil {
		panic(err)
	}
	return string(content)
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
	if fixture.viewport != "" {
		outputs[api("block.method", blockCallPayload("session:1", "block:1", "format", `{"format":"vt"}`))] = fixture.viewport
	}
	if fixture.screen != "" || fixture.viewport != "" {
		size := fixture.size
		if size == "" {
			size = `{"rows":24}`
		}
		outputs[api("block.method", blockMethodPayload("session:1", "block:1", "size"))] = size
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
		records:  `{"records":[]}`,
		process:  `{"foreground":{"pid":42,"name":"node","script":"/opt/homebrew/bin/claude","cwd":"/tmp/project"}}`,
		screen:   `{"content":"⠋ Working… (esc to interrupt)"}`,
		viewport: vtCapture(24, "⠋ Working… (esc to interrupt)"),
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

// malformedStatus is a program-status reply that does not decode. It is a
// failed read, not a successful report that carries no records.
const malformedStatus = `{"records":[`

func TestListAgentsIgnoresAnUnreadableStatusReport(t *testing.T) {
	statusPayload := api("block.method", blockMethodPayload("session:1", "block:1", "program_status"))
	backend, runner := agentBackend(t, agentFixture{
		records: statusJSON("blocked", "claude-code", "2026-10-22T09:48:34+02:00"),
		process: `{"foreground":{"pid":42,"name":"node","script":"/opt/homebrew/bin/claude","cwd":"/tmp/project"}}`,
		screen:  `{"content":"⠋ Working… (esc to interrupt)"}`,
	})
	first, err := backend.ListAgents(context.Background())
	if err != nil || len(first) != 1 || first[0].StoppedSince == nil {
		t.Fatalf("ListAgents() = %#v, %v; want one stopped agent", first, err)
	}
	ledger, err := backend.agentLedger(context.Background())
	if err != nil {
		t.Fatalf("agentLedger() error = %v", err)
	}
	if err := ledger.MarkSeen("block:1"); err != nil {
		t.Fatalf("MarkSeen() error = %v", err)
	}

	// A status read that fails says nothing about the block: the working
	// spinner on screen must not replace the native state it cannot read.
	runner.outputs[statusPayload] = malformedStatus
	unreadable, err := backend.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(unreadable) != 0 {
		t.Fatalf("agents = %#v, want no observation from an unreadable status", unreadable)
	}

	// The next successful report finds the ledger untouched: the block is
	// still the same stop, acknowledged, with its original stopped age.
	runner.outputs[statusPayload] = statusJSON("blocked", "claude-code", "2026-10-22T09:52:00+02:00")
	recovered, err := backend.ListAgents(context.Background())
	if err != nil || len(recovered) != 1 {
		t.Fatalf("ListAgents() = %#v, %v; want the recovered agent", recovered, err)
	}
	if recovered[0].State != "stopped" || !recovered[0].Seen {
		t.Fatalf("agent = %#v, want the acknowledged stopped state preserved", recovered[0])
	}
	if recovered[0].StoppedSince == nil || !recovered[0].StoppedSince.Equal(*first[0].StoppedSince) {
		t.Fatalf("StoppedSince = %v, want the preserved age %v", recovered[0].StoppedSince, first[0].StoppedSince)
	}
}

// cleanScreen is a terminal sitting at a prompt: the process is an agent, but
// nothing on screen says it is working.
const cleanScreen = `{"content":"$ git status\nnothing to commit\n$"}`

// workingScreen is a live spinner, the fallback's proof that an agent works.
const workingScreen = `{"content":"⠋ Working… (esc to interrupt)"}`

// workingViewport and cleanViewport are the same screens as Rex's VT capture
// returns them: a complete terminal frame.
var (
	workingViewport = vtCapture(24, "⠋ Working… (esc to interrupt)")
	cleanViewport   = vtCapture(24, "$ git status", "nothing to commit", "$")
)

// agentProcess makes the fallback recognise a claude agent in the block.
const agentProcess = `{"foreground":{"pid":42,"name":"node","script":"/opt/homebrew/bin/claude","cwd":"/tmp/project"}}`

// listOnce lists agents and requires the expected number of rows.
func listOnce(t *testing.T, backend *Backend, want int) []capabilities.Agent {
	t.Helper()
	agents, err := backend.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != want {
		t.Fatalf("agents = %#v, want %d", agents, want)
	}
	return agents
}

// clockedBackend fixes the ledger clock so a test can move between observations.
func clockedBackend(t *testing.T, fixture agentFixture, now *time.Time) *Backend {
	t.Helper()
	backend, _ := agentBackend(t, fixture)
	backend.now = func() time.Time { return *now }
	return backend
}

func TestListAgentsResetsNativeStateWhenObservationFallsBack(t *testing.T) {
	statusPayload := api("block.method", blockMethodPayload("session:1", "block:1", "program_status"))
	blocked := statusJSON("blocked", "claude-code", "2026-10-22T09:48:34+02:00")
	now := time.Date(2026, time.October, 22, 9, 48, 0, 0, time.UTC)
	backend := clockedBackend(t, agentFixture{
		records:  blocked,
		process:  agentProcess,
		screen:   workingScreen,
		viewport: workingViewport,
	}, &now)
	runner := backend.Runner.(*fakeRunner)

	native := listOnce(t, backend, 1)
	if native[0].State != "stopped" || native[0].StoppedSince == nil || !native[0].StoppedSince.Equal(now) {
		t.Fatalf("agent = %#v, want the native blocked stop at %v", native[0], now)
	}

	// The program stops reporting and the screen still shows an old spinner.
	// The stale native record must not survive the handoff.
	now = now.Add(10 * time.Second)
	runner.outputs[statusPayload] = `{"records":[]}`
	fallback := listOnce(t, backend, 1)
	if fallback[0].State != "working" || fallback[0].StoppedSince != nil {
		t.Fatalf("agent = %#v, want the fallback to observe a working agent", fallback[0])
	}

	// The same native state resumes: because the fallback cleared the native
	// record, this is a fresh stop, not a repeated report of the old one.
	now = now.Add(10 * time.Second)
	runner.outputs[statusPayload] = blocked
	resumed := listOnce(t, backend, 1)
	if resumed[0].State != "stopped" || resumed[0].Seen {
		t.Fatalf("agent = %#v, want a fresh unseen stop", resumed[0])
	}
	if resumed[0].StoppedSince == nil || !resumed[0].StoppedSince.Equal(now) {
		t.Fatalf("StoppedSince = %v, want the fresh stop at %v", resumed[0].StoppedSince, now)
	}
}

func TestListAgentsRefreshesNativeAcknowledgementWhenObservationResumes(t *testing.T) {
	statusPayload := api("block.method", blockMethodPayload("session:1", "block:1", "program_status"))
	idle := statusJSON("idle", "claude-code", "2026-10-22T09:48:34+02:00")
	now := time.Date(2026, time.October, 22, 9, 48, 0, 0, time.UTC)
	backend := clockedBackend(t, agentFixture{
		records:  idle,
		process:  agentProcess,
		screen:   cleanScreen,
		viewport: cleanViewport,
	}, &now)
	runner := backend.Runner.(*fakeRunner)

	native := listOnce(t, backend, 1)
	if native[0].State != "stopped" || !native[0].Seen {
		t.Fatalf("agent = %#v, want the idle agent seen", native[0])
	}

	now = now.Add(10 * time.Second)
	runner.outputs[statusPayload] = `{"records":[]}`
	if fallback := listOnce(t, backend, 1); fallback[0].State != "working" {
		t.Fatalf("agent = %#v, want the fallback to observe a new working agent", fallback[0])
	}

	now = now.Add(10 * time.Second)
	runner.outputs[statusPayload] = idle
	resumed := listOnce(t, backend, 1)
	if resumed[0].State != "stopped" || !resumed[0].Seen {
		t.Fatalf("agent = %#v, want the resumed idle report seen again", resumed[0])
	}
}

// statusJSONOwned is one program-status report published by an owner process.
// Rex replaces updated_at on every report and names the reporting process by
// pid and name, because pids are reused.
func statusJSONOwned(state, app, updatedAt string, pid int, name string) string {
	return `{"records":[{"state":"` + state + `","app":"` + app + `","effective_app":"` + app +
		`","updated_at":"` + updatedAt + `","owner":{"pid":` + strconv.Itoa(pid) + `,"name":"` + name + `"}}]}`
}

func TestListAgentsKeepsAcknowledgementForTheSameOwner(t *testing.T) {
	statusPayload := api("block.method", blockMethodPayload("session:1", "block:1", "program_status"))
	now := time.Date(2026, time.October, 22, 9, 48, 0, 0, time.UTC)
	backend := clockedBackend(t, agentFixture{
		records: statusJSONOwned("blocked", "claude-code", "2026-10-22T09:48:34+02:00", 42, "zsh"),
	}, &now)
	runner := backend.Runner.(*fakeRunner)
	ledger, err := backend.agentLedger(context.Background())
	if err != nil {
		t.Fatalf("agentLedger() error = %v", err)
	}

	first := listOnce(t, backend, 1)
	if first[0].StoppedSince == nil {
		t.Fatalf("agent = %#v, want a stopped agent", first[0])
	}
	if err := ledger.MarkSeen(first[0].PaneID); err != nil {
		t.Fatalf("MarkSeen() error = %v", err)
	}

	// The same process re-reports the same state. Rex moves updated_at on
	// every report, so only the owner identity can tell it apart from new work.
	now = now.Add(time.Minute)
	runner.outputs[statusPayload] = statusJSONOwned("blocked", "claude-code", "2026-10-22T09:49:40+02:00", 42, "zsh")
	repeated := listOnce(t, backend, 1)
	if !repeated[0].Seen {
		t.Fatalf("agent = %#v, want the acknowledged agent to stay seen", repeated[0])
	}
	if repeated[0].StoppedSince == nil || !repeated[0].StoppedSince.Equal(*first[0].StoppedSince) {
		t.Fatalf("StoppedSince = %v, want the preserved age %v", repeated[0].StoppedSince, first[0].StoppedSince)
	}
}

func TestListAgentsResetsAcknowledgementForANewOwner(t *testing.T) {
	statusPayload := api("block.method", blockMethodPayload("session:1", "block:1", "program_status"))
	now := time.Date(2026, time.October, 22, 9, 48, 0, 0, time.UTC)
	backend := clockedBackend(t, agentFixture{
		records: statusJSONOwned("blocked", "claude-code", "2026-10-22T09:48:34+02:00", 42, "zsh"),
	}, &now)
	runner := backend.Runner.(*fakeRunner)
	ledger, err := backend.agentLedger(context.Background())
	if err != nil {
		t.Fatalf("agentLedger() error = %v", err)
	}
	first := listOnce(t, backend, 1)
	if first[0].StoppedSince == nil {
		t.Fatalf("agent = %#v, want a stopped agent", first[0])
	}
	if err := ledger.MarkSeen(first[0].PaneID); err != nil {
		t.Fatalf("MarkSeen() error = %v", err)
	}

	// A new process now owns the block. Its identical status text is new work,
	// not the report the user already acknowledged.
	now = now.Add(time.Minute)
	runner.outputs[statusPayload] = statusJSONOwned("blocked", "claude-code", "2026-10-22T09:49:40+02:00", 43, "zsh")
	replaced := listOnce(t, backend, 1)
	if replaced[0].Seen {
		t.Fatalf("agent = %#v, want a new owner to be unseen", replaced[0])
	}
	if replaced[0].StoppedSince == nil || !replaced[0].StoppedSince.Equal(now) {
		t.Fatalf("StoppedSince = %v, want a fresh stop at %v", replaced[0].StoppedSince, now)
	}

	// Pids are reused, so a different process name is a new owner too.
	if err := ledger.MarkSeen(replaced[0].PaneID); err != nil {
		t.Fatalf("MarkSeen() error = %v", err)
	}
	now = now.Add(time.Minute)
	runner.outputs[statusPayload] = statusJSONOwned("blocked", "claude-code", "2026-10-22T09:50:40+02:00", 43, "login")
	renamed := listOnce(t, backend, 1)
	if renamed[0].Seen {
		t.Fatalf("agent = %#v, want a reused pid with another name to be unseen", renamed[0])
	}
	if renamed[0].StoppedSince == nil || !renamed[0].StoppedSince.Equal(now) {
		t.Fatalf("StoppedSince = %v, want a fresh stop at %v", renamed[0].StoppedSince, now)
	}
}

func TestListAgentsFindsAWorkingMarkerUnderColours(t *testing.T) {
	// Rex's VT capture keeps the colours a harness paints, so the live line
	// reaches the detector wrapped in escape sequences.
	coloured := "\x1b[1m⠋ Working\x1b[0m… (esc to interrupt)"

	now := time.Date(2026, time.October, 22, 9, 48, 0, 0, time.UTC)
	backend := clockedBackend(t, agentFixture{
		records:  `{"records":[]}`,
		process:  agentProcess,
		screen:   cleanScreen,
		viewport: vtCapture(24, coloured),
	}, &now)

	agents := listOnce(t, backend, 1)
	if agents[0].State != "working" {
		t.Fatalf("agent = %#v, want the coloured marker on screen to prove work", agents[0])
	}
	// A marker on screen keeps the agent working past the idle threshold; only
	// the marker's disappearance can stop it.
	now = now.Add(10 * time.Second)
	later := listOnce(t, backend, 1)
	if later[0].State != "working" {
		t.Fatalf("agent = %#v, want the coloured marker to keep the agent working", later[0])
	}
}

func TestListAgentsIgnoresAWorkingMarkerClearedFromTheScreen(t *testing.T) {
	// A runner painted a spinner, then a program cleared the screen and left a
	// clean prompt. Rex's text capture drops the blank rows at the bottom of the
	// screen, so the block reports a three-line capture for a 24-row terminal and
	// those lines are scrollback, not what the user sees.
	spinner := "⠋ Working… (esc to interrupt)"
	preview := `{"content":` + strconv.Quote(strings.Join([]string{spinner, "filler", "$ clean prompt"}, "\n")) + `}`

	// The VT capture keeps the rows that scrolled off plus the whole screen the
	// clear left behind: the prompt and the blank rows below it.
	scrollback := []string{spinner}
	for i := 1; i <= 7; i++ {
		scrollback = append(scrollback, fmt.Sprintf("filler %d", i))
	}
	captured := append(scrollback, "$ clean prompt")
	viewport := vtCapture(len(scrollback)+24, captured...)

	now := time.Date(2026, time.October, 22, 9, 48, 0, 0, time.UTC)
	backend := clockedBackend(t, agentFixture{
		records:  `{"records":[]}`,
		process:  agentProcess,
		screen:   preview,
		viewport: viewport,
	}, &now)

	listOnce(t, backend, 1)
	now = now.Add(4 * time.Second)
	idle := listOnce(t, backend, 1)
	if idle[0].State != "stopped" {
		t.Fatalf("agent = %#v, want a stop once the screen shows no marker", idle[0])
	}

	// The preview keeps showing the capture Rex returns, scrollback included.
	content, err := backend.ReadScreen(context.Background(), "block:1")
	if err != nil {
		t.Fatalf("ReadScreen() error = %v", err)
	}
	if !strings.Contains(content, spinner) || !strings.Contains(content, "$ clean prompt") {
		t.Fatalf("ReadScreen() = %q, want the full text capture", content)
	}
}

func TestListAgentsIgnoresWorkingMarkersInTheScrollback(t *testing.T) {
	// A long transcript pushed the live spinner off the screen; the block now
	// sits at a clean prompt.
	lines := []string{"⠋ Working… (esc to interrupt)"}
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("assistant output %d", i))
	}
	lines = append(lines, "$ git status", "nothing to commit, working tree clean", "$")

	now := time.Date(2026, time.October, 22, 9, 48, 0, 0, time.UTC)
	backend := clockedBackend(t, agentFixture{
		records:  `{"records":[]}`,
		process:  agentProcess,
		screen:   `{"content":` + strconv.Quote(strings.Join(lines, "\n")) + `}`,
		viewport: vtCapture(24, lines...),
		size:     `{"rows":24}`,
	}, &now)

	listOnce(t, backend, 1)
	now = now.Add(4 * time.Second)
	idle := listOnce(t, backend, 1)
	if idle[0].State != "stopped" {
		t.Fatalf("agent = %#v, want a stop once only the scrollback holds the marker", idle[0])
	}

	// The preview still shows the whole terminal, scrollback included.
	content, err := backend.ReadScreen(context.Background(), "block:1")
	if err != nil {
		t.Fatalf("ReadScreen() error = %v", err)
	}
	if !strings.Contains(content, "⠋ Working…") || !strings.Contains(content, "working tree clean") {
		t.Fatalf("ReadScreen() = %q, want the full screen and scrollback", content)
	}
}

// TestListAgentsRecordsOneMixedSnapshotInOneTransaction pins the batch: a block
// with a native status and a block observed from its process and screen are one
// ledger transaction with one clock instant, and native rows stay first.
func TestListAgentsRecordsOneMixedSnapshotInOneTransaction(t *testing.T) {
	now := time.Date(2026, time.October, 22, 9, 48, 0, 0, time.UTC)
	runner := &fakeRunner{outputs: map[string]string{
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.list", ""): sessionsJSON(sessionJSON("session:1", "screen"), sessionJSON("session:2", "native")),
		api("session.list_blocks", `{"session_id":"session:1"}`):                                        blocksJSON(terminalBlockJSON("block:screen", "host-1")),
		api("session.list_blocks", `{"session_id":"session:2"}`):                                        blocksJSON(terminalBlockJSON("block:native", "host-1")),
		api("block.method", blockMethodPayload("session:1", "block:screen", "program_status")):          `{"records":[]}`,
		api("block.method", blockMethodPayload("session:1", "block:screen", "process")):                 `{"foreground":{"pid":42,"name":"node","script":"/opt/homebrew/bin/claude"}}`,
		api("block.method", blockCallPayload("session:1", "block:screen", "format", `{"format":"vt"}`)): vtCapture(24, "⠋ Working… (esc to interrupt)"),
		api("block.method", blockMethodPayload("session:1", "block:screen", "size")):                    `{"rows":24}`,
		api("block.method", blockMethodPayload("session:2", "block:native", "program_status")):          statusJSON("blocked", "claude-code", "2026-10-22T09:48:34+02:00"),
		api("block.method", blockMethodPayload("session:2", "block:native", "process")):                 `{}`,
	}}
	backend := newTestBackend(t, runner, nil)
	samples := 0
	backend.now = func() time.Time {
		samples++
		return now
	}

	agents, err := backend.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if samples != 1 {
		t.Fatalf("ledger clock samples = %d, want one transaction for the snapshot", samples)
	}
	if len(agents) != 2 {
		t.Fatalf("agents = %#v, want one row per block", agents)
	}
	if agents[0].PaneID != "block:native" || agents[1].PaneID != "block:screen" {
		t.Fatalf("agents = %#v, want the native block listed before the screen block", agents)
	}
	if agents[0].State != "stopped" || agents[1].State != "working" {
		t.Fatalf("states = %q and %q, want each observation to use its own mode", agents[0].State, agents[1].State)
	}
	if agents[0].StoppedSince == nil || !agents[0].StoppedSince.Equal(now) {
		t.Fatalf("stopped age = %v, want the batch instant %v", agents[0].StoppedSince, now)
	}
}

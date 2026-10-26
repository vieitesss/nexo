package rex

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/containerstate"
)

var (
	oneApp  = `{"clients":[` + connectedApp("client:1", "Studio", "2026-10-26T08:11:17+01:00") + `]}`
	twoApps = `{"clients":[` +
		connectedApp("client:old", "Studio", "2026-10-26T08:00:00+01:00") + `,` +
		connectedApp("client:new", "MacBook", "2026-10-26T09:30:00+01:00") + `,` +
		`{"client_id":"client:cli","info":{"kind":"cli","name":"rex"},"connected_at":"2026-10-26T09:40:00+01:00","connection_state":"connected"}` +
		`]}`
	twoSessions = `{"sessions":[{"session_id":"session:1","label":"api"},{"session_id":"session:2","label":"web"}]}`
)

func TestListContainersMapsSessionsAndFocusedBlocks(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): twoSessions,
		api("session.view", `{"session_id":"session:1"}`): `{"session_id":"session:1","label":"api","active_window_id":"window:1","windows":[` +
			`{"window_id":"window:1","focused_block_id":"block:9"},{"window_id":"window:2","focused_block_id":"block:3"}]}`,
		api("session.view", `{"session_id":"session:2"}`): `{"session_id":"session:2","label":"web","windows":null}`,
	}}
	backend := newTestBackend(t, runner, nil)

	containers, err := backend.ListContainers(context.Background())
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("containers = %#v, want two sessions", containers)
	}
	if containers[0].ID != "session:1" || containers[0].Name != "api" || containers[0].ActivePaneID != "block:9" {
		t.Fatalf("containers[0] = %#v, want the active window's focused block", containers[0])
	}
	if containers[1].ID != "session:2" || containers[1].ActivePaneID != "" {
		t.Fatalf("containers[1] = %#v, want a session without a focused block", containers[1])
	}
	if containers[0].Machine != containerstate.LocalMachine() || containers[0].Proxy {
		t.Fatalf("containers[0] = %#v, want a local non-proxy container", containers[0])
	}
}

func TestOpenContainerSelectsTheMostRecentlyConnectedApp(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): twoSessions,
		api("client.list", ""):  twoApps,
		api("client.perform", `{"client_id":"client:new","action":"session.select","args":{"session_id":"session:1"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:new"}`,
		whoamiKey: `{"host_id":"host-1","instance_id":"instance-1"}`,
	}}
	backend := newTestBackend(t, runner, nil)

	if err := backend.OpenContainer(context.Background(), "session:1"); err != nil {
		t.Fatalf("OpenContainer() error = %v", err)
	}
	want := []string{
		api("session.list", ""),
		api("client.list", ""),
		api("client.perform", `{"client_id":"client:new","action":"session.select","args":{"session_id":"session:1"},"origin":"cli","timeout_ms":3000}`),
		whoamiKey,
	}
	if strings.Join(runner.keys(), "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %#v, want %#v", runner.keys(), want)
	}
	entries, err := backend.RecentContainers(context.Background())
	if err != nil {
		t.Fatalf("RecentContainers() error = %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "session:1" || entries[0].Name != "api" {
		t.Fatalf("entries = %#v, want the selected session in history", entries)
	}
}

func TestOpenContainerHonoursRexClientByLabel(t *testing.T) {
	t.Setenv("REX_CLIENT", "studio")
	runner := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): twoSessions,
		api("client.list", ""):  twoApps,
		api("client.perform", `{"client_id":"client:old","action":"session.select","args":{"session_id":"session:1"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:old"}`,
		whoamiKey: `{"host_id":"host-1","instance_id":"instance-1"}`,
	}}
	backend := newTestBackend(t, runner, nil)

	if err := backend.OpenContainer(context.Background(), "session:1"); err != nil {
		t.Fatalf("OpenContainer() error = %v", err)
	}
	if !strings.Contains(strings.Join(runner.keys(), "\n"), "client:old") {
		t.Fatalf("commands = %#v, want the REX_CLIENT app targeted", runner.keys())
	}
}

func TestOpenContainerDiagnosesStaleRexClient(t *testing.T) {
	t.Setenv("REX_CLIENT", "client:gone")
	runner := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): twoSessions,
		api("client.list", ""):  twoApps,
	}}
	backend := newTestBackend(t, runner, nil)

	err := backend.OpenContainer(context.Background(), "session:1")
	if err == nil || !strings.Contains(err.Error(), "REX_CLIENT") {
		t.Fatalf("OpenContainer() error = %v, want a stale REX_CLIENT diagnostic", err)
	}
	if strings.Contains(strings.Join(runner.keys(), "\n"), "client.perform") {
		t.Fatalf("commands = %#v, want no focus attempt without a valid client", runner.keys())
	}
}

func TestOpenContainerWithoutAnAppSucceedsAndSaysSo(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): twoSessions,
		api("client.list", ""):  `{"clients":[{"client_id":"client:cli","info":{"kind":"cli"},"connection_state":"connected"}]}`,
	}}
	var messages strings.Builder
	backend := newTestBackend(t, runner, &messages)

	if err := backend.OpenContainer(context.Background(), "session:1"); err != nil {
		t.Fatalf("OpenContainer() error = %v, want a successful no-op", err)
	}
	if strings.Contains(strings.Join(runner.keys(), "\n"), "client.perform") {
		t.Fatalf("commands = %#v, want no selection without an app", runner.keys())
	}
	if !strings.Contains(messages.String(), "no Rex app is connected") || !strings.Contains(messages.String(), "session:1") {
		t.Fatalf("messages = %q, want an informational no-op", messages.String())
	}
}

func TestOpenContainerRejectsUnknownSessionID(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{api("session.list", ""): twoSessions}}
	backend := newTestBackend(t, runner, nil)

	err := backend.OpenContainer(context.Background(), "session:missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("OpenContainer() error = %v, want a missing session error", err)
	}
}

func TestRenameContainerUsesSessionSetLabel(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		api("session.set_label", `{"session_id":"session:1","label":"renamed"}`): `{"revision":2}`,
	}}
	backend := newTestBackend(t, runner, nil)

	if err := backend.RenameContainer(context.Background(), "session:1", "  renamed  "); err != nil {
		t.Fatalf("RenameContainer() error = %v", err)
	}
	if len(runner.calls) != 1 || runner.keys()[0] != api("session.set_label", `{"session_id":"session:1","label":"renamed"}`) {
		t.Fatalf("commands = %#v, want one set_label call with the trimmed label", runner.keys())
	}
	if err := backend.RenameContainer(context.Background(), "session:1", "  "); err == nil {
		t.Fatal("RenameContainer() error = nil, want an empty-name rejection")
	}
}

func TestKillContainerDestroysTheExactSession(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		api("session.destroy", `{"session_id":"session:1"}`): `{}`,
	}}
	backend := newTestBackend(t, runner, nil)

	if err := backend.KillContainer(context.Background(), "session:1"); err != nil {
		t.Fatalf("KillContainer() error = %v", err)
	}
	if len(runner.calls) != 1 || runner.keys()[0] != api("session.destroy", `{"session_id":"session:1"}`) {
		t.Fatalf("commands = %#v, want one destroy call", runner.keys())
	}
}

func TestKillContainersDedupesAndClosesTheCurrentSessionLast(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		whoamiKey: `{"session_id":"session:2"}`,
		api("session.destroy", `{"session_id":"session:1"}`): `{}`,
		api("session.destroy", `{"session_id":"session:2"}`): `{}`,
	}}
	backend := newTestBackend(t, runner, nil)

	if err := backend.KillContainers(context.Background(), []string{"session:1", "session:2", "session:1"}); err != nil {
		t.Fatalf("KillContainers() error = %v", err)
	}
	want := []string{
		whoamiKey,
		api("session.destroy", `{"session_id":"session:1"}`),
		api("session.destroy", `{"session_id":"session:2"}`),
	}
	if strings.Join(runner.keys(), "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %#v, want %#v", runner.keys(), want)
	}
}

func TestContainerHistoryIsScopedToTheServerRun(t *testing.T) {
	shared := t.TempDir()
	server := identity{HostID: "host-1", InstanceID: "instance-1"}

	first := newTestBackend(t, &fakeRunner{outputs: map[string]string{
		api("session.list", ""): twoSessions,
		api("client.list", ""):  oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:1"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}}, nil)
	first.stateDir = shared
	useIdentity(first, server)
	if err := first.OpenContainer(context.Background(), "session:1"); err != nil {
		t.Fatalf("OpenContainer() error = %v", err)
	}

	same := newTestBackend(t, &fakeRunner{}, nil)
	same.stateDir = shared
	useIdentity(same, server)
	entries, err := same.RecentContainers(context.Background())
	if err != nil {
		t.Fatalf("RecentContainers() error = %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "session:1" {
		t.Fatalf("entries = %#v, want the recorded session", entries)
	}

	restarted := newTestBackend(t, &fakeRunner{}, nil)
	restarted.stateDir = shared
	useIdentity(restarted, identity{HostID: "host-1", InstanceID: "instance-2"})
	entries, err = restarted.RecentContainers(context.Background())
	if err != nil {
		t.Fatalf("RecentContainers() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v, want no history for another server run", entries)
	}
}

func TestRecentContainersReportsAnUnreachableServer(t *testing.T) {
	runner := &fakeRunner{errors: map[string]error{whoamiKey: errUnavailable}}
	backend := newTestBackend(t, runner, nil)

	entries, err := backend.RecentContainers(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("RecentContainers() = %#v, %v; want a server error", entries, err)
	}
}

var errUnavailable = &unreachableError{}

type unreachableError struct{}

func (*unreachableError) Error() string { return "server unavailable" }

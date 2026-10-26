package rex

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/projects"
)

func sessionsJSON(entries ...string) string {
	return `{"sessions":[` + strings.Join(entries, ",") + `]}`
}

func sessionJSON(id, label string) string {
	return `{"session_id":"` + id + `","label":"` + label + `"}`
}

func blocksJSON(entries ...string) string {
	return `{"blocks":[` + strings.Join(entries, ",") + `]}`
}

func terminalBlockJSON(id, hostID string) string {
	return `{"block_id":"` + id + `","flavor":"com.superlogical.terminal.shell","host_id":"` + hostID + `"}`
}

func blockMethodPayload(sessionID, blockID, method string) string {
	return `{"session_id":"` + sessionID + `","block_id":"` + blockID + `","method":"` + method + `","args":{}}`
}

func addBlockPaths(fixtures map[string]string, sessionID, blockID, cwd string) {
	process := `{"foreground":{"cwd":"` + cwd + `"}}`
	if cwd == "" {
		process = `{}`
	}
	fixtures[api("block.method", blockMethodPayload(sessionID, blockID, "process"))] = process
	fixtures[api("block.method", blockMethodPayload(sessionID, blockID, "pwd"))] = `{"pwd":""}`
}

func createPayload(label, cwd string) string {
	return `{"label":"` + label + `","initial_windows":[{"layout":{"block":{"flavor":"com.superlogical.terminal.shell","options":{"cwd":"` + cwd + `"}}}}]}`
}

const createdSession = `{"session_id":"session:new","initial_windows":[{"window_id":"window:1","block_ids":["block:new"]}]}`

func TestOpenProjectCreatesAndFocusesANewSession(t *testing.T) {
	project := t.TempDir()
	runner := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): sessionsJSON(),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.create", createPayload(truncateLabel(projects.TargetName(project)), project)): createdSession,
		api("client.list", ""): oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:new"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}}
	backend := newTestBackend(t, runner, nil)

	opened, err := backend.OpenProject(context.Background(), project, capabilities.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	if opened.ID != "session:new" || !opened.Created || opened.Path != project {
		t.Fatalf("opened = %#v, want a created session for the project", opened)
	}
	if opened.Name != truncateLabel(projects.TargetName(project)) {
		t.Fatalf("opened.Name = %q, want the shared project name", opened.Name)
	}
}

func TestOpenProjectReusesTheRecordedSession(t *testing.T) {
	project := t.TempDir()
	shared := t.TempDir()
	target := truncateLabel(projects.TargetName(project))

	firstRunner := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): sessionsJSON(),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.create", createPayload(target, project)): createdSession,
		api("client.list", ""):                                oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:new"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}}
	first := newTestBackend(t, firstRunner, nil)
	first.stateDir = shared
	if _, err := first.OpenProject(context.Background(), project, capabilities.OpenOptions{}); err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	secondRunner := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): sessionsJSON(sessionJSON("session:new", "renamed_by_user")),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("client.list", ""):  oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:new"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}}
	second := newTestBackend(t, secondRunner, nil)
	second.stateDir = shared
	opened, err := second.OpenProject(context.Background(), project, capabilities.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	if opened.ID != "session:new" || opened.Created {
		t.Fatalf("opened = %#v, want the recorded session", opened)
	}
	if opened.Name != "renamed_by_user" {
		t.Fatalf("opened.Name = %q, want the session's current native label", opened.Name)
	}
	for _, key := range secondRunner.keys() {
		if strings.Contains(key, "session.create") || strings.Contains(key, "session.list_blocks") {
			t.Fatalf("commands = %#v, want a recorded reuse without create or adoption probes", secondRunner.keys())
		}
	}
}

func TestOpenProjectAdoptsTheSessionAlreadyInTheProject(t *testing.T) {
	project := t.TempDir()
	fixtures := map[string]string{
		api("session.list", ""): sessionsJSON(sessionJSON("session:1", "handmade")),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.list_blocks", `{"session_id":"session:1"}`): blocksJSON(terminalBlockJSON("block:1", "host-1")),
		api("client.list", ""): oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:1"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}
	addBlockPaths(fixtures, "session:1", "block:1", filepath.Join(project, "internal"))
	backend := newTestBackend(t, &fakeRunner{outputs: fixtures}, nil)

	opened, err := backend.OpenProject(context.Background(), project, capabilities.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	if opened.ID != "session:1" || opened.Created || opened.Name != "handmade" {
		t.Fatalf("opened = %#v, want the adopted session with its native label", opened)
	}
	index, err := backend.projectIndex(context.Background())
	if err != nil {
		t.Fatalf("projectIndex() error = %v", err)
	}
	recorded, ok, err := index.lookup(filepath.Clean(project))
	if err != nil || !ok || recorded != "session:1" {
		t.Fatalf("recorded = %q, %v, %v; want the adopted session in metadata", recorded, ok, err)
	}
}

func TestOpenProjectAdoptsByReportedPwdURL(t *testing.T) {
	project := t.TempDir()
	fixtures := map[string]string{
		api("session.list", ""): sessionsJSON(sessionJSON("session:1", "handmade")),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.list_blocks", `{"session_id":"session:1"}`):                   blocksJSON(terminalBlockJSON("block:1", "host-1")),
		api("block.method", blockMethodPayload("session:1", "block:1", "process")): `{}`,
		api("block.method", blockMethodPayload("session:1", "block:1", "pwd")):     `{"pwd":"file://` + project + `"}`,
		api("client.list", ""): oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:1"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}
	backend := newTestBackend(t, &fakeRunner{outputs: fixtures}, nil)

	opened, err := backend.OpenProject(context.Background(), project, capabilities.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	if opened.ID != "session:1" || opened.Created {
		t.Fatalf("opened = %#v, want the session adopted by its reported pwd", opened)
	}
}

func TestOpenProjectIgnoresALabelOnlyMatch(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	target := truncateLabel(projects.TargetName(project))
	fixtures := map[string]string{
		api("session.list", ""): sessionsJSON(sessionJSON("session:1", target)),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.list_blocks", `{"session_id":"session:1"}`): blocksJSON(terminalBlockJSON("block:1", "host-1")),
		api("session.create", createPayload(target, project)):    createdSession,
		api("client.list", ""):                                   oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:new"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}
	addBlockPaths(fixtures, "session:1", "block:1", other)
	backend := newTestBackend(t, &fakeRunner{outputs: fixtures}, nil)

	opened, err := backend.OpenProject(context.Background(), project, capabilities.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	if opened.ID != "session:new" || !opened.Created {
		t.Fatalf("opened = %#v, want a new session: a matching label is not identity", opened)
	}
}

func TestOpenProjectChoosesTheMostRecentlyUsedCandidate(t *testing.T) {
	project := t.TempDir()
	fixtures := map[string]string{
		api("session.list", ""): sessionsJSON(
			sessionJSON("session:1", "first"),
			sessionJSON("session:2", "second"),
		),
		whoamiKey: `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.list_blocks", `{"session_id":"session:1"}`): blocksJSON(terminalBlockJSON("block:1", "host-1")),
		api("session.list_blocks", `{"session_id":"session:2"}`): blocksJSON(terminalBlockJSON("block:2", "host-1")),
		api("client.list", ""): oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:2"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}
	for _, probe := range []struct{ session, block string }{{"session:1", "block:1"}, {"session:2", "block:2"}} {
		addBlockPaths(fixtures, probe.session, probe.block, filepath.Join(project, probe.session))
	}
	backend := newTestBackend(t, &fakeRunner{outputs: fixtures}, nil)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})
	if err := backend.recordContainer(context.Background(), containerstate.Entry{ID: "session:2", Name: "second", Machine: containerstate.LocalMachine()}); err != nil {
		t.Fatalf("recordContainer() error = %v", err)
	}

	opened, err := backend.OpenProject(context.Background(), project, capabilities.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	if opened.ID != "session:2" || opened.Created {
		t.Fatalf("opened = %#v, want the most recently used candidate", opened)
	}
}

func TestOpenProjectCreatesWhenCandidatesAreAmbiguousAndUnrecorded(t *testing.T) {
	project := t.TempDir()
	target := truncateLabel(projects.TargetName(project))
	fixtures := map[string]string{
		api("session.list", ""): sessionsJSON(
			sessionJSON("session:1", target),
			sessionJSON("session:2", target),
		),
		whoamiKey: `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.list_blocks", `{"session_id":"session:1"}`): blocksJSON(terminalBlockJSON("block:1", "host-1")),
		api("session.list_blocks", `{"session_id":"session:2"}`): blocksJSON(terminalBlockJSON("block:2", "host-1")),
		api("session.create", createPayload(target, project)):    createdSession,
		api("client.list", ""): oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:new"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}
	for _, probe := range []struct{ session, block string }{{"session:1", "block:1"}, {"session:2", "block:2"}} {
		addBlockPaths(fixtures, probe.session, probe.block, project)
	}
	backend := newTestBackend(t, &fakeRunner{outputs: fixtures}, nil)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})

	opened, err := backend.OpenProject(context.Background(), project, capabilities.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	if opened.ID != "session:new" || !opened.Created {
		t.Fatalf("opened = %#v, want a new session when no candidate has history", opened)
	}
}

func TestOpenProjectSkipsSessionsOnAnotherHost(t *testing.T) {
	project := t.TempDir()
	target := truncateLabel(projects.TargetName(project))
	fixtures := map[string]string{
		api("session.list", ""): sessionsJSON(sessionJSON("session:1", target)),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.list_blocks", `{"session_id":"session:1"}`): blocksJSON(terminalBlockJSON("block:1", "host-2")),
		api("session.create", createPayload(target, project)):    createdSession,
		api("client.list", ""):                                   oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:new"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}
	backend := newTestBackend(t, &fakeRunner{outputs: fixtures}, nil)

	opened, err := backend.OpenProject(context.Background(), project, capabilities.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	if opened.ID != "session:new" || !opened.Created {
		t.Fatalf("opened = %#v, want a local session: a remote block proves nothing", opened)
	}
}

func TestOpenProjectHonoursAnExplicitSessionID(t *testing.T) {
	project := t.TempDir()
	runner := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): sessionsJSON(sessionJSON("session:7", "chosen")),
		api("client.list", ""):  oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:7"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
		whoamiKey: `{"host_id":"host-1","instance_id":"instance-1"}`,
	}}
	backend := newTestBackend(t, runner, nil)

	opened, err := backend.OpenProject(context.Background(), project, capabilities.OpenOptions{ContainerID: "session:7"})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	if opened.ID != "session:7" || opened.Created {
		t.Fatalf("opened = %#v, want the explicit session", opened)
	}
	for _, key := range runner.keys() {
		if strings.Contains(key, "session.list_blocks") || strings.Contains(key, "session.create") {
			t.Fatalf("commands = %#v, want id-only resolution", runner.keys())
		}
	}
}

func TestOpenProjectReportsAnUnknownExplicitSessionID(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{api("session.list", ""): sessionsJSON()}}
	backend := newTestBackend(t, runner, nil)

	_, err := backend.OpenProject(context.Background(), t.TempDir(), capabilities.OpenOptions{ContainerID: "session:missing"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("OpenProject() error = %v, want a missing session error", err)
	}
}

func TestOpenProjectWithoutFocusNeedsNoApp(t *testing.T) {
	project := t.TempDir()
	target := truncateLabel(projects.TargetName(project))
	fixtures := map[string]string{
		api("session.list", ""): sessionsJSON(),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.create", createPayload(target, project)): createdSession,
	}
	backend := newTestBackend(t, &fakeRunner{outputs: fixtures}, nil)

	opened, err := backend.OpenProject(context.Background(), project, capabilities.OpenOptions{NoFocus: true})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	if opened.ID != "session:new" || !opened.Created {
		t.Fatalf("opened = %#v, want a created session without focus", opened)
	}
}

func TestProjectActiveFindsTerminalBlocksInTheProject(t *testing.T) {
	project := t.TempDir()
	fixtures := map[string]string{
		api("session.list", ""): sessionsJSON(sessionJSON("session:1", "work")),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.list_blocks", `{"session_id":"session:1"}`): blocksJSON(
			terminalBlockJSON("block:1", "host-1"),
			`{"block_id":"block:2","flavor":"com.superlogical.terminal.shell","host_id":"host-1","placement":"detached"}`,
		),
		api("block.method", blockMethodPayload("session:1", "block:1", "process")): `{"foreground":{"cwd":"` + t.TempDir() + `"}}`,
		api("block.method", blockMethodPayload("session:1", "block:1", "pwd")):     `{"pwd":""}`,
		api("block.method", blockMethodPayload("session:1", "block:2", "process")): `{"foreground":{"cwd":"` + filepath.Join(project, "pkg") + `"}}`,
		api("block.method", blockMethodPayload("session:1", "block:2", "pwd")):     `{"pwd":""}`,
	}
	backend := newTestBackend(t, &fakeRunner{outputs: fixtures}, nil)

	active, err := backend.ProjectActive(context.Background(), project)
	if err != nil {
		t.Fatalf("ProjectActive() error = %v", err)
	}
	if !active {
		t.Fatal("ProjectActive() = false, want the detached block to count as in use")
	}
}

func TestProjectActiveIsFalseForAnUnusedProject(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	fixtures := map[string]string{
		api("session.list", ""): sessionsJSON(sessionJSON("session:1", "work")),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.list_blocks", `{"session_id":"session:1"}`): blocksJSON(terminalBlockJSON("block:1", "host-1")),
	}
	addBlockPaths(fixtures, "session:1", "block:1", other)
	backend := newTestBackend(t, &fakeRunner{outputs: fixtures}, nil)

	active, err := backend.ProjectActive(context.Background(), project)
	if err != nil {
		t.Fatalf("ProjectActive() error = %v", err)
	}
	if active {
		t.Fatal("ProjectActive() = true, want an unused project")
	}
}

func TestProjectActiveReportsUninspectableBlocks(t *testing.T) {
	project := t.TempDir()
	cases := map[string]map[string]string{
		"unknown working directory": {
			api("session.list", ""): sessionsJSON(sessionJSON("session:1", "work")),
			whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
			api("session.list_blocks", `{"session_id":"session:1"}`):                   blocksJSON(terminalBlockJSON("block:1", "host-1")),
			api("block.method", blockMethodPayload("session:1", "block:1", "process")): `{}`,
			api("block.method", blockMethodPayload("session:1", "block:1", "pwd")):     `{}`,
		},
		"another host": {
			api("session.list", ""): sessionsJSON(sessionJSON("session:1", "work")),
			whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
			api("session.list_blocks", `{"session_id":"session:1"}`): blocksJSON(terminalBlockJSON("block:1", "host-2")),
		},
	}
	for name, fixtures := range cases {
		t.Run(name, func(t *testing.T) {
			backend := newTestBackend(t, &fakeRunner{outputs: fixtures}, nil)
			if _, err := backend.ProjectActive(context.Background(), project); err == nil {
				t.Fatal("ProjectActive() error = nil, want unreadable state to stay an error")
			}
		})
	}
}

func TestDecodePwdAcceptsPlainPathsAndLocalFileURLs(t *testing.T) {
	project := t.TempDir()
	plain, ok := decodePwd(project)
	if !ok || plain != project {
		t.Fatalf("decodePwd(%q) = %q, %v; want the plain path", project, plain, ok)
	}
	fromURL, ok := decodePwd("file://" + project)
	if !ok || fromURL != project {
		t.Fatalf("decodePwd(file URL) = %q, %v; want the decoded path", fromURL, ok)
	}
	if _, ok := decodePwd("file://another-host.example" + project); ok {
		t.Fatal("decodePwd() accepted another machine's path")
	}
	if _, ok := decodePwd("  "); ok {
		t.Fatal("decodePwd() accepted an empty pwd")
	}
}

func TestTruncateLabelKeepsRexLimit(t *testing.T) {
	long := strings.Repeat("a", labelLimit+5)
	if got := truncateLabel(long); got != strings.Repeat("a", labelLimit) {
		t.Fatalf("truncateLabel() = %q, want the stored limit", got)
	}
}

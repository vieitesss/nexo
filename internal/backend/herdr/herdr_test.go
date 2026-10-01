package herdr

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls            []call
	outputs          map[string][]byte
	errors           map[string]error
	metadataReported bool
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
	key := strings.Join(args, " ")
	if strings.HasPrefix(key, "workspace report-metadata ") {
		f.metadataReported = true
	}
	if f.metadataReported && key == "workspace list" {
		return []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"machine/container","tokens":{"nexo-proxy":"machine/container"}}]}}`), nil
	}
	if err := f.errors[key]; err != nil {
		return nil, err
	}
	if output, ok := f.outputs[key]; ok {
		return output, nil
	}
	if strings.HasPrefix(key, "workspace create --label machine/container --env") {
		return []byte(`{"result":{"type":"workspace_created","workspace":{"workspace_id":"w1"},"root_pane":{"pane_id":"p1"}}}`), nil
	}
	if strings.HasPrefix(key, "workspace create ") {
		return []byte(`{"result":{"type":"workspace_created","workspace":{"workspace_id":"workspace-created","label":"acme_app"},"root_pane":{"pane_id":"p1"}}}`), nil
	}
	if strings.HasPrefix(key, "worktree create ") {
		return []byte(`{"result":{"type":"workspace_created","workspace":{"workspace_id":"workspace-worktree","label":"worktrees_feature"},"root_pane":{"pane_id":"p1"}}}`), nil
	}
	// Agent location labels are read through Herdr's list commands. Keep the
	// older fixtures focused on state behavior by returning empty snapshots.
	switch key {
	case "workspace list":
		return []byte(`{"result":{"type":"workspace_list","workspaces":[]}}`), nil
	case "tab list":
		return []byte(`{"result":{"type":"tab_list","tabs":[]}}`), nil
	}
	return nil, nil
}

func TestListContainersUsesWorkspaceAndFocusedPane(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list":                    []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-1","label":"dev","focused":true},{"workspace_id":"workspace-2","label":"work"}]}}`),
		"pane list --workspace workspace-1": []byte(`{"result":{"type":"pane_list","panes":[{"pane_id":"pane-1","focused":false},{"pane_id":"pane-2","focused":true}]}}`),
		"pane list --workspace workspace-2": []byte(`{"result":{"type":"pane_list","panes":[{"pane_id":"pane-3","focused":false}]}}`),
	}}
	backend := New(runner)
	containers, err := backend.ListContainers(context.Background())
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	if len(containers) != 2 || containers[0].ID != "workspace-1" || containers[0].ActivePaneID != "pane-2" || containers[1].ActivePaneID != "pane-3" {
		t.Fatalf("containers = %#v, want focused panes", containers)
	}
}

func TestListContainersMapsConfiguredProxyLabelsToRemoteMachine(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list":              []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"proxy","label":"dev/api"},{"workspace_id":"local","label":"0"}]}}`),
		"pane list --workspace proxy": []byte(`{"result":{"type":"pane_list","panes":[]}}`),
		"pane list --workspace local": []byte(`{"result":{"type":"pane_list","panes":[]}}`),
	}}
	backend := New(runner)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"dev"}}, "")
	containers, err := backend.ListContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 2 || containers[0].Machine != "dev" || containers[0].Name != "api" || !containers[0].Proxy || containers[1].Machine == "dev" || containers[1].Name != "0" {
		t.Fatalf("containers = %#v, want remote proxy and local workspace", containers)
	}
}

func TestListContainersOmitsMarkedProxyWorkspaces(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list":               []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"normal","label":"renamed"},{"workspace_id":"proxy","label":"also-renamed","tokens":{"nexo-proxy":"dev/api"}}]}}`),
		"pane list --workspace normal": []byte(`{"result":{"type":"pane_list","panes":[]}}`),
	}}
	containers, err := New(runner).ListContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || containers[0].ID != "normal" || containers[0].Proxy {
		t.Fatalf("containers = %#v, want only unmarked workspace", containers)
	}
}

func TestContainerActionsUseNativeHerdrCommands(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner)
	if err := backend.OpenContainer(context.Background(), "workspace-1"); err != nil {
		t.Fatalf("OpenContainer() error = %v", err)
	}
	if err := backend.RenameContainer(context.Background(), "workspace-1", "renamed"); err != nil {
		t.Fatalf("RenameContainer() error = %v", err)
	}
	if err := backend.KillContainers(context.Background(), []string{"workspace-1", "workspace-2"}); err != nil {
		t.Fatalf("KillContainers() error = %v", err)
	}
	want := []call{
		{name: "herdr", args: []string{"workspace", "focus", "workspace-1"}},
		{name: "herdr", args: []string{"workspace", "rename", "workspace-1", "renamed"}},
		{name: "herdr", args: []string{"workspace", "close", "workspace-1"}},
		{name: "herdr", args: []string{"workspace", "close", "workspace-2"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestKillContainersKillsCurrentWorkspaceLast(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "pane-current")
	runner := &fakeRunner{outputs: map[string][]byte{
		"pane get pane-current": []byte(`{"result":{"type":"pane_info","pane":{"pane_id":"pane-current","workspace_id":"workspace-1","tab_id":"tab-1","terminal_id":"terminal-1","focused":true,"agent_status":"idle","revision":1}}}`),
	}}
	if err := New(runner).KillContainers(context.Background(), []string{"workspace-1", "workspace-2"}); err != nil {
		t.Fatalf("KillContainers() error = %v", err)
	}
	want := []call{
		{name: "herdr", args: []string{"pane", "get", "pane-current"}},
		{name: "herdr", args: []string{"workspace", "close", "workspace-2"}},
		{name: "herdr", args: []string{"workspace", "close", "workspace-1"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want current workspace last: %#v", runner.calls, want)
	}
}

func TestReadScreenDecodesNativePaneRead(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"pane read pane-1 --source visible": []byte(`{"result":{"type":"pane_read","read":{"text":"hello"}}}`),
	}}
	backend := New(runner)
	got, err := backend.ReadScreen(context.Background(), "pane-1")
	if err != nil || got != "hello" {
		t.Fatalf("ReadScreen() = %q, %v", got, err)
	}
}

func TestOpenProjectFocusesWorkspaceWithExactTargetLabel(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-exact","label":"acme_app"}]}}`),
	}}
	backend := New(runner)

	if _, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	want := []call{
		{name: "herdr", args: []string{"workspace", "list"}},
		{name: "herdr", args: []string{"workspace", "focus", "workspace-exact"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestOpenProjectFocusesWorkspaceWhoseLabelContainsDirectoryName(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-unrelated","label":"other"},{"workspace_id":"workspace-containing","label":"app-feature"}]}}`),
	}}
	backend := New(runner)

	if _, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	want := []call{
		{name: "herdr", args: []string{"workspace", "list"}},
		{name: "herdr", args: []string{"workspace", "focus", "workspace-containing"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestOpenProjectFocusesFirstWorkspaceWhenLabelsBothContainDirectoryName(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-first","label":"app-first"},{"workspace_id":"workspace-second","label":"app-second"}]}}`),
	}}
	backend := New(runner)

	if _, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	want := []call{
		{name: "herdr", args: []string{"workspace", "list"}},
		{name: "herdr", args: []string{"workspace", "focus", "workspace-first"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want first matching workspace: %#v", runner.calls, want)
	}
}

func TestOpenProjectCreatesWorkspaceWhenNoLabelMatches(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-other","label":"other"}]}}`),
	}}
	backend := New(runner)

	if _, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	want := []call{
		{name: "herdr", args: []string{"workspace", "list"}},
		{name: "herdr", args: []string{"workspace", "create", "--cwd", "/work/acme/app", "--label", "acme_app", "--focus"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestOpenProjectDoesNotUseOtherWorkspaceTabPath(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-other","label":"other","tabs":[{"cwd":"/work/acme/app"}]}]}}`),
	}}
	backend := New(runner)

	if _, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	if len(runner.calls) != 2 || !reflect.DeepEqual(runner.calls[1], call{name: "herdr", args: []string{"workspace", "create", "--cwd", "/work/acme/app", "--label", "acme_app", "--focus"}}) {
		t.Fatalf("calls = %#v, want list followed by create", runner.calls)
	}
}

func TestFocusPaneUsesExactAgentPaneID(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner)
	if err := backend.FocusPane(context.Background(), "w1:p2"); err != nil {
		t.Fatalf("FocusPane() error = %v", err)
	}
	want := []call{{name: "herdr", args: []string{"agent", "focus", "w1:p2"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestOpenProjectWorkspaceIDFocusReportsWorkspaceLabel(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-forced","label":"forced-label"}]}}`),
	}}
	backend := New(runner)

	container, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{ContainerID: "workspace-forced"})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	want := []call{
		{name: "herdr", args: []string{"workspace", "list"}},
		{name: "herdr", args: []string{"workspace", "focus", "workspace-forced"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
	if container.ID != "workspace-forced" || container.Name != "forced-label" || container.Path != "/work/acme/app" || container.Created {
		t.Fatalf("container = %#v, want focused workspace label", container)
	}
}

func TestOpenProjectWorkspaceIDPreservesEmptyNativeLabel(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-unlabeled","label":""}]}}`),
	}}
	backend := New(runner)

	container, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{ContainerID: "workspace-unlabeled", NoFocus: true})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	wantCalls := []call{{name: "herdr", args: []string{"workspace", "list"}}}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, wantCalls)
	}
	want := capabilities.OpenedContainer{ID: "workspace-unlabeled", Name: "", Path: "/work/acme/app", Created: false}
	if container != want {
		t.Fatalf("container = %#v, want native unlabeled workspace %#v", container, want)
	}
}

func TestOpenProjectNoFocusReusesWorkspaceWithoutFocus(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-exact","label":"acme_app"}]}}`),
	}}
	backend := New(runner)

	container, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{NoFocus: true})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	want := []call{{name: "herdr", args: []string{"workspace", "list"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want no workspace focus: %#v", runner.calls, want)
	}
	if container.ID != "workspace-exact" || container.Name != "acme_app" || container.Path != "/work/acme/app" || container.Created {
		t.Fatalf("container = %#v, want reused workspace", container)
	}
}

func TestOpenProjectNoFocusReusesWorkspaceByFuzzyLabel(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-fuzzy","label":"dev/app"}]}}`),
	}}
	backend := New(runner)

	container, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{NoFocus: true})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	want := []call{{name: "herdr", args: []string{"workspace", "list"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want no workspace focus: %#v", runner.calls, want)
	}
	if container.ID != "workspace-fuzzy" || container.Name != "dev/app" || container.Path != "/work/acme/app" || container.Created {
		t.Fatalf("container = %#v, want reused fuzzy workspace", container)
	}
}

func TestOpenProjectNoFocusCreatesWorkspaceWithoutFocusFlag(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-other","label":"other"}]}}`),
		"workspace create --cwd /work/acme/app --label acme_app": []byte(`{"result":{"type":"workspace_created","workspace":{"workspace_id":"workspace-new","label":"acme_app"}}}`),
	}}
	backend := New(runner)

	container, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{NoFocus: true})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	want := []call{
		{name: "herdr", args: []string{"workspace", "list"}},
		{name: "herdr", args: []string{"workspace", "create", "--cwd", "/work/acme/app", "--label", "acme_app"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want create without --focus: %#v", runner.calls, want)
	}
	if container.ID != "workspace-new" || container.Name != "acme_app" || !container.Created {
		t.Fatalf("container = %#v, want created workspace", container)
	}
}

func TestOpenProjectNoFocusWorkspaceIDVerifiesExisting(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-1","label":"renamed"}]}}`),
	}}
	backend := New(runner)

	container, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{ContainerID: "workspace-1", NoFocus: true})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	want := []call{{name: "herdr", args: []string{"workspace", "list"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want verification only: %#v", runner.calls, want)
	}
	if container.ID != "workspace-1" || container.Name != "renamed" || container.Created {
		t.Fatalf("container = %#v, want existing workspace", container)
	}
}

func TestOpenProjectNoFocusWorkspaceIDRejectsMissing(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[]}}`),
	}}
	backend := New(runner)

	if _, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{ContainerID: "missing", NoFocus: true}); err == nil {
		t.Fatal("OpenProject() error = nil, want missing workspace error")
	}
}

func TestOpenProjectRejectsCreateWithoutWorkspaceID(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[]}}`),
		"workspace create --cwd /work/acme/app --label acme_app": []byte(`{"result":{"type":"workspace_created","workspace":{}}}`),
	}}
	backend := New(runner)

	if _, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{NoFocus: true}); err == nil {
		t.Fatal("OpenProject() error = nil, want missing workspace id error")
	}
}

func TestOpenProjectRejectsMalformedCreateOutput(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[]}}`),
		"workspace create --cwd /work/acme/app --label acme_app": []byte("not json"),
	}}
	backend := New(runner)

	if _, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{NoFocus: true}); err == nil {
		t.Fatal("OpenProject() error = nil, want malformed output error")
	}
}

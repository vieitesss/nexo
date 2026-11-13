package herdr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
		return []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"machine/container","tokens":{"nexo-proxy":"machine"}}]}}`), nil
	}
	if err := f.errors[key]; err != nil {
		return nil, err
	}
	if output, ok := f.outputs[key]; ok {
		return output, nil
	}
	if strings.HasPrefix(key, "workspace create --label machine --env") {
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

type partialWorktreeRunner struct{}

func (partialWorktreeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "herdr" && len(args) > 0 && args[0] == "worktree" && args[1] == "create" {
		var repo, destination, branch string
		for index := 0; index < len(args); index++ {
			switch args[index] {
			case "--cwd":
				repo = args[index+1]
			case "--path":
				destination = args[index+1]
			case "--branch":
				branch = args[index+1]
			}
		}
		output, err := exec.CommandContext(ctx, "git", "-C", repo, "worktree", "add", destination, branch).CombinedOutput()
		if err != nil {
			return output, err
		}
		return []byte("malformed response"), nil
	}
	if name == "herdr" {
		return nil, nil
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func runPartialHerdrGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestExtractWorktreeCleansPartialNativeCreate(t *testing.T) {
	repo := t.TempDir()
	runPartialHerdrGit(t, repo, "init", "-q")
	runPartialHerdrGit(t, repo, "config", "user.email", "test@example.com")
	runPartialHerdrGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runPartialHerdrGit(t, repo, "add", "README")
	runPartialHerdrGit(t, repo, "commit", "-qm", "initial")
	base := runPartialHerdrGit(t, repo, "branch", "--show-current")
	runPartialHerdrGit(t, repo, "checkout", "-qb", "feature")
	before := runPartialHerdrGit(t, repo, "rev-parse", "HEAD")
	destination := filepath.Join(t.TempDir(), "feature")

	_, err := New(partialWorktreeRunner{}).ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, ReturnBranch: base, NoFocus: true,
	})
	if err == nil || !strings.Contains(err.Error(), "decode herdr created workspace") {
		t.Fatalf("ExtractWorktree() error = %v, want malformed response", err)
	}
	if runPartialHerdrGit(t, repo, "branch", "--show-current") != "feature" || runPartialHerdrGit(t, repo, "rev-parse", "HEAD") != before {
		t.Fatalf("original branch or HEAD changed after partial create")
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination after partial create = %v", statErr)
	}
	listing := runPartialHerdrGit(t, repo, "worktree", "list", "--porcelain")
	if strings.Contains(listing, destination) || !strings.Contains(listing, "branch refs/heads/feature") {
		t.Fatalf("worktree list after partial create = %s", listing)
	}
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

func TestListContainersDoesNotMapWorkspaceLabelsToRemoteProxies(t *testing.T) {
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
	if len(containers) != 2 || containers[0].Proxy || containers[0].Machine == "dev" || containers[0].Name != "dev/api" || containers[1].Proxy || containers[1].Machine == "dev" || containers[1].Name != "0" {
		t.Fatalf("containers = %#v, want untagged workspaces as local rows", containers)
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
		{name: "herdr", args: []string{"workspace", "list"}},
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
		{name: "herdr", args: []string{"workspace", "list"}},
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
		{name: "herdr", args: []string{"workspace", "list"}},
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
		{name: "herdr", args: []string{"workspace", "list"}},
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
		{name: "herdr", args: []string{"workspace", "list"}},
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

	if len(runner.calls) != 3 || !reflect.DeepEqual(runner.calls[1], call{name: "herdr", args: []string{"workspace", "create", "--cwd", "/work/acme/app", "--label", "acme_app", "--focus"}}) {
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
		{name: "herdr", args: []string{"workspace", "list"}},
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

func TestOpenProjectUsesConfiguredName(t *testing.T) {
	for _, tc := range []struct {
		name, template, list, responseLabel, want string
		id                                        string
		created                                   bool
	}{
		{name: "exact before substring", template: "{org}/{repo}", list: `{"workspace_id":"other","label":"my-api"},{"workspace_id":"chosen","label":"acme/api"}`, want: "acme/api", id: "chosen"},
		{name: "directory fallback", template: "{org}/{repo}", list: `{"workspace_id":"chosen","label":"my-api"}`, want: "my-api", id: "chosen"},
		{name: "create", template: "{org}/{repo}", responseLabel: "acme/api", want: "acme/api", id: "new", created: true},
		{name: "empty created label", template: "{org}/{repo}", want: "acme/api", id: "new", created: true},
		{name: "leading dash", template: "-task-{repo}", responseLabel: "-task-api", want: "-task-api", id: "new", created: true},
		{name: "literal shell text", template: "$(literal)-{repo}", responseLabel: "$(literal)-api", want: "$(literal)-api", id: "new", created: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "acme", "api")
			generated := "acme/api"
			if strings.HasPrefix(tc.template, "-") {
				generated = "-task-api"
			}
			if strings.HasPrefix(tc.template, "$") {
				generated = "$(literal)-api"
			}
			create := []string{"workspace", "create", "--cwd", path, "--label", generated}
			r := &fakeRunner{outputs: map[string][]byte{"workspace list": []byte(`{"result":{"workspaces":[` + tc.list + `]}}`), strings.Join(create, " "): []byte(`{"workspace":{"workspace_id":"new","label":"` + tc.responseLabel + `"}}`)}}
			b := New(r)
			b.NameTemplate = &tc.template
			got, err := b.OpenProject(context.Background(), path, capabilities.OpenOptions{NoFocus: true})
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != tc.want || got.ID != tc.id || got.Created != tc.created {
				t.Fatalf("container = %#v", got)
			}
			want := []call{{"herdr", []string{"workspace", "list"}}}
			if tc.created {
				want = append(want, call{"herdr", create})
			}
			if !reflect.DeepEqual(r.calls, want) {
				t.Fatalf("calls = %#v, want %#v", r.calls, want)
			}
		})
	}
	for _, label := range []string{"native", ""} {
		t.Run("explicit ID "+label, func(t *testing.T) {
			path := t.TempDir()
			if err := os.Mkdir(filepath.Join(path, ".git"), 0700); err != nil {
				t.Fatal(err)
			}
			template := "{team}" // Would fail if naming were attempted.
			r := &fakeRunner{outputs: map[string][]byte{"workspace list": []byte(`{"workspaces":[{"workspace_id":"chosen","label":"` + label + `"}]}`)}}
			b := New(r)
			b.NameTemplate = &template
			got, err := b.OpenProject(context.Background(), path, capabilities.OpenOptions{ContainerID: "chosen", NoFocus: true})
			if err != nil || got.Name != label || got.Created {
				t.Fatalf("adopted = %#v, %v", got, err)
			}
			if !reflect.DeepEqual(r.calls, []call{{"herdr", []string{"workspace", "list"}}}) {
				t.Fatalf("calls = %#v", r.calls)
			}
		})
	}
	for _, cancelled := range []bool{false, true} {
		template := "{team}"
		r := &fakeRunner{}
		b := New(r)
		b.NameTemplate = &template
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		_, err := b.OpenProject(ctx, "/work/acme/api", capabilities.OpenOptions{NoFocus: true})
		cancel()
		if err == nil || (cancelled && !errors.Is(err, context.Canceled)) {
			t.Fatalf("error = %v", err)
		}
		if !reflect.DeepEqual(r.calls, []call{{"herdr", []string{"workspace", "list"}}}) {
			t.Fatalf("calls = %#v", r.calls)
		}
	}
}

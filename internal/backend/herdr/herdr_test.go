package herdr

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls   []call
	outputs map[string][]byte
	errors  map[string]error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
	key := strings.Join(args, " ")
	if err := f.errors[key]; err != nil {
		return nil, err
	}
	return f.outputs[key], nil
}

func TestOpenProjectFocusesWorkspaceWithExactTargetLabel(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[{"workspace_id":"workspace-exact","label":"acme_app"}]}}`),
	}}
	backend := New(runner)

	if err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
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

	if err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
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

	if err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
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

	if err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
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

	if err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	if len(runner.calls) != 2 || !reflect.DeepEqual(runner.calls[1], call{name: "herdr", args: []string{"workspace", "create", "--cwd", "/work/acme/app", "--label", "acme_app", "--focus"}}) {
		t.Fatalf("calls = %#v, want list followed by create", runner.calls)
	}
}

func TestOpenProjectWorkspaceIDForcesFocus(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner)

	if err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{ContainerID: "workspace-forced"}); err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}

	want := []call{{name: "herdr", args: []string{"workspace", "focus", "workspace-forced"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

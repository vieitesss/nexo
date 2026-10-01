package herdr

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectActiveChecksWorkspacePaths(t *testing.T) {
	setProjectSafetyEnv(t)
	backend := New(projectUseRunner{output: []byte(`{"workspaces":[{"workspace_id":"one","path":"/tmp/project/src"}]}`)})
	active, err := backend.ProjectActive(context.Background(), "/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("ProjectActive() = false for descendant workspace")
	}
}

func TestProjectActiveChecksEveryWorkspaceAndPanePath(t *testing.T) {
	setProjectSafetyEnv(t)
	runner := &projectUsageRunner{outputs: map[string][]byte{
		"workspace list":            []byte(`{"workspaces":[{"workspace_id":"one","path":"/projects/root","pane_count":3,"tabs":[{"panes":[{"cwd":"/projects/inline-tab/src"}]}]}]}`),
		"pane list --workspace one": []byte(`{"result":{"type":"pane_list","panes":[{"pane_id":"p1","cwd":"/projects/root"},{"pane_id":"p2","cwd":"/projects/other/src"},{"pane_id":"p3","cwd":"/projects/inline-tab/src"}]}}`),
	}}
	backend := New(runner)
	for _, candidate := range []string{"/projects/root", "/projects/other", "/projects/inline-tab"} {
		active, err := backend.ProjectActive(context.Background(), candidate)
		if err != nil {
			t.Fatalf("ProjectActive(%q) error = %v", candidate, err)
		}
		if !active {
			t.Fatalf("ProjectActive(%q) = false, want pane usage", candidate)
		}
	}
}

type projectUsageRunner struct {
	outputs map[string][]byte
}

func (r *projectUsageRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	if output, ok := r.outputs[key]; ok {
		return output, nil
	}
	return nil, errors.New("unexpected Herdr command: " + key)
}

func TestProjectActiveFailsClosedWhenWorkspacePathIsUnknown(t *testing.T) {
	setProjectSafetyEnv(t)
	backend := New(projectUseRunner{output: []byte(`{"workspaces":[{"workspace_id":"one"}]}`)})
	if _, err := backend.ProjectActive(context.Background(), "/tmp/project"); err == nil {
		t.Fatal("ProjectActive() error = nil without workspace path")
	}
	backend = New(projectUseRunner{err: errors.New("workspace service unavailable")})
	if _, err := backend.ProjectActive(context.Background(), "/tmp/project"); err == nil {
		t.Fatal("ProjectActive() error = nil when workspace listing fails")
	}
}

type projectUseRunner struct {
	output []byte
	err    error
}

func (r projectUseRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	if strings.HasPrefix(strings.Join(args, " "), "pane list ") {
		return []byte(`{"result":{"type":"pane_list","panes":[]}}`), nil
	}
	return r.output, r.err
}

func setProjectSafetyEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
}

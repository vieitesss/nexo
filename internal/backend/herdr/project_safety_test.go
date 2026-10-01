package herdr

import (
	"context"
	"errors"
	"path/filepath"
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

func (r projectUseRunner) Run(context.Context, string, ...string) ([]byte, error) {
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

package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/projects"
)

func TestCreateWorktreePreflightsAndNormalizesThroughAdapter(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "feature")
	var gotOptions capabilities.WorktreeCreateOptions
	var gotOpenOptions capabilities.OpenOptions
	adapter := WorktreeAdapter{
		Create: func(_ context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
			gotOptions = options
			return capabilities.Worktree{Branch: "", Repo: ""}, nil
		},
		Open: func(_ context.Context, _ string, options capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
			gotOpenOptions = options
			return capabilities.OpenedContainer{ID: "$7"}, nil
		},
	}

	got, err := CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: "/repo", Branch: "feature", Destination: destination, NewBranch: true, NoFocus: true,
	}, adapter)
	if err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	if gotOptions.Destination != destination || !gotOptions.NewBranch || !gotOptions.NoFocus {
		t.Fatalf("create options = %#v, want normalized destination and flags", gotOptions)
	}
	if gotOpenOptions != (capabilities.OpenOptions{NoFocus: true}) {
		t.Fatalf("open options = %#v, want no focus", gotOpenOptions)
	}
	if got.Path != destination || got.Branch != "feature" || got.Repo != "/repo" || !got.NewBranch {
		t.Fatalf("worktree = %#v, want normalized result", got)
	}
	wantContainer := capabilities.OpenedContainer{ID: "$7", Path: destination}
	if !reflect.DeepEqual(got.Container, wantContainer) {
		t.Fatalf("container = %#v, want %#v", got.Container, wantContainer)
	}
}

func TestCreateWorktreeSkipsOpenWhenNativeResultContainsContainer(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "feature")
	opened := false
	got, err := CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{Destination: destination}, WorktreeAdapter{
		Create: func(context.Context, capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
			return capabilities.Worktree{ContainerID: "workspace-1", Container: capabilities.OpenedContainer{ID: "workspace-1", Created: true}}, nil
		},
		Open: func(context.Context, string, capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
			opened = true
			return capabilities.OpenedContainer{}, nil
		},
	})
	if err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	if opened {
		t.Fatal("Open() called for native result that already contains a container")
	}
	if got.ContainerID != "workspace-1" || got.Container.Path != destination {
		t.Fatalf("worktree = %#v, want native container with default path", got)
	}
}

func TestCreateWorktreeRollsBackWhenOpenFails(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "feature")
	openErr := errors.New("focus failed")
	rollbackErr := errors.New("cleanup failed")
	var rolledBack capabilities.Worktree
	got, err := CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: "/repo", Branch: "feature", Destination: destination,
	}, WorktreeAdapter{
		Create: func(context.Context, capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
			return capabilities.Worktree{}, nil
		},
		Open: func(context.Context, string, capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
			return capabilities.OpenedContainer{ID: "$9", Created: true}, openErr
		},
		Rollback: func(_ context.Context, worktree capabilities.Worktree) error {
			rolledBack = worktree
			return rollbackErr
		},
	})
	if got != (capabilities.Worktree{}) {
		t.Fatalf("worktree = %#v, want zero result on open failure", got)
	}
	if !errors.Is(err, openErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("error = %v, want open and rollback failures", err)
	}
	if rolledBack.Container.ID != "$9" || rolledBack.Container.Path != destination || rolledBack.Path != destination {
		t.Fatalf("rollback worktree = %#v, want normalized result", rolledBack)
	}
}

func TestCreateWorktreeRejectsExistingDestinationBeforeAdapter(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{Destination: destination}, WorktreeAdapter{
		Create: func(context.Context, capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
			called = true
			return capabilities.Worktree{}, nil
		},
	})
	if err == nil {
		t.Fatal("CreateWorktree() error = nil, want existing destination error")
	}
	if called {
		t.Fatal("Create() called for existing destination")
	}
}

func TestOpenProjectPreservesExistingContainerNativeLabel(t *testing.T) {
	path := "/work/acme/app"
	got, err := OpenProject(context.Background(), path, capabilities.OpenOptions{NoFocus: true}, func(_ context.Context, gotPath string, options capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
		if gotPath != path || !options.NoFocus {
			t.Fatalf("adapter arguments = %q, %#v", gotPath, options)
		}
		return capabilities.OpenedContainer{ID: "workspace-1"}, nil
	})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	want := capabilities.OpenedContainer{ID: "workspace-1", Path: path}
	if got != want {
		t.Fatalf("container = %#v, want native empty label preserved as %#v", got, want)
	}
}

func TestOpenProjectDefaultsCreatedContainerName(t *testing.T) {
	path := "/work/acme/app"
	got, err := OpenProject(context.Background(), path, capabilities.OpenOptions{}, func(context.Context, string, capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
		return capabilities.OpenedContainer{ID: "workspace-2", Created: true}, nil
	})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	want := capabilities.OpenedContainer{ID: "workspace-2", Name: projects.TargetName(path), Path: path, Created: true}
	if got != want {
		t.Fatalf("container = %#v, want created default %#v", got, want)
	}
}

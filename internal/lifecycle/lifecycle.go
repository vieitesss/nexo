// Package lifecycle owns the shared open and worktree transaction policy.
// Backend adapters provide the native operations at this module's seams.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/projects"
)

// ProjectAdapter is the native project-open operation supplied by a backend.
// The lifecycle module keeps result defaults in one place while the adapter
// retains container lookup, creation, and focus semantics.
type ProjectAdapter func(context.Context, string, capabilities.OpenOptions) (capabilities.OpenedContainer, error)

// WorktreeAdapter is the native worktree seam supplied by a backend. Create
// owns native worktree creation and its immediate rollback; Open is used when
// creation does not itself return an opened container. Rollback owns cleanup
// after a successful native create cannot be opened.
type WorktreeAdapter struct {
	Create   func(context.Context, capabilities.WorktreeCreateOptions) (capabilities.Worktree, error)
	Open     ProjectAdapter
	Rollback func(context.Context, capabilities.Worktree) error
}

// OpenProject applies the shared result contract to a backend's native open
// operation. The adapter remains responsible for stable identity lookup,
// creation defaults, and whether focus is requested. An existing container's
// native label is returned unchanged, including when it is empty.
func OpenProject(ctx context.Context, path string, options capabilities.OpenOptions, adapter ProjectAdapter) (capabilities.OpenedContainer, error) {
	if adapter == nil {
		return capabilities.OpenedContainer{}, errors.New("project lifecycle requires an adapter")
	}
	container, err := adapter(ctx, path, options)
	container = normalizeContainer(path, container)
	if err != nil {
		return container, err
	}
	return container, nil
}

// CreateWorktree performs destination preflight, normalizes the worktree and
// opened-container results, and sequences cleanup when the open seam fails.
// Native command construction and backend-specific rollback stay in the
// adapter, keeping this module deep without unifying backend semantics.
func CreateWorktree(ctx context.Context, options capabilities.WorktreeCreateOptions, adapter WorktreeAdapter) (capabilities.Worktree, error) {
	if adapter.Create == nil {
		return capabilities.Worktree{}, errors.New("worktree lifecycle requires a create adapter")
	}
	path, err := preflightDestination(options.Destination)
	if err != nil {
		return capabilities.Worktree{}, err
	}
	options.Destination = path

	result, err := adapter.Create(ctx, options)
	if err != nil {
		return capabilities.Worktree{}, err
	}
	result = normalizeWorktree(options, result)
	nativeContainer := hasContainer(result)
	if nativeContainer {
		result.Container = normalizeContainer(result.Path, result.Container)
		return result, nil
	}
	if adapter.Open == nil {
		openErr := errors.New("worktree lifecycle requires an open adapter")
		if adapter.Rollback == nil {
			return capabilities.Worktree{}, openErr
		}
		if rollbackErr := adapter.Rollback(ctx, result); rollbackErr != nil {
			return capabilities.Worktree{}, errors.Join(openErr, rollbackErr)
		}
		return capabilities.Worktree{}, openErr
	}

	container, openErr := adapter.Open(ctx, path, capabilities.OpenOptions{NoFocus: options.NoFocus})
	result.Container = normalizeContainer(path, container)
	result = normalizeWorktree(options, result)
	result.Container = normalizeContainer(path, result.Container)
	if openErr == nil {
		return result, nil
	}
	if adapter.Rollback == nil {
		return capabilities.Worktree{}, openErr
	}
	if rollbackErr := adapter.Rollback(ctx, result); rollbackErr != nil {
		return capabilities.Worktree{}, errors.Join(openErr, rollbackErr)
	}
	return capabilities.Worktree{}, openErr
}

func preflightDestination(destination string) (string, error) {
	path, err := filepath.Abs(destination)
	if err != nil {
		return "", fmt.Errorf("resolve worktree destination: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("worktree destination %q already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("check worktree destination %q: %w", path, err)
	}
	return path, nil
}

func normalizeContainer(path string, container capabilities.OpenedContainer) capabilities.OpenedContainer {
	if container.Path == "" {
		container.Path = path
	}
	// A name on a reused container is the backend's native label and stays
	// authoritative, even when it is empty. Only a container this open created
	// may take the shared default; adapters already apply the same default on
	// their native create paths.
	if container.Created && container.Name == "" && path != "" {
		container.Name = projects.TargetName(path)
	}
	return container
}

func normalizeWorktree(options capabilities.WorktreeCreateOptions, result capabilities.Worktree) capabilities.Worktree {
	if result.Path == "" {
		result.Path = options.Destination
	}
	if result.Branch == "" {
		result.Branch = options.Branch
	}
	if result.Repo == "" {
		result.Repo = options.Repo
	}
	result.NewBranch = options.NewBranch
	if result.ContainerID == "" {
		result.ContainerID = result.Container.ID
	}
	if result.Container.ID == "" {
		result.Container.ID = result.ContainerID
	}
	if result.SessionName == "" && result.Container.Created {
		result.SessionName = result.Container.Name
	}
	return result
}

func hasContainer(result capabilities.Worktree) bool {
	container := result.Container
	return result.ContainerID != "" || container.ID != "" || container.Name != "" || container.Path != "" || container.Created
}

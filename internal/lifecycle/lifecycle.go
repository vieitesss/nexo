// Package lifecycle owns the shared open and worktree transaction policy.
// Backend adapters provide the native operations at this module's seams.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/destination"
	"github.com/vieitesss/nexo/internal/projects"
)

// ProjectAdapter is the native project-open operation supplied by a backend.
// The lifecycle module keeps result defaults in one place while the adapter
// retains container lookup, creation, and focus semantics.
type ProjectAdapter func(context.Context, string, capabilities.OpenOptions) (capabilities.OpenedContainer, error)

// WorktreeAdapter is the worktree seam supplied by a backend. Create owns
// creation and its immediate rollback; Open is used when creation does not
// itself return an opened container. Rollback owns cleanup after a successful
// create cannot be opened. Native container lifetime stays in the backend.
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
	path, err := PreflightProjectPath(path)
	if err != nil {
		return capabilities.OpenedContainer{}, err
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
	return createAndOpenWorktree(ctx, options, adapter)
}

// createAndOpenWorktree runs the native create and open steps with their
// shared result normalization and rollback. Extraction shares it so both
// transactions keep identical open and cleanup semantics.
func createAndOpenWorktree(ctx context.Context, options capabilities.WorktreeCreateOptions, adapter WorktreeAdapter) (capabilities.Worktree, error) {
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
		cleanupCtx, cancel := CleanupContext(ctx)
		defer cancel()
		if rollbackErr := adapter.Rollback(cleanupCtx, result); rollbackErr != nil {
			return capabilities.Worktree{}, errors.Join(openErr, rollbackErr)
		}
		return capabilities.Worktree{}, openErr
	}

	container, openErr := OpenProject(ctx, options.Destination, capabilities.OpenOptions{NoFocus: options.NoFocus}, adapter.Open)
	result.Container = container
	result = normalizeWorktree(options, result)
	if openErr == nil {
		return result, nil
	}
	if adapter.Rollback == nil {
		return capabilities.Worktree{}, openErr
	}
	cleanupCtx, cancel := CleanupContext(ctx)
	defer cancel()
	if rollbackErr := adapter.Rollback(cleanupCtx, result); rollbackErr != nil {
		return capabilities.Worktree{}, errors.Join(openErr, rollbackErr)
	}
	return capabilities.Worktree{}, openErr
}

// PreflightProjectPath returns the lexical absolute path used by every
// project-open operation. It deliberately does not resolve symlinks: a
// configured symlink is a project candidate in its own right.
func PreflightProjectPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("project path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	return filepath.Clean(absolute), nil
}

func preflightDestination(path string) (string, error) {
	result, err := destination.New(destination.Options{AllowMissingParent: true}).Resolve(path)
	if err != nil {
		return "", fmt.Errorf("resolve worktree destination: %w", err)
	}
	return result.Path, nil
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

// CleanupContext returns a bounded context that is not canceled when the
// caller abandons an operation after mutation has begun. Recovery must still
// have a deadline so a broken native command cannot hang forever.
func CleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
}

func hasContainer(result capabilities.Worktree) bool {
	container := result.Container
	return result.ContainerID != "" || container.ID != "" || container.Name != "" || container.Path != "" || container.Created
}

package deletion

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/vieitesss/nexo/internal/runner"
)

// Trash moves one path to the operating system trash. It must not fall back to
// permanent deletion when the platform operation fails.
type Trash interface {
	Trash(context.Context, string) error
}

// CommandTrash delegates directly to the platform trash operation. A failed
// utility is an error; it never becomes permanent removal.
type CommandTrash struct {
	Runner runner.CommandRunner
	GOOS   string
}

func (t CommandTrash) Trash(ctx context.Context, path string) error {
	if t.Runner == nil {
		return errors.New("trash requires a command runner")
	}
	goos := t.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	switch goos {
	case "linux":
		if _, err := t.Runner.Run(ctx, "gio", "trash", path); err == nil {
			return nil
		} else if _, fallbackErr := t.Runner.Run(ctx, "trash-put", path); fallbackErr != nil {
			return fmt.Errorf("move %q to trash: %w (trash-put: %v)", path, err, fallbackErr)
		}
		return nil
	case "darwin":
		_, err := t.Runner.Run(ctx, "osascript", "-e", finderDeleteScript(path))
		if err != nil {
			return fmt.Errorf("move %q to Finder trash: %w", path, err)
		}
		return nil
	default:
		return fmt.Errorf("OS trash is not supported on %s", goos)
	}
}

func finderDeleteScript(path string) string {
	quoted := ""
	for _, value := range path {
		if value == '\\' || value == '"' {
			quoted += "\\"
		}
		quoted += string(value)
	}
	return `tell application "Finder" to delete POSIX file "` + quoted + `"`
}

// WorktreeRemover is the narrow Git mutation seam. Implementations must use a
// non-force worktree removal on the registered checkout path.
type WorktreeRemover interface {
	RemoveWorktree(context.Context, string, string) error
}

// Execute performs the selected operation after the caller has completed its
// final full preflight. The identity checks here only reject stale path or
// repository objects; the external Git and trash operations own their final
// refusal semantics.
func Execute(ctx context.Context, plan Plan, force, permanent bool, fs FileSystem, git WorktreeRemover, trash Trash) error {
	if len(plan.Blockers) != 0 {
		return fmt.Errorf("delete blocked: %s", FormatFindings(plan.Blockers))
	}
	if plan.HasWarnings() && !force {
		return fmt.Errorf("delete requires confirmation: %s", FormatFindings(plan.Warnings))
	}
	if fs == nil {
		fs = OSFileSystem{}
	}
	path := filepath.Clean(plan.Path)
	if err := validateAncestors(fs, path); err != nil {
		return fmt.Errorf("validate deletion path: %w", err)
	}
	current, err := Fingerprint(fs, path)
	if err != nil {
		return fmt.Errorf("recheck %q: %w", path, err)
	}
	if plan.Fingerprint != "" && current != plan.Fingerprint {
		return fmt.Errorf("path changed since preflight: %q", path)
	}

	if plan.Action == "worktree" {
		if git == nil || plan.Repository == "" {
			return errors.New("linked worktree removal requires a repository identity and Git adapter")
		}
		if err := validateAncestors(fs, plan.Repository); err != nil {
			return fmt.Errorf("validate Git repository path: %w", err)
		}
		if plan.RepositoryFingerprint != "" {
			repositoryCurrent, repositoryErr := Fingerprint(fs, plan.Repository)
			if repositoryErr != nil {
				return fmt.Errorf("recheck Git repository %q: %w", plan.Repository, repositoryErr)
			}
			if repositoryCurrent != plan.RepositoryFingerprint {
				return fmt.Errorf("git repository changed since preflight: %q", plan.Repository)
			}
		}
		// Do not stage, rename, or recursively remove a worktree. Git must see
		// the actual registered checkout so its non-force refusal remains the
		// final data-safety check.
		return git.RemoveWorktree(ctx, plan.Repository, path)
	}

	if permanent {
		if plan.Symlink {
			return fs.Remove(path)
		}
		return fs.RemoveAll(path)
	}
	if trash == nil {
		return errors.New("delete requires a trash adapter")
	}
	return trash.Trash(ctx, path)
}

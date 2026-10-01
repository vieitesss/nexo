package deletion

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/vieitesss/nexo/internal/runner"
)

// Trash moves one path to the operating system's trash. It must not fall back
// to permanent deletion when the command fails.
type Trash interface {
	Trash(context.Context, string) error
}

// CommandTrash delegates trashing to a platform utility through the shared
// runner seam. gio/trash-put are both trash operations; neither is deletion.
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
	case "darwin":
		_, err := t.Runner.Run(ctx, "osascript", "-e", finderDeleteScript(path))
		if err != nil {
			return fmt.Errorf("move %q to Finder trash: %w", path, err)
		}
		return nil
	case "linux":
		if _, err := t.Runner.Run(ctx, "gio", "trash", path); err == nil {
			return nil
		} else {
			if _, putErr := t.Runner.Run(ctx, "trash-put", path); putErr == nil {
				return nil
			} else {
				return fmt.Errorf("move %q to trash: %w (trash-put: %v)", path, err, putErr)
			}
		}
	default:
		return fmt.Errorf("OS trash is not supported on %s", goos)
	}
}

func finderDeleteScript(path string) string {
	// AppleScript string literals use backslash escaping. The lexical path is
	// passed to Finder as a POSIX file and is never resolved by nexo.
	quoted := ""
	for _, r := range path {
		if r == '\\' || r == '"' {
			quoted += "\\"
		}
		quoted += string(r)
	}
	return `tell application "Finder" to delete POSIX file "` + quoted + `"`
}

// Execute rechecks the lexical path identity and then performs exactly the
// selected action. A failed trash operation is returned unchanged; no
// permanent fallback exists.
func Execute(ctx context.Context, plan Plan, force, permanent bool, fs FileSystem, git interface {
	RemoveWorktree(context.Context, string, string) error
}, trash Trash) error {
	if len(plan.Blockers) > 0 {
		return fmt.Errorf("delete blocked: %s", formatFindings(plan.Blockers))
	}
	if plan.HasWarnings() && !force {
		return fmt.Errorf("delete requires confirmation: %s", formatFindings(plan.Warnings))
	}
	if fs == nil {
		fs = OSFileSystem{}
	}
	current, err := Fingerprint(fs, plan.Path)
	if err != nil {
		return fmt.Errorf("recheck %q: %w", plan.Path, err)
	}
	if plan.Fingerprint != "" && current != plan.Fingerprint {
		return fmt.Errorf("path changed since preflight: %q", plan.Path)
	}
	if plan.Action == "worktree" {
		if git == nil {
			return errors.New("linked worktree removal requires a Git adapter")
		}
		return git.RemoveWorktree(ctx, plan.Repository, plan.Path)
	}

	// Stage ordinary paths through the identity-aware seam. The stage operation
	// is the mutation boundary: all subsequent operations address the moved
	// inode, not a pathname that another process could replace.
	if stager, ok := fs.(IdentityStager); ok {
		staged, stageErr := stager.Stage(plan.Path, plan.Fingerprint)
		if stageErr != nil {
			return stageErr
		}
		if permanent {
			if plan.Symlink {
				return fs.Remove(staged)
			}
			return fs.RemoveAll(staged)
		}
		restore := func() error {
			if restorer, ok := fs.(identityRestorer); ok {
				return restorer.Restore(staged, plan.Path)
			}
			return errors.New("staged path cannot be restored")
		}
		if trash == nil {
			_ = restore()
			return errors.New("delete requires a trash adapter")
		}
		if err := trash.Trash(ctx, staged); err != nil {
			if restoreErr := restore(); restoreErr != nil {
				return errors.Join(err, fmt.Errorf("restore after trash failure: %w", restoreErr))
			}
			return err
		}
		return nil
	}
	if permanent {
		if plan.Symlink {
			return fs.Remove(plan.Path)
		}
		return fs.RemoveAll(plan.Path)
	}
	if trash == nil {
		return errors.New("delete requires a trash adapter")
	}
	return trash.Trash(ctx, plan.Path)
}

func formatFindings(findings []Finding) string {
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, finding.Message)
	}
	return strings.Join(parts, "; ")
}

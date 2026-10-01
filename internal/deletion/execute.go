package deletion

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/vieitesss/nexo/internal/runner"
)

// Trash moves one path to the operating system's trash. It must not fall back
// to permanent deletion when the command fails.
type Trash interface {
	Trash(context.Context, string) error
}

// IdentityTrash receives the original logical path separately from the staged
// path. Adapters that support restoration metadata use it to preserve the
// user's selected name and destination through the trash transaction.
type IdentityTrash interface {
	TrashEntry(context.Context, string, string) error
}

// mutationMu serializes the final identity check and the mutation for this
// process. It prevents two nexo deletion operations from invalidating each
// other's observations at the last boundary; adapters still perform their own
// no-follow checks before invoking external commands.
var mutationMu sync.Mutex

// CommandTrash delegates trashing to a platform utility through the shared
// runner seam. gio/trash-put are both trash operations; neither is deletion.
type CommandTrash struct {
	Runner runner.CommandRunner
	GOOS   string
}

func (t CommandTrash) Trash(ctx context.Context, path string) error {
	return t.trashPath(ctx, path, "")
}

// TrashEntry is the identity-preserving form used after a deletion stage. The
// staged object keeps the original basename, while adapters receive the
// original pathname so they can retain restoration metadata where supported.
func (t CommandTrash) TrashEntry(ctx context.Context, staged, original string) error {
	if original == "" {
		return errors.New("trash requires the original path")
	}
	return t.trashPath(ctx, staged, original)
}

func (t CommandTrash) trashPath(ctx context.Context, path, original string) error {
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
			return rewriteLinuxTrashMetadata(path, original)
		} else {
			if _, putErr := t.Runner.Run(ctx, "trash-put", path); putErr == nil {
				return rewriteLinuxTrashMetadata(path, original)
			} else {
				return fmt.Errorf("move %q to trash: %w (trash-put: %v)", path, err, putErr)
			}
		}
	default:
		return fmt.Errorf("OS trash is not supported on %s", goos)
	}
}

func rewriteLinuxTrashMetadata(staged, original string) error {
	if original == "" {
		return nil
	}
	staged, err := filepath.Abs(staged)
	if err != nil {
		return fmt.Errorf("resolve staged trash path: %w", err)
	}
	original, err = filepath.Abs(original)
	if err != nil {
		return fmt.Errorf("resolve original trash path: %w", err)
	}
	roots := []string{}
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" {
		roots = append(roots, filepath.Join(dataHome, "Trash"))
	}
	if home := os.Getenv("HOME"); home != "" {
		roots = append(roots, filepath.Join(home, ".local", "share", "Trash"))
	}
	for _, root := range roots {
		infoDir := filepath.Join(root, "info")
		entries, readErr := os.ReadDir(infoDir)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("inspect trash metadata: %w", readErr)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".trashinfo") {
				continue
			}
			metadataPath := filepath.Join(infoDir, entry.Name())
			contents, readErr := os.ReadFile(metadataPath)
			if readErr != nil {
				return fmt.Errorf("read trash metadata %q: %w", metadataPath, readErr)
			}
			value, ok := trashMetadataPath(contents)
			if !ok || value != staged {
				continue
			}
			rewritten := rewriteTrashPath(contents, escapeTrashPath(original))
			if writeErr := os.WriteFile(metadataPath, rewritten, 0o600); writeErr != nil {
				return fmt.Errorf("preserve original trash path %q: %w", original, writeErr)
			}
			return nil
		}
	}
	return nil
}

func trashMetadataPath(contents []byte) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(string(contents)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "Path=") {
			continue
		}
		value, err := url.PathUnescape(strings.TrimPrefix(line, "Path="))
		return value, err == nil
	}
	return "", false
}

func rewriteTrashPath(contents []byte, escaped string) []byte {
	lines := strings.Split(string(contents), "\n")
	for index, line := range lines {
		if strings.HasPrefix(line, "Path=") {
			lines[index] = "Path=" + escaped
			break
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func escapeTrashPath(path string) string {
	return (&url.URL{Path: path}).EscapedPath()
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
		return fmt.Errorf("delete blocked: %s", FormatFindings(plan.Blockers))
	}
	if plan.HasWarnings() && !force {
		return fmt.Errorf("delete requires confirmation: %s", FormatFindings(plan.Warnings))
	}
	if fs == nil {
		fs = OSFileSystem{}
	}
	mutationMu.Lock()
	defer mutationMu.Unlock()
	if err := validateAncestors(fs, filepath.Clean(plan.Path)); err != nil {
		return fmt.Errorf("validate deletion path: %w", err)
	}
	current, err := Fingerprint(fs, plan.Path)
	if err != nil {
		return fmt.Errorf("recheck %q: %w", plan.Path, err)
	}
	if plan.Fingerprint != "" && current != plan.Fingerprint {
		return fmt.Errorf("path changed since preflight: %q", plan.Path)
	}
	if plan.Action == "worktree" {
		if plan.Repository == "" {
			return errors.New("linked worktree removal requires a repository identity")
		}
		if err := validateAncestors(fs, filepath.Clean(plan.Repository)); err != nil {
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
		if git == nil {
			return errors.New("linked worktree removal requires a Git adapter")
		}
		// Stage the registered worktree itself before invoking Git. Git removes
		// the registration for the original path even when its directory has
		// moved, while the staged inode remains bound to this transaction. A
		// pathname replacement can therefore never become Git's deletion
		// operand. On refusal, restore the staged inode without overwriting a
		// replacement.
		if stager, ok := fs.(IdentityStager); ok {
			staged, stageErr := stager.Stage(plan.Path, plan.Fingerprint)
			if stageErr != nil {
				if staged == "" {
					return stageErr
				}
				if restorer, restoreOK := fs.(identityRestorer); restoreOK {
					if restoreErr := restorer.Restore(staged, plan.Path); restoreErr != nil {
						return errors.Join(stageErr, fmt.Errorf("staged worktree %q remains recoverable; restore failed: %w", staged, restoreErr))
					}
					return stageErr
				}
				return fmt.Errorf("%w (staged worktree %q remains recoverable; restoration is unavailable)", stageErr, staged)
			}
			if verifier, verifyOK := git.(interface {
				VerifyWorktreeRemoval(context.Context, string) error
			}); verifyOK {
				if verifyErr := verifier.VerifyWorktreeRemoval(ctx, staged); verifyErr != nil {
					if restorer, restoreOK := fs.(identityRestorer); restoreOK {
						if restoreErr := restorer.Restore(staged, plan.Path); restoreErr != nil {
							return errors.Join(verifyErr, fmt.Errorf("restore unremovable worktree %q: %w", staged, restoreErr))
						}
						return verifyErr
					}
					return fmt.Errorf("%w (staged worktree %q remains recoverable; restoration is unavailable)", verifyErr, staged)
				}
			}
			removeErr := git.RemoveWorktree(ctx, plan.Repository, plan.Path)
			if removeErr != nil {
				if restorer, restoreOK := fs.(identityRestorer); restoreOK {
					if restoreErr := restorer.Restore(staged, plan.Path); restoreErr != nil {
						return errors.Join(removeErr, fmt.Errorf("restore refused worktree %q: %w", staged, restoreErr))
					}
					return removeErr
				}
				return fmt.Errorf("%w (staged worktree %q remains recoverable; restoration is unavailable)", removeErr, staged)
			}
			if err := fs.RemoveAll(staged); err != nil {
				return fmt.Errorf("remove staged worktree %q: %w", staged, err)
			}
			return cleanupStaged(fs, staged)
		}
		if guarded, ok := git.(interface {
			RemoveWorktreeChecked(context.Context, string, string, FileSystem, string) error
		}); ok {
			return guarded.RemoveWorktreeChecked(ctx, plan.Repository, plan.Path, fs, plan.Fingerprint)
		}
		return git.RemoveWorktree(ctx, plan.Repository, plan.Path)
	}

	// Stage ordinary paths through the identity-aware seam. The stage operation
	// is the mutation boundary: all subsequent operations address the moved
	// inode, not a pathname that another process could replace.
	if stager, ok := fs.(IdentityStager); ok {
		staged, stageErr := stager.Stage(plan.Path, plan.Fingerprint)
		if stageErr != nil {
			if staged == "" {
				return stageErr
			}
			if restorer, restoreOK := fs.(identityRestorer); restoreOK {
				if restoreErr := restorer.Restore(staged, plan.Path); restoreErr != nil {
					return errors.Join(stageErr, fmt.Errorf("staged path %q remains recoverable; restore failed: %w", staged, restoreErr))
				}
				return stageErr
			}
			return fmt.Errorf("%w (staged path %q remains recoverable; restoration is unavailable)", stageErr, staged)
		}
		if permanent {
			var removeErr error
			if plan.Symlink {
				removeErr = fs.Remove(staged)
			} else {
				removeErr = fs.RemoveAll(staged)
			}
			if removeErr == nil {
				removeErr = cleanupStaged(fs, staged)
			}
			return removeErr
		}
		restore := func() error {
			if restorer, ok := fs.(identityRestorer); ok {
				return restorer.Restore(staged, plan.Path)
			}
			return errors.New("staged path cannot be restored")
		}
		if trash == nil {
			if restoreErr := restore(); restoreErr != nil {
				return fmt.Errorf("delete requires a trash adapter; staged path %q could not be restored: %w", staged, restoreErr)
			}
			return errors.New("delete requires a trash adapter")
		}
		var trashErr error
		if identityTrash, ok := trash.(IdentityTrash); ok {
			trashErr = identityTrash.TrashEntry(ctx, staged, plan.Path)
		} else {
			trashErr = trash.Trash(ctx, staged)
		}
		if trashErr != nil {
			if restoreErr := restore(); restoreErr != nil {
				return errors.Join(trashErr, fmt.Errorf("restore staged path %q after trash failure: %w", staged, restoreErr))
			}
			return trashErr
		}
		if cleanupErr := cleanupStaged(fs, staged); cleanupErr != nil {
			return cleanupErr
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

func cleanupStaged(fs FileSystem, staged string) error {
	cleanup, ok := fs.(interface{ CleanupStaged(string) error })
	if !ok {
		return nil
	}
	return cleanup.CleanupStaged(staged)
}

// FormatFindings renders findings consistently for command and execution
// errors. It intentionally keeps the complete messages so a refusal remains
// actionable without reopening a plan.
func FormatFindings(findings []Finding) string {
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, finding.Message)
	}
	return strings.Join(parts, "; ")
}

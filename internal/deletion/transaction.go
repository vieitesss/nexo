package deletion

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/destination"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/runner"
)

// Options supplies the configuration snapshot and backend seams used by one
// deletion operation. The transaction owns discovery, safety checks, and the
// final mutation so the CLI and TUI cannot drift apart.
type Options struct {
	Projects      config.ProjectsConfig
	Home          string
	Runner        runner.CommandRunner
	ActiveChecker capabilities.ProjectUseChecker
}

// Transaction is the shared project-deletion operation used by the CLI and
// the Projects tab.
type Transaction struct {
	options Options
}

// New creates a deletion transaction from one invocation snapshot.
func New(options Options) Transaction {
	if options.Home == "" {
		options.Home = os.Getenv("HOME")
	}
	return Transaction{options: options}
}

// Preflight gathers the complete read-only deletion plan. selectionIdentity is
// captured by the TUI before it opens confirmation; a replacement at the same
// pathname becomes a hard blocker even when force is requested.
func (t Transaction) Preflight(ctx context.Context, path, selectionIdentity string) (Plan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path = lexicalAbs(path)
	discovery := projects.Discover(t.options.Projects, t.options.Home)
	candidate := false
	for _, item := range discovery.Candidates {
		if lexicalAbs(item.Path) == path {
			candidate = true
			break
		}
	}

	observation, err := InspectFilesystem(OSFileSystem{}, path)
	if err != nil {
		return Plan{}, err
	}
	observation.Candidate = candidate
	observation.Configured = configuredPath(path, t.options.Projects.Paths, t.options.Home) || (t.options.Projects.GitFromHome && under(path, t.options.Home))
	observation.Protected, observation.ProtectedReason = protectedPath(path, observation.Symlink, t.options.Projects.Paths, t.options.Home)

	currentIdentity := observation.Fingerprint
	if selectionIdentity != "" {
		observation.Fingerprint = selectionIdentity
		observation.CurrentFingerprint = currentIdentity
		if currentIdentity == "" || currentIdentity != selectionIdentity {
			observation.PathChanged = true
		}
	}
	if observation.Directory && !observation.Symlink {
		if t.options.Runner == nil {
			if _, statErr := os.Lstat(filepath.Join(path, ".git")); statErr == nil {
				observation.GitError = errors.New("git check requires a command runner")
			}
		} else {
			state, gitErr := (GitInspector{Runner: t.options.Runner}).Inspect(ctx, path)
			observation.Git, observation.GitError = state, gitErr
			if gitErr == nil && state.LinkedWorktree && state.PrimaryPath != "" {
				observation.RepositoryFingerprint, observation.GitError = Fingerprint(OSFileSystem{}, state.PrimaryPath)
			}
		}
	}
	if t.options.ActiveChecker == nil {
		observation.ActiveCheckError = errors.New("selected backend cannot verify active project use")
	} else {
		observation.ActiveContainer, observation.ActiveCheckError = t.options.ActiveChecker.ProjectActive(ctx, path)
	}
	return Preflight(observation), nil
}

// Execute rechecks the displayed plan and performs deletion only when every
// safety fact is unchanged. The low-level Execute function owns the final
// operation-specific refusal checks and mutation seam.
func (t Transaction) Execute(ctx context.Context, plan Plan, force, permanent bool) error {
	fresh, err := t.Preflight(ctx, plan.Path, plan.Fingerprint)
	if err != nil {
		return fmt.Errorf("recheck %q before deletion: %w", plan.Path, err)
	}
	if !samePlan(plan, fresh) {
		return errors.New("safety facts changed after confirmation; deletion was refused; reopen the preflight")
	}
	return Execute(ctx, fresh, force, permanent, OSFileSystem{}, GitInspector{Runner: t.options.Runner}, CommandTrash{Runner: t.options.Runner})
}

func samePlan(confirmed, fresh Plan) bool {
	if confirmed.Path != fresh.Path || confirmed.Action != fresh.Action || confirmed.Symlink != fresh.Symlink || confirmed.Repository != fresh.Repository || confirmed.Fingerprint != fresh.Fingerprint || confirmed.RepositoryFingerprint != fresh.RepositoryFingerprint || confirmed.CanProceed != fresh.CanProceed {
		return false
	}
	return sameFindings(confirmed.Warnings, fresh.Warnings) && sameFindings(confirmed.Blockers, fresh.Blockers)
}

func sameFindings(left, right []Finding) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func configuredPath(path string, patterns []string, home string) bool {
	for _, pattern := range patterns {
		pattern = destination.ExpandHome(pattern, home)
		wildcard := strings.HasSuffix(pattern, string(filepath.Separator)+"*")
		if wildcard {
			pattern = strings.TrimSuffix(pattern, string(filepath.Separator)+"*")
		}
		if under(path, pattern) {
			return true
		}
	}
	return false
}

// protectedPath excludes infrastructure itself, not ordinary children beneath
// a configured path. Physical comparisons are used only for non-symlink
// deletion targets, so a link is never followed to decide what is removed.
func protectedPath(path string, targetSymlink bool, patterns []string, home string) (bool, string) {
	path = lexicalAbs(path)
	if path == filepath.Clean(string(filepath.Separator)) {
		return true, "filesystem root"
	}
	if home != "" && path == lexicalAbs(home) {
		return true, "home directory"
	}
	parents := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		parent := destination.ExpandHome(pattern, home)
		if strings.HasSuffix(parent, string(filepath.Separator)+"*") {
			parent = strings.TrimSuffix(parent, string(filepath.Separator)+"*")
		}
		parent = lexicalAbs(parent)
		if parent == "" {
			continue
		}
		parents = append(parents, parent)
		if path == parent {
			return true, "configured project parent"
		}
	}
	if targetSymlink {
		return false, ""
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, ""
	}
	if home != "" {
		if resolvedHome, homeErr := filepath.EvalSymlinks(lexicalAbs(home)); homeErr == nil && lexicalAbs(resolvedPath) == lexicalAbs(resolvedHome) {
			return true, "home directory"
		}
	}
	for _, parent := range parents {
		resolvedParent, parentErr := filepath.EvalSymlinks(parent)
		if parentErr == nil && lexicalAbs(resolvedPath) == lexicalAbs(resolvedParent) {
			return true, "configured project parent"
		}
	}
	return false, ""
}

func under(path, parent string) bool {
	if parent == "" {
		return false
	}
	path, parent = lexicalAbs(path), lexicalAbs(parent)
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

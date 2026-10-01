package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/deletion"
	"github.com/vieitesss/nexo/internal/destination"
	"github.com/vieitesss/nexo/internal/projects"
)

type deleteCommand struct{}
type deleteArgs struct {
	dryRun, permanent, force bool
	path                     string
}

func init()                        { registerBuiltin(func() Command { return deleteCommand{} }) }
func (deleteCommand) Name() string { return "delete" }

func (deleteCommand) Run(ctx context.Context, invocation Invocation) error {
	parsed, err := parseDeleteArgs(invocation.Args)
	if err != nil {
		return UsageError{Err: err}
	}
	path := lexicalAbs(destination.ExpandHome(parsed.path, os.Getenv("HOME")))
	plan, err := preflightDelete(ctx, invocation, path, invocation.DeleteIdentity)
	if err != nil {
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	if invocation.Stdout != nil {
		if err := writeDeletePlan(invocation.Stdout, plan, invocation.JSON); err != nil {
			return err
		}
	}
	if parsed.dryRun {
		if len(plan.Blockers) > 0 {
			return fmt.Errorf("delete blocked: %s", findingText(plan.Blockers))
		}
		return nil
	}
	if len(plan.Blockers) > 0 {
		return fmt.Errorf("delete blocked: %s", findingText(plan.Blockers))
	}
	if plan.HasWarnings() && !parsed.force {
		return fmt.Errorf("delete requires --force: %s", findingText(plan.Warnings))
	}
	if err := executeDelete(ctx, invocation, plan, parsed.force, parsed.permanent); err != nil {
		return err
	}
	if invocation.Stdout != nil && !invocation.JSON {
		_, _ = fmt.Fprintf(invocation.Stdout, "deleted %s\n", path)
	}
	return nil
}

// preflightDelete is shared by the CLI and the TUI. selectionIdentity is the
// identity captured when the user selected/opened the row; a later replacement
// is a hard blocker even when force is requested.
func preflightDelete(ctx context.Context, invocation Invocation, path, selectionIdentity string) (deletion.Plan, error) {
	discovery := projects.Discover(invocation.Config.ProjectsConfig, os.Getenv("HOME"))
	candidate := false
	for _, item := range discovery.Candidates {
		if lexicalAbs(item.Path) == path {
			candidate = true
			break
		}
	}
	observation, err := deletion.InspectFilesystem(deletion.OSFileSystem{}, path)
	if err != nil {
		return deletion.Plan{}, err
	}
	observation.Candidate = candidate
	observation.Configured = configuredPath(path, invocation.Config.Paths, os.Getenv("HOME")) || (invocation.Config.GitFromHome && under(path, os.Getenv("HOME")))
	observation.Protected, observation.ProtectedReason = protectedPath(path, observation.Symlink, invocation.Config.Paths, os.Getenv("HOME"))

	currentIdentity := observation.Fingerprint
	if selectionIdentity != "" {
		observation.Fingerprint = selectionIdentity
		observation.CurrentFingerprint = currentIdentity
		if currentIdentity == "" || currentIdentity != selectionIdentity {
			observation.PathChanged = true
		}
	}
	if observation.Directory && !observation.Symlink {
		if invocation.Runner == nil {
			if _, statErr := os.Lstat(filepath.Join(path, ".git")); statErr == nil {
				observation.GitError = errors.New("git check requires a command runner")
			}
		} else {
			state, gitErr := (deletion.GitInspector{Runner: invocation.Runner}).Inspect(ctx, path)
			observation.Git, observation.GitError = state, gitErr
		}
	}
	checker, ok := invocation.Adapter.(capabilities.ProjectUseChecker)
	if !ok {
		observation.ActiveCheckError = errors.New("selected backend cannot verify active project use")
	} else {
		observation.ActiveContainer, observation.ActiveCheckError = checker.ProjectActive(ctx, path)
	}
	return deletion.Preflight(observation), nil
}

func executeDelete(ctx context.Context, invocation Invocation, plan deletion.Plan, force, permanent bool) error {
	trash := deletion.CommandTrash{Runner: invocation.Runner}
	git := deletion.GitInspector{Runner: invocation.Runner}
	return deletion.Execute(ctx, plan, force, permanent, deletion.OSFileSystem{}, git, trash)
}

func parseDeleteArgs(args []string) (deleteArgs, error) {
	var parsed deleteArgs
	for _, arg := range args {
		switch arg {
		case "--dry-run":
			if parsed.dryRun {
				return deleteArgs{}, errors.New("--dry-run may be provided only once")
			}
			parsed.dryRun = true
		case "--permanent":
			if parsed.permanent {
				return deleteArgs{}, errors.New("--permanent may be provided only once")
			}
			parsed.permanent = true
		case "--force":
			if parsed.force {
				return deleteArgs{}, errors.New("--force may be provided only once")
			}
			parsed.force = true
		case "":
			return deleteArgs{}, errors.New("delete path may not be empty")
		default:
			if strings.HasPrefix(arg, "-") {
				return deleteArgs{}, fmt.Errorf("unknown delete option %q", arg)
			}
			if parsed.path != "" {
				return deleteArgs{}, errors.New("delete accepts exactly one PATH")
			}
			parsed.path = arg
		}
	}
	if parsed.path == "" {
		return deleteArgs{}, errors.New("delete requires PATH")
	}
	return parsed, nil
}

func writeDeletePlan(out interface{ Write([]byte) (int, error) }, plan deletion.Plan, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(out).Encode(plan)
	}
	if _, err := fmt.Fprintf(out, "preflight %s (%s)\n", plan.Path, plan.Action); err != nil {
		return err
	}
	for _, finding := range plan.Warnings {
		if _, err := fmt.Fprintf(out, "warning: %s\n", finding.Message); err != nil {
			return err
		}
	}
	for _, finding := range plan.Blockers {
		if _, err := fmt.Fprintf(out, "blocker: %s\n", finding.Message); err != nil {
			return err
		}
	}
	return nil
}

func findingText(findings []deletion.Finding) string {
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, finding.Message)
	}
	return strings.Join(parts, "; ")
}

func lexicalAbs(path string) string {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
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

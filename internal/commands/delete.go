package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	path := lexicalAbs(parsed.path)
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
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	observation.Candidate = candidate
	observation.Configured = configuredPath(path, invocation.Config.Paths, os.Getenv("HOME")) || (invocation.Config.GitFromHome && under(path, os.Getenv("HOME")))
	if !observation.Symlink && observation.Exists {
		if invocation.Runner == nil {
			if _, statErr := os.Lstat(filepath.Join(path, ".git")); statErr == nil {
				observation.GitError = errors.New("git check requires a command runner")
			}
		} else {
			state, gitErr := (deletion.GitInspector{Runner: invocation.Runner}).Inspect(ctx, path)
			observation.Git, observation.GitError = state, gitErr
		}
	}
	if checker, ok := invocation.Adapter.(interface {
		ProjectActive(context.Context, string) (bool, error)
	}); ok {
		observation.ActiveContainer, observation.ActiveCheckError = checker.ProjectActive(ctx, path)
	}
	plan := deletion.Preflight(observation)
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
	trash := deletion.CommandTrash{Runner: invocation.Runner}
	git := deletion.GitInspector{Runner: invocation.Runner}
	if err := deletion.Execute(ctx, plan, parsed.force, parsed.permanent, deletion.OSFileSystem{}, git, trash); err != nil {
		return err
	}
	if invocation.Stdout != nil && !invocation.JSON {
		_, _ = fmt.Fprintf(invocation.Stdout, "deleted %s\n", path)
	}
	return nil
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
		pattern = destination.ExpandHome(strings.TrimSpace(pattern), home)
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

func under(path, parent string) bool {
	if strings.TrimSpace(parent) == "" {
		return false
	}
	path, parent = lexicalAbs(path), lexicalAbs(parent)
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

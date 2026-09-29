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
	"github.com/vieitesss/nexo/internal/config"
)

type worktreeCommand struct{}

type worktreeCreateArgs struct {
	Repo        string
	Branch      string
	Destination string
	NewBranch   bool
	AddParent   bool
}

func init() {
	registerBuiltin(func() Command { return worktreeCommand{} })
}

func (worktreeCommand) Name() string { return "worktree" }

func (worktreeCommand) Run(ctx context.Context, invocation Invocation) error {
	parsed, err := parseWorktreeCreateArgs(invocation.Args)
	if err != nil {
		return err
	}
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	creator, ok := invocation.Adapter.(capabilities.WorktreeCreator)
	if !ok {
		return fmt.Errorf("backend %q does not support creating worktrees", invocation.Selection.Kind)
	}

	var restore func() error
	if parsed.AddParent {
		configPath := invocation.ConfigPath
		if configPath == "" {
			configPath = config.DefaultPath()
		}
		parent := filepath.Dir(parsed.Destination)
		if absolute, err := filepath.Abs(parent); err == nil {
			parent = absolute
		}
		var changed bool
		restore, changed, err = config.AddProjectPath(configPath, parent, os.Getenv("HOME"))
		if err != nil {
			return err
		}
		if !changed {
			restore = nil
		}
	}

	result, err := creator.CreateWorktree(ctx, capabilities.WorktreeCreateOptions{
		Repo:        parsed.Repo,
		Branch:      parsed.Branch,
		Destination: parsed.Destination,
		NewBranch:   parsed.NewBranch,
	})
	if err != nil {
		if restore != nil {
			if restoreErr := restore(); restoreErr != nil {
				return errors.Join(err, fmt.Errorf("restore config after worktree failure: %w", restoreErr))
			}
		}
		return err
	}

	if result.Path == "" {
		result.Path = parsed.Destination
	}
	if result.Branch == "" {
		result.Branch = parsed.Branch
	}
	if invocation.JSON {
		if invocation.Stdout == nil {
			return errors.New("worktree create requires stdout for JSON output")
		}
		return json.NewEncoder(invocation.Stdout).Encode(result)
	}
	if invocation.Stdout == nil {
		return nil
	}
	_, err = fmt.Fprintf(invocation.Stdout, "created worktree %s on %s\n", result.Path, result.Branch)
	return err
}

func parseWorktreeCreateArgs(args []string) (worktreeCreateArgs, error) {
	var parsed worktreeCreateArgs
	positionals := make([]string, 0, 3)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--new-branch":
			if parsed.NewBranch {
				return worktreeCreateArgs{}, errors.New("--new-branch may be provided only once")
			}
			parsed.NewBranch = true
		case "--add-parent":
			if parsed.AddParent {
				return worktreeCreateArgs{}, errors.New("--add-parent may be provided only once")
			}
			parsed.AddParent = true
		case "":
			return worktreeCreateArgs{}, errors.New("worktree create arguments may not be empty")
		default:
			if strings.HasPrefix(args[i], "-") {
				return worktreeCreateArgs{}, fmt.Errorf("unknown worktree option %q", args[i])
			}
			positionals = append(positionals, args[i])
		}
	}
	if len(positionals) != 3 {
		return worktreeCreateArgs{}, fmt.Errorf("worktree create requires <repo> <branch> <destination>, got %d arguments", len(positionals))
	}
	parsed.Repo, parsed.Branch, parsed.Destination = positionals[0], positionals[1], positionals[2]
	return parsed, nil
}

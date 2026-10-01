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
	NoFocus     bool
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
	if invocation.Stdout == nil {
		return errors.New("worktree create requires stdout")
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
		NoFocus:     parsed.NoFocus,
	})
	if err != nil {
		if restore != nil {
			if restoreErr := restore(); restoreErr != nil {
				return errors.Join(err, fmt.Errorf("restore config after worktree failure: %w", restoreErr))
			}
		}
		return err
	}

	var outputErr error
	if invocation.JSON {
		outputErr = json.NewEncoder(invocation.Stdout).Encode(worktreeJSON{
			Path:      result.Path,
			Branch:    result.Branch,
			Container: newOpenedContainerJSON(string(invocation.Selection.Kind), result.Container),
		})
	} else {
		_, outputErr = fmt.Fprintf(invocation.Stdout, "created worktree %s on %s\n", result.Path, result.Branch)
	}
	if outputErr == nil {
		return nil
	}

	var rollbackErr error
	if remover, ok := invocation.Adapter.(capabilities.WorktreeRemover); ok {
		rollbackErr = remover.RemoveWorktree(ctx, result)
	} else {
		rollbackErr = errors.New("backend cannot roll back a created worktree")
	}
	if restore != nil {
		if configErr := restore(); configErr != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore config after output failure: %w", configErr))
		}
	}
	if rollbackErr != nil {
		return errors.Join(outputErr, rollbackErr)
	}
	return outputErr
}

// worktreeJSON is the documented worktree result plus the container the
// creator opened for it.
type worktreeJSON struct {
	Path      string              `json:"path"`
	Branch    string              `json:"branch"`
	Container openedContainerJSON `json:"container"`
}

func parseWorktreeCreateArgs(args []string) (worktreeCreateArgs, error) {
	if len(args) == 0 || args[0] != "create" {
		return worktreeCreateArgs{}, errors.New("worktree requires the create subcommand")
	}
	args = args[1:]
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
		case "--no-focus":
			if parsed.NoFocus {
				return worktreeCreateArgs{}, errors.New("--no-focus may be provided only once")
			}
			parsed.NoFocus = true
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

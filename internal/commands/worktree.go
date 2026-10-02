package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	worktreeflow "github.com/vieitesss/nexo/internal/worktree"
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

type worktreeExtractArgs struct {
	Repo         string
	Destination  string
	ReturnBranch string
	AddParent    bool
	NoFocus      bool
}

func init() {
	registerBuiltin(func() Command { return worktreeCommand{} })
}

func (worktreeCommand) Name() string { return "worktree" }

func (worktreeCommand) Run(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) == 0 {
		return errors.New("worktree requires the create or extract subcommand")
	}
	switch invocation.Args[0] {
	case "create":
		return runWorktreeCreate(ctx, invocation)
	case "extract":
		return runWorktreeExtract(ctx, invocation)
	default:
		return fmt.Errorf("unknown worktree subcommand %q", invocation.Args[0])
	}
}

func runWorktreeCreate(ctx context.Context, invocation Invocation) error {
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

	remover, _ := invocation.Adapter.(capabilities.WorktreeRemover)
	operation, err := worktreeflow.Create(ctx, worktreeflow.Options{
		Creator:    creator,
		Remover:    remover,
		ConfigPath: invocation.ConfigPath,
		Paths:      invocation.Config.Paths,
		Home:       os.Getenv("HOME"),
	}, worktreeflow.Request{
		Repo:        parsed.Repo,
		Branch:      parsed.Branch,
		Destination: parsed.Destination,
		NewBranch:   parsed.NewBranch,
		AddParent:   parsed.AddParent,
		NoFocus:     parsed.NoFocus,
	})
	if err != nil {
		return err
	}
	return writeWorktreeResult(ctx, invocation, operation, "created")
}

func runWorktreeExtract(ctx context.Context, invocation Invocation) error {
	parsed, err := parseWorktreeExtractArgs(invocation.Args)
	if err != nil {
		return err
	}
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	extractor, ok := invocation.Adapter.(capabilities.WorktreeExtractor)
	if !ok {
		return fmt.Errorf("backend %q does not support extracting worktrees", invocation.Selection.Kind)
	}
	if invocation.Stdout == nil {
		return errors.New("worktree extract requires stdout")
	}

	rollbacker, _ := invocation.Adapter.(capabilities.WorktreeExtractionRollback)
	operation, err := worktreeflow.Extract(ctx, worktreeflow.Options{
		Extractor:          extractor,
		ExtractionRollback: rollbacker,
		ConfigPath:         invocation.ConfigPath,
		Paths:              invocation.Config.Paths,
		Home:               os.Getenv("HOME"),
	}, worktreeflow.Request{
		Repo:         parsed.Repo,
		Destination:  parsed.Destination,
		ReturnBranch: parsed.ReturnBranch,
		AddParent:    parsed.AddParent,
		NoFocus:      parsed.NoFocus,
	})
	if err != nil {
		return err
	}
	return writeWorktreeResult(ctx, invocation, operation, "extracted")
}

func writeWorktreeResult(ctx context.Context, invocation Invocation, operation worktreeflow.Result, action string) error {
	result := operation.Worktree
	var outputErr error
	if invocation.JSON {
		outputErr = json.NewEncoder(invocation.Stdout).Encode(worktreeJSON{
			Path:         result.Path,
			Branch:       result.Branch,
			ReturnBranch: result.ReturnBranch,
			Container:    newOpenedContainerJSON(string(invocation.Selection.Kind), result.Container),
		})
	} else {
		_, outputErr = fmt.Fprintf(invocation.Stdout, "%s worktree %s on %s\n", action, result.Path, result.Branch)
	}
	if outputErr == nil {
		return nil
	}
	if rollbackErr := operation.Rollback(ctx); rollbackErr != nil {
		return errors.Join(outputErr, rollbackErr)
	}
	return outputErr
}

// worktreeJSON is the documented worktree result plus the container the
// creator opened for it. ReturnBranch is set only for extraction.
type worktreeJSON struct {
	Path         string              `json:"path"`
	Branch       string              `json:"branch"`
	ReturnBranch string              `json:"return_branch,omitempty"`
	Container    openedContainerJSON `json:"container"`
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

func parseWorktreeExtractArgs(args []string) (worktreeExtractArgs, error) {
	if len(args) == 0 || args[0] != "extract" {
		return worktreeExtractArgs{}, errors.New("worktree requires the extract subcommand")
	}
	args = args[1:]
	var parsed worktreeExtractArgs
	positionals := make([]string, 0, 2)
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--return-branch":
			if parsed.ReturnBranch != "" {
				return worktreeExtractArgs{}, errors.New("--return-branch may be provided only once")
			}
			if i+1 >= len(args) || args[i+1] == "" || strings.HasPrefix(args[i+1], "-") {
				return worktreeExtractArgs{}, errors.New("--return-branch requires a value")
			}
			i++
			parsed.ReturnBranch = args[i]
		case strings.HasPrefix(args[i], "--return-branch="):
			if parsed.ReturnBranch != "" {
				return worktreeExtractArgs{}, errors.New("--return-branch may be provided only once")
			}
			value := strings.TrimPrefix(args[i], "--return-branch=")
			if value == "" {
				return worktreeExtractArgs{}, errors.New("--return-branch requires a value")
			}
			parsed.ReturnBranch = value
		case args[i] == "--add-parent":
			if parsed.AddParent {
				return worktreeExtractArgs{}, errors.New("--add-parent may be provided only once")
			}
			parsed.AddParent = true
		case args[i] == "--no-focus":
			if parsed.NoFocus {
				return worktreeExtractArgs{}, errors.New("--no-focus may be provided only once")
			}
			parsed.NoFocus = true
		case args[i] == "":
			return worktreeExtractArgs{}, errors.New("worktree extract arguments may not be empty")
		case strings.HasPrefix(args[i], "-"):
			return worktreeExtractArgs{}, fmt.Errorf("unknown worktree option %q", args[i])
		default:
			positionals = append(positionals, args[i])
		}
	}
	if len(positionals) != 2 {
		return worktreeExtractArgs{}, fmt.Errorf("worktree extract requires <repo> <destination>, got %d arguments", len(positionals))
	}
	parsed.Repo, parsed.Destination = positionals[0], positionals[1]
	return parsed, nil
}

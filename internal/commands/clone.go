package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/destination"
	gitops "github.com/vieitesss/nexo/internal/git"
)

type cloneCommand struct{}

type cloneArgs struct {
	Source      string
	Destination string
	AddParent   bool
}

type cloneJSON struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func init() { registerBuiltin(func() Command { return cloneCommand{} }) }

func (cloneCommand) Name() string { return "clone" }

func (cloneCommand) Run(ctx context.Context, invocation Invocation) error {
	parsed, err := parseCloneArgs(invocation.Args)
	if err != nil {
		return err
	}
	if invocation.Runner == nil {
		return errors.New("clone requires a command runner")
	}

	resolved := parsed.Destination
	var restore func() error
	if parsed.Destination != "" {
		result, resolveErr := destination.New(destination.Options{
			Paths: invocation.Config.Paths,
			Home:  os.Getenv("HOME"),
		}).Resolve(parsed.Destination)
		if resolveErr != nil {
			return resolveErr
		}
		resolved = result.Path
		if parsed.AddParent && result.AddParent {
			restore, err = addParentToConfig(invocation, result.Path)
			if err != nil {
				return err
			}
		}
	} else if parsed.AddParent {
		return UsageError{Err: errors.New("clone --add-parent requires a destination")}
	}

	if err := gitops.Clone(ctx, invocation.Runner, parsed.Source, resolved); err != nil {
		return restoreConfigOnFailure(err, restore, "clone")
	}

	if invocation.Stdout == nil {
		return nil
	}
	result := cloneJSON{Source: parsed.Source, Destination: resolved}
	if invocation.JSON {
		if err := json.NewEncoder(invocation.Stdout).Encode(result); err != nil {
			if restore != nil {
				if restoreErr := restore(); restoreErr != nil {
					return errors.Join(err, fmt.Errorf("restore config after clone output: %w", restoreErr))
				}
			}
			return err
		}
		return nil
	}
	if resolved == "" {
		_, err = fmt.Fprintf(invocation.Stdout, "cloned %s\n", parsed.Source)
	} else {
		_, err = fmt.Fprintf(invocation.Stdout, "cloned %s to %s\n", parsed.Source, resolved)
	}
	return err
}

func parseCloneArgs(args []string) (cloneArgs, error) {
	var parsed cloneArgs
	positionals := make([]string, 0, 2)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--add-parent":
			if parsed.AddParent {
				return cloneArgs{}, errors.New("--add-parent may be provided only once")
			}
			parsed.AddParent = true
		case "":
			return cloneArgs{}, errors.New("clone arguments may not be empty")
		default:
			if strings.HasPrefix(arg, "-") {
				return cloneArgs{}, fmt.Errorf("unknown clone option %q", arg)
			}
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) == 0 {
		return cloneArgs{}, errors.New("clone requires a source")
	}
	if len(positionals) > 2 {
		return cloneArgs{}, fmt.Errorf("clone accepts SOURCE [DESTINATION], got %d arguments", len(positionals))
	}
	parsed.Source = positionals[0]
	if len(positionals) == 2 {
		parsed.Destination = positionals[1]
	}
	return parsed, nil
}

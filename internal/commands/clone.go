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

	home := os.Getenv("HOME")
	requested := parsed.Destination
	if requested == "" {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return fmt.Errorf("get working directory: %w", cwdErr)
		}
		requested, err = destination.DefaultPath(parsed.Source, cwd, home)
		if err != nil {
			return err
		}
	}
	resolvedResult, err := destination.New(destination.Options{Paths: invocation.Config.Paths, Home: home}).Resolve(requested)
	if err != nil {
		return err
	}
	resolved := resolvedResult.Path
	sourceOperand := destination.ExpandHome(parsed.Source, home)
	if err := gitops.Clone(ctx, invocation.Runner, sourceOperand, resolved); err != nil {
		if _, statErr := os.Lstat(resolved); statErr == nil {
			return fmt.Errorf("%w (partial destination %q remains; remove it before retrying)", err, resolved)
		}
		return err
	}
	if parsed.AddParent && resolvedResult.AddParent {
		if _, err := addParentToConfig(invocation, resolved); err != nil {
			return fmt.Errorf("clone succeeded but add parent: %w", err)
		}
	}

	if invocation.Stdout == nil {
		return nil
	}
	result := cloneJSON{Source: parsed.Source, Destination: resolved}
	if invocation.JSON {
		if err := json.NewEncoder(invocation.Stdout).Encode(result); err != nil {
			return err
		}
		return nil
	}
	_, err = fmt.Fprintf(invocation.Stdout, "cloned %s to %s\n", parsed.Source, resolved)
	return err
}

func parseCloneArgs(args []string) (cloneArgs, error) {
	var parsed cloneArgs
	positionals := make([]string, 0, 2)
	endOptions := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if endOptions {
			if arg == "" {
				return cloneArgs{}, errors.New("clone arguments may not be empty")
			}
			positionals = append(positionals, arg)
			continue
		}
		switch arg {
		case "--":
			endOptions = true
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

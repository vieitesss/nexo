package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	cloneflow "github.com/vieitesss/nexo/internal/clone"
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
	result, err := cloneflow.Clone(ctx, cloneflow.Options{
		Runner:     invocation.Runner,
		Paths:      invocation.Config.Paths,
		ConfigPath: invocation.ConfigPath,
		Home:       os.Getenv("HOME"),
	}, cloneflow.Request{
		Source:      parsed.Source,
		Destination: parsed.Destination,
		AddParent:   parsed.AddParent,
	})
	if err != nil {
		return err
	}
	if invocation.Stdout == nil {
		return nil
	}
	output := cloneJSON{Source: result.Source, Destination: result.Destination}
	if invocation.JSON {
		if err := json.NewEncoder(invocation.Stdout).Encode(output); err != nil {
			return err
		}
		return nil
	}
	_, err = fmt.Fprintf(invocation.Stdout, "cloned %s to %s\n", result.Source, result.Destination)
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

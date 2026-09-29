package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/projects"
)

type openCommand struct{}

type openArgs struct {
	Path    string
	Options capabilities.OpenOptions
}

func init() {
	registerBuiltin(func() Command { return openCommand{} })
}

func (openCommand) Name() string { return "open" }

func (openCommand) Run(ctx context.Context, invocation Invocation) error {
	parsed, err := parseOpenArgs(invocation.Args)
	if err != nil {
		return err
	}

	discovery := projects.Discover(invocation.Config.ProjectsConfig, os.Getenv("HOME"))
	for _, candidate := range discovery.Candidates {
		if candidate.Path == parsed.Path {
			return openCandidate(ctx, invocation, parsed)
		}
	}
	return fmt.Errorf("project %q is not a current candidate", parsed.Path)
}

func openCandidate(ctx context.Context, invocation Invocation, parsed openArgs) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	opener, ok := invocation.Adapter.(capabilities.ProjectOpener)
	if !ok {
		return fmt.Errorf("backend %q does not support opening projects", invocation.Selection.Kind)
	}
	return opener.OpenProject(ctx, parsed.Path, parsed.Options)
}

func parseOpenArgs(args []string) (openArgs, error) {
	var parsed openArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--workspace-id":
			if parsed.Options.ContainerID != "" {
				return openArgs{}, errors.New("--workspace-id may be provided only once")
			}
			if i+1 >= len(args) || args[i+1] == "" || strings.HasPrefix(args[i+1], "-") {
				return openArgs{}, errors.New("--workspace-id requires a value")
			}
			i++
			parsed.Options.ContainerID = args[i]
		case strings.HasPrefix(arg, "--workspace-id="):
			if parsed.Options.ContainerID != "" {
				return openArgs{}, errors.New("--workspace-id may be provided only once")
			}
			value := strings.TrimPrefix(arg, "--workspace-id=")
			if value == "" {
				return openArgs{}, errors.New("--workspace-id requires a value")
			}
			parsed.Options.ContainerID = value
		case strings.HasPrefix(arg, "-"):
			return openArgs{}, fmt.Errorf("unknown open option %q", arg)
		case parsed.Path != "":
			return openArgs{}, fmt.Errorf("open accepts one project path, got %q", arg)
		default:
			parsed.Path = arg
		}
	}
	if parsed.Path == "" {
		return openArgs{}, errors.New("open requires a project path")
	}
	return parsed, nil
}

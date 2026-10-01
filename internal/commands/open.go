package commands

import (
	"context"
	"encoding/json"
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
	container, err := opener.OpenProject(ctx, parsed.Path, parsed.Options)
	if err != nil {
		return err
	}
	if !invocation.JSON && !parsed.Options.NoFocus {
		return nil
	}
	return writeOpenedContainer(invocation, container)
}

// openedContainerJSON is the automation shape for an opened container.
type openedContainerJSON struct {
	Backend string `json:"backend"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Created bool   `json:"created"`
}

func newOpenedContainerJSON(backendName string, container capabilities.OpenedContainer) openedContainerJSON {
	return openedContainerJSON{
		Backend: backendName,
		ID:      container.ID,
		Name:    container.Name,
		Path:    container.Path,
		Created: container.Created,
	}
}

// writeOpenedContainer writes the JSON object with --json and the one-line
// human summary for a --no-focus open.
func writeOpenedContainer(invocation Invocation, container capabilities.OpenedContainer) error {
	if invocation.Stdout == nil {
		return nil
	}
	if invocation.JSON {
		return json.NewEncoder(invocation.Stdout).Encode(newOpenedContainerJSON(string(invocation.Selection.Kind), container))
	}
	_, err := fmt.Fprintf(invocation.Stdout, "opened %s (%s)\n", container.Name, container.ID)
	return err
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
		case arg == "--no-focus":
			if parsed.Options.NoFocus {
				return openArgs{}, errors.New("--no-focus may be provided only once")
			}
			parsed.Options.NoFocus = true
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

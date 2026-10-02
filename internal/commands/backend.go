package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type backendCommand struct{}

func init() {
	registerBuiltin(func() Command { return backendCommand{} })
}

func (backendCommand) Name() string { return "backend" }
func (backendCommand) Help() Help {
	return Help{Summary: "Print the selected backend and its capabilities.", Usage: "nexo backend", Examples: []string{"nexo --json backend"}}
}

func (backendCommand) Run(_ context.Context, invocation Invocation) error {
	if invocation.Stdout == nil {
		return errors.New("backend command requires stdout")
	}
	selection := invocation.Selection
	if selection.Kind == "" {
		return errors.New("backend command requires a backend selection")
	}
	if selection.Capabilities == nil {
		selection.Capabilities = []string{}
	}
	if invocation.JSON {
		return json.NewEncoder(invocation.Stdout).Encode(selection)
	}
	_, err := fmt.Fprintf(invocation.Stdout, "backend: %s\nreason: %s\nsource: %s\ncapabilities: %s\n", selection.Kind, selection.Reason, selection.Source, formatCapabilities(selection.Capabilities))
	return err
}

func formatCapabilities(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return fmt.Sprintf("%v", names)
}

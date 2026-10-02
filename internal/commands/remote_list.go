package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/vieitesss/nexo/internal/remote"
)

func runRemoteList(ctx context.Context, invocation Invocation) error {
	if invocation.Stdout == nil {
		return errors.New("remote list requires stdout")
	}
	if len(invocation.Args) != 1 {
		return UsageError{Err: fmt.Errorf("remote list accepts no arguments")}
	}
	orchestrator := newRemoteOrchestrator(invocation, nil)
	results := orchestrator.ListWithAgents(ctx, invocation.Config.Remote.ConfiguredMachines())
	if invocation.JSON {
		return json.NewEncoder(invocation.Stdout).Encode(results)
	}
	return writeRemoteResults(invocation.Stdout, results)
}

func writeRemoteResults(stdout io.Writer, results []remote.MachineResult) error {
	for _, result := range results {
		if result.Error != "" {
			if _, err := fmt.Fprintf(stdout, "%s: %s\n", result.Machine, result.Error); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(stdout, "%s:\n", result.Machine); err != nil {
			return err
		}
		if result.Warning != "" {
			if _, err := fmt.Fprintf(stdout, "  warning: %s\n", result.Warning); err != nil {
				return err
			}
		}
		for _, container := range result.Containers {
			if _, err := fmt.Fprintf(stdout, "  %s\n", container.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

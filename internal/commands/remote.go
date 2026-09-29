package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/vieitesss/nexo/internal/remote"
)

type remoteCommand struct{}

func init() {
	registerBuiltin(func() Command { return remoteCommand{} })
}

func (remoteCommand) Name() string { return "remote" }

func (remoteCommand) Run(ctx context.Context, invocation Invocation) error {
	if invocation.Stdout == nil {
		return errors.New("remote command requires stdout")
	}
	if len(invocation.Args) != 1 || invocation.Args[0] != "list" {
		if len(invocation.Args) == 0 {
			return errors.New("remote requires a subcommand (want list)")
		}
		return fmt.Errorf("unknown remote subcommand %q (want list)", invocation.Args[0])
	}
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	lister := remote.NewLister(invocation.Runner, invocation.Selection.Kind, invocation.Config.Remote.SSHConfig)
	results := lister.List(ctx, invocation.Config.Remote.Machines)
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
		for _, container := range result.Containers {
			if _, err := fmt.Fprintf(stdout, "  %s\n", container.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

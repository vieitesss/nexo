package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/vieitesss/nexo/internal/capabilities"
)

type previousCommand struct{}

func init() {
	registerBuiltin(func() Command { return previousCommand{} })
}

func (previousCommand) Name() string { return "previous" }
func (previousCommand) Help() Help {
	return Help{Summary: "Open the last non-proxy local container.", Usage: "nexo previous"}
}

// Run opens the most recent live non-proxy container that is not the current
// one. It is the scriptable equivalent of tmux's switch-client -l.
func (previousCommand) Run(ctx context.Context, invocation Invocation) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	if len(invocation.Args) != 0 {
		return UsageError{Err: errors.New("previous accepts no arguments")}
	}
	opener, ok := invocation.Adapter.(capabilities.ContainerOpener)
	if !ok {
		return fmt.Errorf("backend %q does not support opening containers", invocation.Selection.Kind)
	}
	entries, err := recentContainerEntries(ctx, invocation)
	if err != nil {
		return fmt.Errorf("read previous container: %w", err)
	}
	live, err := liveLocalContainerIDs(ctx, invocation)
	if err != nil {
		return fmt.Errorf("list live containers: %w", err)
	}
	current, err := currentContainerID(ctx, invocation)
	if err != nil {
		return fmt.Errorf("identify current container: %w", err)
	}
	for _, entry := range entries {
		if entry.Proxy || entry.ID == "" || entry.ID == current {
			continue
		}
		if _, ok := live[entry.ID]; !ok {
			continue
		}
		return opener.OpenContainer(ctx, entry.ID)
	}
	return nil
}

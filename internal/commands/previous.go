package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
)

type previousCommand struct{}

func init() {
	registerBuiltin(func() Command { return previousCommand{} })
}

func (previousCommand) Name() string { return "previous" }

func (previousCommand) Run(ctx context.Context, invocation Invocation) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	if len(invocation.Args) != 0 {
		return errors.New("previous accepts no arguments")
	}
	entry, ok, err := historyStore(invocation).Previous(false)
	if err != nil {
		return fmt.Errorf("read previous container: %w", err)
	}
	if !ok {
		return nil
	}
	opener, ok := invocation.Adapter.(capabilities.ContainerOpener)
	if !ok {
		return fmt.Errorf("backend %q does not support opening containers", invocation.Selection.Kind)
	}
	if entry.ID == "" {
		return nil
	}
	return opener.OpenContainer(ctx, entry.ID)
}

func historyStore(invocation Invocation) containerstate.Store {
	return containerstate.New(invocation.StateDir, string(invocation.Selection.Kind), containerstate.LocalMachine())
}

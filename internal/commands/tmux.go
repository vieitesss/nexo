package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/vieitesss/nexo/internal/backend"
	tmuxbackend "github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/config"
)

type tmuxCommand struct{}

func init() {
	registerBuiltin(func() Command { return tmuxCommand{} })
}

func (tmuxCommand) Name() string { return "tmux" }

func (tmuxCommand) Run(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) != 1 || invocation.Args[0] != "init" {
		return fmt.Errorf("usage: nexo tmux init")
	}
	if invocation.Selection.Kind != backend.Tmux {
		return errors.New("tmux init requires the tmux backend")
	}
	initializer, ok := invocation.Adapter.(interface {
		Init(context.Context, tmuxbackend.InitInput) error
	})
	if !ok {
		return errors.New("selected backend does not support tmux adapter initialization")
	}
	if configurable, ok := invocation.Adapter.(interface{ ConfigureAgents(config.AgentsConfig) }); ok {
		configurable.ConfigureAgents(invocation.Config.Agents)
	}
	return initializer.Init(ctx, tmuxbackend.InitInput{Config: invocation.Config.Tmux, ConfigPath: invocation.ConfigPath})
}

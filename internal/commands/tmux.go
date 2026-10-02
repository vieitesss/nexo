package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/vieitesss/nexo/internal/backend"
	tmuxbackend "github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
)

type tmuxCommand struct{}

func init() {
	registerBuiltin(func() Command { return tmuxCommand{} })
}

func (tmuxCommand) Name() string { return "tmux" }

func (tmuxCommand) Run(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) == 0 {
		return fmt.Errorf("usage: nexo tmux init|glance on|off|toggle")
	}
	if invocation.Selection.Kind != backend.Tmux {
		return errors.New("tmux commands require the tmux backend")
	}
	switch invocation.Args[0] {
	case "init":
		if len(invocation.Args) != 1 {
			return fmt.Errorf("usage: nexo tmux init")
		}
		return runTmuxInit(ctx, invocation)
	case "glance":
		if len(invocation.Args) != 2 {
			return fmt.Errorf("usage: nexo tmux glance on|off|toggle")
		}
		return runTmuxGlance(ctx, invocation)
	default:
		return fmt.Errorf("unknown tmux command %q", invocation.Args[0])
	}
}

func runTmuxInit(ctx context.Context, invocation Invocation) error {
	initializer, ok := invocation.Adapter.(capabilities.TmuxInitializer)
	if !ok {
		return errors.New("selected backend does not support tmux adapter initialization")
	}
	return initializer.Init(ctx, capabilities.InitInput{ConfigPath: invocation.ConfigPath})
}

func runTmuxGlance(ctx context.Context, invocation Invocation) error {
	action, err := tmuxbackend.ParseGlanceAction(invocation.Args[1])
	if err != nil {
		return UsageError{Err: err}
	}
	if configurable, ok := invocation.Adapter.(capabilities.GlanceConfigurator); ok {
		configurable.ConfigureGlance(invocation.Config.Glance)
	}
	controller, ok := invocation.Adapter.(capabilities.GlanceController)
	if !ok {
		return errors.New("selected backend does not support the glance row")
	}
	return controller.Glance(ctx, action)
}

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
func (tmuxCommand) Help() Help {
	return Help{Summary: "Install and control the tmux adapter.", Usage: "nexo tmux <subcommand>", Subcommands: []SubcommandHelp{
		{Name: "glance", Summary: "Show, hide, or toggle the agent glance row.", Usage: "nexo tmux glance on|off|toggle"},
		{Name: "init", Summary: "Install focus hooks and start the agent watcher.", Usage: "nexo tmux init"},
	}, Examples: []string{"nexo tmux init", "nexo tmux glance toggle"}}
}

func (tmuxCommand) Run(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) == 0 {
		return UsageError{Err: fmt.Errorf("usage: nexo tmux init|glance on|off|toggle")}
	}
	if invocation.Selection.Kind != backend.Tmux {
		return errors.New("tmux commands require the tmux backend")
	}
	switch invocation.Args[0] {
	case "init":
		if len(invocation.Args) != 1 {
			return UsageError{Err: fmt.Errorf("usage: nexo tmux init")}
		}
		return runTmuxInit(ctx, invocation)
	case "glance":
		if len(invocation.Args) != 2 {
			return UsageError{Err: fmt.Errorf("usage: nexo tmux glance on|off|toggle")}
		}
		return runTmuxGlance(ctx, invocation)
	default:
		return UsageError{Err: fmt.Errorf("unknown tmux command %q", invocation.Args[0])}
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

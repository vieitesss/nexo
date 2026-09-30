package commands

import (
	"context"
	"fmt"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

type tuiCommand struct{}

func init() {
	registerBuiltin(func() Command { return tuiCommand{} })
}

func (tuiCommand) Name() string { return "tui" }

func (tuiCommand) Run(ctx context.Context, invocation Invocation) error {
	if configurable, ok := invocation.Adapter.(interface{ ConfigureAgents(config.AgentsConfig) }); ok {
		configurable.ConfigureAgents(invocation.Config.Agents)
	}
	tab, err := tuiTab(ctx, invocation)
	if err != nil {
		return err
	}
	if tab == "agents" {
		return runAgentPicker(ctx, invocation)
	}
	picker := placePickerCommand{tab: tab}
	return picker.Run(ctx, invocation)
}

// tuiTab is deliberately the one routing seam for the tabbed TUI. The tabbed
// implementation can replace this function without changing command dispatch.
func tuiTab(ctx context.Context, invocation Invocation) (string, error) {
	if len(invocation.Args) > 1 {
		return "", fmt.Errorf("usage: nexo tui [agents|containers|projects]")
	}
	if len(invocation.Args) == 1 {
		switch invocation.Args[0] {
		case "agents", "containers", "projects":
			return invocation.Args[0], nil
		default:
			return "", fmt.Errorf("unknown tui tab %q", invocation.Args[0])
		}
	}
	source, ok := invocation.Adapter.(capabilities.AgentSource)
	if !ok {
		return "containers", nil
	}
	agents, err := source.ListAgents(ctx)
	if err != nil {
		return "", fmt.Errorf("choose default tui tab: %w", err)
	}
	for _, agent := range agents {
		if agent.State == "stopped" && !agent.Seen {
			return "agents", nil
		}
	}
	return "containers", nil
}

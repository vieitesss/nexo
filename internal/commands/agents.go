package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
)

type agentsCommand struct{}

func init() {
	registerBuiltin(func() Command { return agentsCommand{} })
}

func (agentsCommand) Name() string { return "agents" }

func (agentsCommand) Run(ctx context.Context, invocation Invocation) error {
	if invocation.Adapter == nil {
		return errors.New("agents requires a backend with the AgentSource capability")
	}
	if configurable, ok := invocation.Adapter.(interface{ ConfigureAgents(config.AgentsConfig) }); ok {
		configurable.ConfigureAgents(invocation.Config.Agents)
	}
	if len(invocation.Args) > 0 {
		switch invocation.Args[0] {
		case "watch":
			if len(invocation.Args) != 1 {
				return UsageError{Err: errors.New("usage: nexo agents watch")}
			}
			if configurable, ok := invocation.Adapter.(interface {
				ConfigureGlance(config.GlanceConfig)
			}); ok {
				configurable.ConfigureGlance(invocation.Config.Glance)
			}
			if configurable, ok := invocation.Adapter.(interface {
				ConfigureAgentCommand(string, string)
			}); ok {
				configurable.ConfigureAgentCommand(os.Args[0], invocation.ConfigPath)
			}
			if watcher, ok := invocation.Adapter.(interface{ WatchAgents(context.Context) error }); ok {
				return watcher.WatchAgents(ctx)
			}
			return errors.New("selected backend does not support agent watching")
		default:
			return fmt.Errorf("unknown agents command %q", invocation.Args[0])
		}
	}

	if invocation.Stdout == nil {
		return errors.New("agents command requires stdout")
	}
	source, ok := invocation.Adapter.(capabilities.AgentSource)
	if !ok {
		return errors.New("selected backend does not support agents")
	}
	agents, err := source.ListAgents(ctx)
	if err != nil {
		return err
	}
	agents = picker.SortAgents(agents)
	if invocation.JSON {
		return json.NewEncoder(invocation.Stdout).Encode(agents)
	}
	for _, agent := range agents {
		seen := "unseen"
		if agent.Seen {
			seen = "seen"
		}
		stoppedSince := "-"
		if agent.StoppedSince != nil {
			stoppedSince = agent.StoppedSince.Format(time.RFC3339)
		}
		if _, err := fmt.Fprintf(invocation.Stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", agent.Target, agent.Harness, agent.State, seen, stoppedSince, agent.Branch); err != nil {
			return err
		}
	}
	return nil
}

package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
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
			if watcher, ok := invocation.Adapter.(interface{ WatchAgents(context.Context) error }); ok {
				return watcher.WatchAgents(ctx)
			}
			return errors.New("selected backend does not support agent watching")
		case "seen":
			if len(invocation.Args) != 2 || strings.TrimSpace(invocation.Args[1]) == "" {
				return errors.New("agents seen requires a pane id")
			}
			if marker, ok := invocation.Adapter.(interface {
				MarkAgentSeen(context.Context, string) error
			}); ok {
				return marker.MarkAgentSeen(ctx, invocation.Args[1])
			}
			return errors.New("selected backend does not support marking agents seen")
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
	sort.SliceStable(agents, func(i, j int) bool {
		if agents[i].State != agents[j].State {
			return agents[i].State == "stopped"
		}
		if agents[i].State == "stopped" && agents[i].Seen != agents[j].Seen {
			return !agents[i].Seen
		}
		return agents[i].Target < agents[j].Target
	})
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

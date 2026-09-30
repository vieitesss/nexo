package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/tui"
)

type tuiCommand struct{}

var runTabbedTUI = tui.Run

func init()                     { registerBuiltin(func() Command { return tuiCommand{} }) }
func (tuiCommand) Name() string { return "tui" }

func (tuiCommand) Run(ctx context.Context, invocation Invocation) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	if configurable, ok := invocation.Adapter.(interface{ ConfigureAgents(config.AgentsConfig) }); ok {
		configurable.ConfigureAgents(invocation.Config.Agents)
	}
	var initial []capabilities.Agent
	var initialErr error
	var tab string
	var err error
	if len(invocation.Args) == 0 {
		if source, ok := invocation.Adapter.(capabilities.AgentSource); ok {
			initial, initialErr = source.ListAgents(ctx)
			if initialErr != nil {
				initial = []capabilities.Agent{}
				tab = string(tui.ContainersTab)
			} else {
				tab = string(tui.DefaultTab(initial))
			}
		} else {
			tab = string(tui.ContainersTab)
		}
	} else {
		tab, err = tuiTab(ctx, invocation)
	}
	if err != nil {
		return err
	}
	deps, err := tuiDependencies(ctx, invocation, tab, initial, initialErr)
	if err != nil {
		return err
	}
	return runTabbedTUI(ctx, deps, tui.Tab(tab))
}

func tuiDependencies(ctx context.Context, invocation Invocation, tab string, initial []capabilities.Agent, initialErr error) (tui.Deps, error) {
	discovery := projects.Discover(invocation.Config.ProjectsConfig, os.Getenv("HOME"))
	for _, warning := range discovery.Warnings {
		if invocation.Stderr != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "nexo: warning: %s\n", warning)
		}
	}
	deps := tui.Deps{AgentError: initialErr, Backend: string(invocation.Selection.Kind), Projects: picker.ProjectPlaces(discovery.Candidates), Input: invocation.Stdin, Output: invocation.Stdout}
	if lister, ok := invocation.Adapter.(capabilities.ContainerLister); ok {
		containers, err := lister.ListContainers(ctx)
		if err != nil {
			return tui.Deps{}, fmt.Errorf("list containers: %w", err)
		}
		deps.Containers = containers
		if reader, ok := invocation.Adapter.(capabilities.ScreenReader); ok {
			deps.PreviewCommand = previewCommand(ctx, reader, containers)
		}
	}
	if source, ok := invocation.Adapter.(capabilities.AgentSource); ok {
		deps.AgentSource = source
		deps.Agents = initial
		if initial == nil {
			agents, listErr := source.ListAgents(ctx)
			if listErr != nil {
				deps.AgentError = listErr
			} else {
				deps.Agents = agents
			}
		}
		cfg := invocation.Config.Agents.WithDefaults()
		deps.AgentRefreshInterval = time.Duration(cfg.PollInterval) * time.Second
	}
	if invocation.Runner != nil {
		deps.GitLoader = picker.NewGitContextLoader(invocation.Runner)
	}
	deps.RemoteCommands = remotePlaceCommands(ctx, invocation)
	deps.RemoteMachines = append([]string(nil), invocation.Config.Remote.Machines...)
	deps.OpenProject = func(openCtx context.Context, place picker.Place) error {
		opener, ok := invocation.Adapter.(capabilities.ProjectOpener)
		if !ok {
			return fmt.Errorf("backend %q does not support opening projects", invocation.Selection.Kind)
		}
		return opener.OpenProject(openCtx, place.Path, capabilities.OpenOptions{})
	}
	resolver := containerActionResolver(invocation)
	deps.OpenContainer = func(openCtx context.Context, place picker.Place) error {
		actions, resolveErr := resolver.ResolveContainerActions(openCtx, place.Machine)
		if resolveErr != nil {
			return resolveErr
		}
		return actions.OpenContainer(openCtx, containerOpenID(place))
	}
	deps.RenameContainer = func(actionCtx context.Context, ref picker.ContainerRef, name string) error {
		actions, resolveErr := resolver.ResolveContainerActions(actionCtx, ref.Machine)
		if resolveErr != nil {
			return resolveErr
		}
		return actions.RenameContainer(actionCtx, ref.ID, name)
	}
	deps.KillContainers = func(actionCtx context.Context, refs []picker.ContainerRef) error {
		groups := make([]containerTargetGroup, 0)
		for _, ref := range refs {
			index := -1
			for i := range groups {
				if groups[i].machine == ref.Machine {
					index = i
					break
				}
			}
			if index < 0 {
				groups = append(groups, containerTargetGroup{machine: ref.Machine})
				index = len(groups) - 1
			}
			groups[index].ids = append(groups[index].ids, ref.ID)
		}
		for _, group := range groups {
			actions, resolveErr := resolver.ResolveContainerActions(actionCtx, group.machine)
			if resolveErr != nil {
				return resolveErr
			}
			if batch, ok := actions.(capabilities.ContainerBatchKiller); ok {
				if err := batch.KillContainers(actionCtx, group.ids); err != nil {
					return err
				}
				continue
			}
			for _, id := range group.ids {
				if err := actions.KillContainer(actionCtx, id); err != nil {
					return err
				}
			}
		}
		return nil
	}
	deps.FocusAgent = func(focusCtx context.Context, ref tui.AgentRef) error {
		if ref.Machine != "" && ref.Machine != picker.LocalMachine() {
			actions, resolveErr := resolver.ResolveContainerActions(focusCtx, ref.Machine)
			if resolveErr != nil {
				return resolveErr
			}
			id := ref.Agent.ContainerID
			if id == "" {
				id = ref.Agent.Container
			}
			return actions.OpenContainer(focusCtx, id)
		}
		focuser, ok := invocation.Adapter.(capabilities.PaneFocuser)
		if !ok {
			return errors.New("selected backend does not support focusing panes")
		}
		return focuser.FocusPane(focusCtx, ref.Agent.PaneID)
	}
	return deps, nil
}

// tuiTab validates the explicit opening tab and applies the no-argument rule.
func tuiTab(ctx context.Context, invocation Invocation) (string, error) {
	if len(invocation.Args) > 1 {
		return "", UsageError{Err: fmt.Errorf("usage: nexo tui [agents|containers|projects]")}
	}
	if len(invocation.Args) == 1 {
		switch invocation.Args[0] {
		case "agents", "containers", "projects":
			return invocation.Args[0], nil
		default:
			return "", UsageError{Err: fmt.Errorf("unknown tui tab %q", invocation.Args[0])}
		}
	}
	if source, ok := invocation.Adapter.(capabilities.AgentSource); ok {
		agents, err := source.ListAgents(ctx)
		if err != nil {
			return "", fmt.Errorf("choose default tui tab: %w", err)
		}
		return string(tui.DefaultTab(agents)), nil
	}
	return string(tui.ContainersTab), nil
}

package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/remote"
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
	var initialRead bool
	var tab string
	var err error
	if len(invocation.Args) == 0 {
		if source, ok := invocation.Adapter.(capabilities.AgentSource); ok {
			initialRead = true
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
	deps, err := tuiDependencies(ctx, invocation, tab, initial, initialErr, initialRead)
	if err != nil {
		return err
	}
	return runTabbedTUI(ctx, deps, tui.Tab(tab))
}

func tuiDependencies(ctx context.Context, invocation Invocation, tab string, initial []capabilities.Agent, initialErr error, initialRead ...bool) (tui.Deps, error) {
	discovery := projects.Discover(invocation.Config.ProjectsConfig, os.Getenv("HOME"))
	for _, warning := range discovery.Warnings {
		if invocation.Stderr != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "nexo: warning: %s\n", warning)
		}
	}
	deps := tui.Deps{AgentError: initialErr, Backend: string(invocation.Selection.Kind), Theme: invocation.Config.TUI.EffectiveTheme(), Projects: picker.ProjectPlaces(discovery.Candidates), Input: invocation.Stdin, Output: invocation.Stdout}
	localProxyIDs := make(map[string]bool)
	if lister, ok := invocation.Adapter.(capabilities.ContainerLister); ok {
		containers, err := lister.ListContainers(ctx)
		if err != nil {
			return tui.Deps{}, fmt.Errorf("list containers: %w", err)
		}
		deps.Containers = containers
		for _, container := range containers {
			if container.Proxy {
				localProxyIDs[container.Machine+"\x00"+container.ID] = true
			}
		}
	}
	if source, ok := invocation.Adapter.(capabilities.AgentSource); ok {
		deps.AgentSource = source
		deps.Agents = initial
		readInitial := len(initialRead) > 0 && initialRead[0]
		if !readInitial {
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
	deps.RemoteMachines = append([]string(nil), invocation.Config.Remote.ConfiguredMachines()...)
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
			for _, id := range group.ids {
				if !localProxyIDs[group.machine+"\x00"+id] {
					continue
				}
				actions, resolveErr := (localContainerResolver{adapter: invocation.Adapter}).ResolveContainerActions(actionCtx, picker.LocalMachine())
				if resolveErr != nil {
					return resolveErr
				}
				if err := actions.KillContainer(actionCtx, id); err != nil {
					return err
				}
			}
			remoteIDs := make([]string, 0, len(group.ids))
			for _, id := range group.ids {
				if !localProxyIDs[group.machine+"\x00"+id] {
					remoteIDs = append(remoteIDs, id)
				}
			}
			if len(remoteIDs) == 0 {
				continue
			}
			actions, resolveErr := resolver.ResolveContainerActions(actionCtx, group.machine)
			if resolveErr != nil {
				return resolveErr
			}
			if batch, ok := actions.(capabilities.ContainerBatchKiller); ok {
				if err := batch.KillContainers(actionCtx, remoteIDs); err != nil {
					return err
				}
				continue
			}
			for _, id := range remoteIDs {
				if err := actions.KillContainer(actionCtx, id); err != nil {
					return err
				}
			}
		}
		return nil
	}
	deps.FocusAgent = func(focusCtx context.Context, ref tui.AgentRef) error {
		if ref.Machine != "" && ref.Machine != picker.LocalMachine() {
			kind := remote.Kind(ref.Backend)
			container := strings.TrimSpace(ref.Agent.Container)
			if container == "" {
				container = strings.TrimSpace(ref.Agent.ContainerID)
			}
			if container == "" {
				return errors.New("remote agent has no container name")
			}
			switch kind {
			case remote.KindTmux:
				if invocation.Selection.Kind != backend.Tmux && invocation.Selection.Kind != backend.Herdr {
					return fmt.Errorf("unsupported: %s runs tmux; use tmux or Herdr to focus it", ref.Machine)
				}
			case remote.KindHerdr:
				if invocation.Selection.Kind != backend.Herdr {
					return fmt.Errorf("unsupported: %s runs Herdr; use Herdr to focus it", ref.Machine)
				}
			default:
				return fmt.Errorf("remote machine %q has an unknown multiplexer kind", ref.Machine)
			}
			// Pane IDs belong to the remote multiplexer. Focus them over SSH
			// before opening the proxy/sidebar entry; never pass them to the
			// local adapter, whose namespace may contain the same ID.
			if err := remote.FocusRemotePane(focusCtx, invocation.Runner, invocation.Config.Remote.SSHConfig, ref.Machine, kind, ref.Agent.PaneID); err != nil {
				return err
			}
			return openRemoteContainerWithResolver(focusCtx, invocation, ref.Machine, container, kind)
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

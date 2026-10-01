package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/remote"
)

func containerOpenID(place picker.Place) string {
	if !isLocalContainer(place.Machine) && strings.TrimSpace(place.ContainerName) != "" {
		// Remote tmux IDs are stable backend handles, but proxies attach by
		// the displayed session name and use it for their local identity.
		return place.ContainerName
	}
	return place.ContainerID
}

type containerTargetGroup struct {
	machine string
	ids     []string
}

type localContainerResolver struct {
	adapter any
}

func containerActionResolver(invocation Invocation) capabilities.ContainerActionResolver {
	if invocation.ContainerResolver != nil {
		return invocation.ContainerResolver
	}
	var proxy capabilities.RemoteProxy
	if candidate, ok := invocation.Adapter.(capabilities.RemoteProxy); ok {
		proxy = candidate
	}
	return configuredContainerResolver{
		local:  localContainerResolver{adapter: invocation.Adapter},
		remote: configuredRemoteMachines(invocation.Config.Remote.ConfiguredMachines()),
		proxy:  proxy,
	}
}

func configuredRemoteMachines(machines []string) map[string]bool {
	configured := make(map[string]bool, len(machines))
	for _, machine := range machines {
		machine = config.NormalizeMachineName(machine)
		if machine != "" {
			configured[machine] = true
		}
	}
	return configured
}

type configuredContainerResolver struct {
	local  localContainerResolver
	remote map[string]bool
	proxy  capabilities.RemoteProxy
}

func (r configuredContainerResolver) ResolveContainerActions(ctx context.Context, machine string) (capabilities.ContainerActions, error) {
	if isLocalContainer(machine) {
		return r.local.ResolveContainerActions(ctx, machine)
	}
	if r.remote[config.NormalizeMachineName(machine)] {
		if r.proxy != nil {
			return remoteContainerActions{machine: machine, proxy: r.proxy}, nil
		}
		return unsupportedRemoteContainerActions{machine: machine}, nil
	}
	return nil, fmt.Errorf("container machine %q is not configured", machine)
}

type remoteContainerActions struct {
	machine string
	proxy   capabilities.RemoteProxy
}

func (a remoteContainerActions) OpenContainer(ctx context.Context, id string) error {
	return a.proxy.Proxy(ctx, a.machine, id)
}

func (a remoteContainerActions) RenameContainer(context.Context, string, string) error {
	return fmt.Errorf("renaming remote containers is not yet supported for machine %q", a.machine)
}

func (a remoteContainerActions) KillContainer(context.Context, string) error {
	return fmt.Errorf("killing remote containers is not yet supported for machine %q", a.machine)
}

type unsupportedRemoteContainerActions struct {
	machine string
}

func (a unsupportedRemoteContainerActions) OpenContainer(context.Context, string) error {
	return fmt.Errorf("opening remote containers is not yet supported for machine %q", a.machine)
}

func (a unsupportedRemoteContainerActions) RenameContainer(context.Context, string, string) error {
	return fmt.Errorf("renaming remote containers is not yet supported for machine %q", a.machine)
}

func (a unsupportedRemoteContainerActions) KillContainer(context.Context, string) error {
	return fmt.Errorf("killing remote containers is not yet supported for machine %q", a.machine)
}

// ResolveContainerActions wraps the selected backend as the local machine's
// source. A future remote resolver can be supplied on Invocation without
// changing TUI action routing.
func (r localContainerResolver) ResolveContainerActions(_ context.Context, machine string) (capabilities.ContainerActions, error) {
	if !isLocalContainer(machine) {
		return nil, fmt.Errorf("container machine %q is not supported", machine)
	}
	return localContainerActions(r), nil
}

type localContainerActions struct {
	adapter any
}

func (a localContainerActions) OpenContainer(ctx context.Context, id string) error {
	if opener, ok := a.adapter.(capabilities.ContainerOpener); ok {
		return opener.OpenContainer(ctx, id)
	}
	// Keep the existing project-opener seam usable for adapters that model an
	// existing container as an OpenOptions selection.
	if opener, ok := a.adapter.(capabilities.ProjectOpener); ok {
		return opener.OpenProject(ctx, "", capabilities.OpenOptions{ContainerID: id})
	}
	return errors.New("local backend does not support opening containers")
}

func (a localContainerActions) RenameContainer(ctx context.Context, id, name string) error {
	if renamer, ok := a.adapter.(capabilities.ContainerRenamer); ok {
		return renamer.RenameContainer(ctx, id, name)
	}
	return errors.New("local backend does not support renaming containers")
}

func (a localContainerActions) KillContainer(ctx context.Context, id string) error {
	if killer, ok := a.adapter.(capabilities.ContainerKiller); ok {
		return killer.KillContainer(ctx, id)
	}
	return errors.New("local backend does not support killing containers")
}

func (a localContainerActions) KillContainers(ctx context.Context, ids []string) error {
	if killer, ok := a.adapter.(capabilities.ContainerBatchKiller); ok {
		return killer.KillContainers(ctx, ids)
	}
	for _, id := range ids {
		if err := a.KillContainer(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func isLocalContainer(machine string) bool {
	machine = strings.TrimSpace(machine)
	return machine == "" || config.NormalizeMachineName(machine) == "local" || config.NormalizeMachineName(machine) == config.NormalizeMachineName(picker.LocalMachine())
}

func remotePlaceCommands(ctx context.Context, invocation Invocation) []tea.Cmd {
	machines := invocation.Config.Remote.ConfiguredMachines()
	if len(machines) == 0 || invocation.Runner == nil {
		return nil
	}
	lister := remote.NewLister(invocation.Runner, invocation.Selection.Kind, invocation.Config.Remote.SSHConfig).WithBackends(invocation.Config.Remote.Backends).WithKindDetection()
	commands := make([]tea.Cmd, 0, len(machines))
	seen := make(map[string]bool)
	for _, configured := range machines {
		machine := strings.TrimSpace(configured)
		key := config.NormalizeMachineName(machine)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		commands = append(commands, func() tea.Msg {
			result := lister.ListMachineWithAgents(ctx, machine)
			places := picker.ContainerPlaces(result.Containers)
			for index := range places {
				if places[index].Kind != picker.ContainerKind {
					continue
				}
				kind := result.Kind
				places[index].OpenAction = func(openCtx context.Context, place picker.Place) error {
					return openRemoteContainerWithResolver(openCtx, invocation, machine, containerOpenID(place), kind)
				}
			}
			return picker.RemoteBatch{Machine: result.Machine, Backend: string(result.Kind), Places: places, Agents: result.Agents, Warning: result.Warning, Error: result.Error}
		})
	}
	return commands
}

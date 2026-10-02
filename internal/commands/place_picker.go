package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/proxyidentity"
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
		remote: proxyidentity.NewMachineSet(invocation.Config.Remote.ConfiguredMachines()),
		proxy:  proxy,
	}
}

type configuredContainerResolver struct {
	local  localContainerResolver
	remote proxyidentity.MachineSet
	proxy  capabilities.RemoteProxy
}

func (r configuredContainerResolver) ResolveContainerActions(ctx context.Context, machine string) (capabilities.ContainerActions, error) {
	if isLocalContainer(machine) {
		return r.local.ResolveContainerActions(ctx, machine)
	}
	if r.remote.Contains(machine) {
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

// ResolveContainerActions routes local containers through the selected backend
// and configured remote containers through their proxy capability.
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
	return machine == "" || proxyidentity.NormalizeMachineName(machine) == "local" || proxyidentity.NormalizeMachineName(machine) == proxyidentity.NormalizeMachineName(picker.LocalMachine())
}

func remotePlaceCommands(ctx context.Context, invocation Invocation) []tea.Cmd {
	machines := invocation.Config.Remote.ConfiguredMachines()
	if len(machines) == 0 || invocation.Runner == nil {
		return nil
	}
	orchestrator := newRemoteOrchestrator(invocation, containerActionResolver(invocation))
	commands := make([]tea.Cmd, 0, len(machines))
	seen := make(map[string]bool)
	for _, configured := range machines {
		machine := strings.TrimSpace(configured)
		key := proxyidentity.NormalizeMachineName(machine)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		commands = append(commands, func() tea.Msg {
			result := orchestrator.ListMachineWithAgents(ctx, machine)
			places := picker.ContainerPlaces(result.Containers)
			for index := range places {
				if places[index].Kind != picker.ContainerKind {
					continue
				}
				places[index].OpenAction = func(openCtx context.Context, place picker.Place) error {
					return orchestrator.Open(openCtx, result, containerOpenID(place))
				}
			}
			return picker.RemoteBatch{Machine: result.Machine, Backend: string(result.Kind), Places: places, Agents: result.Agents, Warning: result.Warning, Error: result.Error}
		})
	}
	return commands
}

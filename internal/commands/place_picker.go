package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/remote"
)

type placePickerCommand struct{}

func (placePickerCommand) Run(ctx context.Context, invocation Invocation) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}

	discovery := projects.Discover(invocation.Config.ProjectsConfig, os.Getenv("HOME"))
	places := picker.ProjectPlaces(discovery.Candidates)
	var previewCmd func() tea.Msg
	var agentCmd tea.Cmd
	if lister, ok := invocation.Adapter.(capabilities.ContainerLister); ok {
		containers, err := lister.ListContainers(ctx)
		if err != nil {
			return fmt.Errorf("list containers: %w", err)
		}
		places = append(places, picker.ContainerPlaces(containers)...)
		places = append(places, picker.MachinePlaces(containers)...)
		if reader, ok := invocation.Adapter.(capabilities.ScreenReader); ok {
			previewCmd = previewCommand(ctx, reader, containers)
		}
	}
	if configurable, ok := invocation.Adapter.(interface{ ConfigureAgents(config.AgentsConfig) }); ok {
		configurable.ConfigureAgents(invocation.Config.Agents)
	}
	if source, ok := invocation.Adapter.(capabilities.AgentSource); ok {
		agentCmd = func() tea.Msg {
			agents, err := source.ListAgents(ctx)
			if err != nil {
				return picker.AgentBatch{Error: err.Error()}
			}
			return picker.AgentBatch{Agents: agents}
		}
	}
	for _, warning := range discovery.Warnings {
		if invocation.Stderr != nil {
			if _, err := fmt.Fprintf(invocation.Stderr, "nexo: warning: %s\n", warning); err != nil {
				return err
			}
		}
	}

	theme := picker.ThemeFromTerminal(ctx, picker.ThemeQuery{Reader: os.Stdin, Writer: os.Stdout}, 100*time.Millisecond)
	model := picker.NewModel(places, theme, picker.NewGitContextLoader(invocation.Runner))
	if agentCmd != nil {
		model = model.WithAgentCommand(agentCmd)
	}
	if previewCmd != nil {
		model = model.WithPreviewCommand(previewCmd)
	}
	if remoteCmds := remotePlaceCommands(ctx, invocation); len(remoteCmds) > 0 {
		model = model.WithRemoteCommands(remoteCmds)
	}
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	result, err := program.Run()
	if err != nil {
		return fmt.Errorf("run place picker: %w", err)
	}
	final, ok := result.(picker.Model)
	if !ok {
		return errors.New("place picker returned an invalid model")
	}
	if final.Cancelled() {
		return nil
	}
	resolver := containerActionResolver(invocation)
	if target, name, ok := final.RenameTargetRequest(); ok {
		actions, err := resolver.ResolveContainerActions(ctx, target.Machine)
		if err != nil {
			return err
		}
		return actions.RenameContainer(ctx, target.ID, name)
	}
	if targets, ok := final.KillTargetsRequest(); ok {
		// A selection can span machines. Resolve each machine once and keep the
		// picker order within that machine so native batch close operations still
		// receive one confirmation for the complete selection.
		groups := make([]containerTargetGroup, 0)
		for _, target := range targets {
			groupIndex := -1
			for index := range groups {
				if groups[index].machine == target.Machine {
					groupIndex = index
					break
				}
			}
			if groupIndex < 0 {
				groups = append(groups, containerTargetGroup{machine: target.Machine})
				groupIndex = len(groups) - 1
			}
			groups[groupIndex].ids = append(groups[groupIndex].ids, target.ID)
		}
		for _, group := range groups {
			actions, err := resolver.ResolveContainerActions(ctx, group.machine)
			if err != nil {
				return err
			}
			if killer, supported := actions.(capabilities.ContainerBatchKiller); supported {
				// The batch capability receives the whole per-machine selection
				// exactly once; the backend may issue one native close per ID.
				if err := killer.KillContainers(ctx, group.ids); err != nil {
					return err
				}
				continue
			}
			for _, id := range group.ids {
				if err := actions.KillContainer(ctx, id); err != nil {
					return err
				}
			}
		}
		return nil
	}
	place, ok := final.Selected()
	if !ok {
		return nil
	}
	if place.OpenAction != nil {
		return place.OpenAction(ctx, place)
	}
	if place.Kind != picker.ProjectKind && place.Kind != picker.ContainerKind {
		return fmt.Errorf("place kind %q is not openable yet", place.Kind)
	}
	if place.Kind == picker.ContainerKind {
		actions, err := resolver.ResolveContainerActions(ctx, place.Machine)
		if err != nil {
			return err
		}
		return actions.OpenContainer(ctx, containerOpenID(place))
	}
	opener, ok := invocation.Adapter.(capabilities.ProjectOpener)
	if !ok {
		return fmt.Errorf("backend %q does not support opening projects", invocation.Selection.Kind)
	}
	return opener.OpenProject(ctx, place.Path, capabilities.OpenOptions{})
}

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
		remote: configuredRemoteMachines(invocation.Config.Remote.Machines),
		proxy:  proxy,
	}
}

func configuredRemoteMachines(machines []string) map[string]bool {
	configured := make(map[string]bool, len(machines))
	for _, machine := range machines {
		machine = strings.TrimSpace(machine)
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
	if r.remote[machine] {
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
// changing the picker command's action routing.
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
	return machine == "" || machine == "local" || machine == picker.LocalMachine()
}

func remotePlaceCommands(ctx context.Context, invocation Invocation) []tea.Cmd {
	if len(invocation.Config.Remote.Machines) == 0 || invocation.Runner == nil {
		return nil
	}
	lister := remote.NewLister(invocation.Runner, invocation.Selection.Kind, invocation.Config.Remote.SSHConfig).WithKindDetection()
	commands := make([]tea.Cmd, 0, len(invocation.Config.Remote.Machines))
	seen := make(map[string]bool)
	for _, configured := range invocation.Config.Remote.Machines {
		machine := strings.TrimSpace(configured)
		if machine == "" || seen[machine] {
			continue
		}
		seen[machine] = true
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
			return picker.RemoteBatch{Machine: result.Machine, Places: places, Agents: result.Agents, Error: result.Error}
		})
	}
	return commands
}

func previewCommand(ctx context.Context, reader capabilities.ScreenReader, containers []capabilities.Container) func() tea.Msg {
	return func() tea.Msg {
		type job struct {
			ref  picker.ContainerRef
			pane string
		}
		jobs := make(chan job)
		previews := make(map[picker.ContainerRef]string)
		var mu sync.Mutex
		var wg sync.WaitGroup
		workers := 4
		if len(containers) < workers {
			workers = len(containers)
		}
		if workers == 0 {
			return nil
		}
		worker := func() {
			defer wg.Done()
			for item := range jobs {
				readCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
				preview, err := reader.ReadScreen(readCtx, item.pane)
				cancel()
				if err == nil {
					mu.Lock()
					previews[item.ref] = preview
					mu.Unlock()
				}
			}
		}
		wg.Add(workers)
		for index := 0; index < workers; index++ {
			go worker()
		}
		for _, container := range containers {
			if container.ActivePaneID != "" {
				machine := container.Machine
				if machine == "" {
					machine = picker.LocalMachine()
				}
				jobs <- job{ref: picker.ContainerRef{Machine: machine, ID: container.ID}, pane: container.ActivePaneID}
			}
		}
		close(jobs)
		wg.Wait()
		if len(previews) == 0 {
			return nil
		}
		return picker.PreviewBatch{Previews: previews}
	}
}

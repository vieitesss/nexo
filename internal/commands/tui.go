package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	cloneflow "github.com/vieitesss/nexo/internal/clone"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/deletion"
	"github.com/vieitesss/nexo/internal/destination"
	gitops "github.com/vieitesss/nexo/internal/git"
	"github.com/vieitesss/nexo/internal/lifecycle"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/remote"
	"github.com/vieitesss/nexo/internal/startup"
	"github.com/vieitesss/nexo/internal/tui"
	worktreeflow "github.com/vieitesss/nexo/internal/worktree"
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
	tab, err := tuiTab(invocation.Args)
	if err != nil {
		return err
	}
	var traceBuffer bytes.Buffer
	trace := invocation.StartupTrace
	if trace == nil {
		traceOutput := invocation.Stderr
		if strings.TrimSpace(os.Getenv("NEXO_STARTUP_TRACE")) != "" {
			traceOutput = &traceBuffer
		}
		trace = startup.New("tui", traceOutput)
	}
	trace.Mark("command-start")
	deps, err := tuiDependencies(ctx, invocation)
	if err != nil {
		return err
	}
	deps.AutoTab = len(invocation.Args) == 0
	deps.StartupTrace = trace
	runErr := runTabbedTUI(ctx, deps, tui.Tab(tab))
	if invocation.StartupTrace == nil && traceBuffer.Len() > 0 && invocation.Stderr != nil {
		_, _ = invocation.Stderr.Write(traceBuffer.Bytes())
	}
	return runErr
}

func tuiDependencies(ctx context.Context, invocation Invocation) (tui.Deps, error) {
	deps := tui.Deps{Theme: invocation.Config.TUI.EffectiveTheme(), Input: invocation.Stdin, Output: invocation.Stdout, CloneConfig: invocation.Config.Clone, ProjectPaths: invocation.Config.Paths, Home: os.Getenv("HOME")}
	deps.ConfigPath = invocation.ConfigPath
	if deps.ConfigPath == "" {
		deps.ConfigPath = config.DefaultPath()
	}
	var projectWarningsMu sync.Mutex
	var projectWarnings []string
	deps.ProjectWarnings = func() []string {
		projectWarningsMu.Lock()
		defer projectWarningsMu.Unlock()
		warnings := append([]string(nil), projectWarnings...)
		projectWarnings = nil
		return warnings
	}
	deps.ProjectSource = func(discoveryCtx context.Context) ([]picker.Place, error) {
		projectWarningsMu.Lock()
		projectWarnings = nil
		projectWarningsMu.Unlock()
		projectConfig := invocation.Config.ProjectsConfig
		if current, loadErr := config.LoadOptional(deps.ConfigPath); loadErr == nil {
			projectConfig = current.ProjectsConfig
		}
		discovery := projects.Discover(projectConfig, os.Getenv("HOME"))
		projectWarningsMu.Lock()
		projectWarnings = append(projectWarnings, discovery.Warnings...)
		projectWarningsMu.Unlock()
		if discoveryCtx.Err() != nil {
			return nil, discoveryCtx.Err()
		}
		return picker.ProjectPlaces(discovery.Candidates), nil
	}
	deps.AddProjectPath = func(addCtx context.Context, parent string) error {
		if err := addCtx.Err(); err != nil {
			return err
		}
		configPath := deps.ConfigPath
		_, _, err := config.AddProjectPath(configPath, parent, os.Getenv("HOME"))
		return err
	}
	if lister, ok := invocation.Adapter.(capabilities.ContainerLister); ok {
		// Container discovery is a source command. It must not delay the first
		// frame; action-time proxy checks below refresh the small identity set.
		deps.ContainerSource = lister
	}
	if source, ok := invocation.Adapter.(capabilities.AgentSource); ok {
		deps.AgentSource = source
		cfg := invocation.Config.Agents.WithDefaults()
		deps.AgentRefreshInterval = time.Duration(cfg.PollInterval) * time.Second
	}
	if invocation.Runner != nil {
		deps.GitLoader = picker.NewGitContextLoader(invocation.Runner)
		deps.PullRequestLoader = picker.NewGitHubPullRequestLoader(invocation.Runner)
	}
	deps.RemoteCommands = remotePlaceCommands(ctx, invocation)
	deps.RemoteMachines = append([]string(nil), invocation.Config.Remote.ConfiguredMachines()...)
	deps.CloneProject = func(cloneCtx context.Context, request cloneflow.Request) error {
		_, err := cloneflow.Clone(cloneCtx, cloneflow.Options{
			Runner:     invocation.Runner,
			Paths:      invocation.Config.Paths,
			ConfigPath: deps.ConfigPath,
			Home:       deps.Home,
		}, request)
		return err
	}
	deps.CreateDirectory = func(createCtx context.Context, path string) error {
		if err := createCtx.Err(); err != nil {
			return err
		}
		return destination.CreateDirectory(path)
	}
	deps.ExtractReturnBranch = func(extractCtx context.Context, repo string) (string, error) {
		if invocation.Runner == nil {
			return "", errors.New("worktree extraction requires a command runner")
		}
		return lifecycle.SuggestedReturnBranch(extractCtx, repo, gitops.New(invocation.Runner))
	}
	deps.ExtractWorktree = func(extractCtx context.Context, repo, destination, returnBranch string) (capabilities.Worktree, error) {
		extractor, ok := invocation.Adapter.(capabilities.WorktreeExtractor)
		if !ok {
			return capabilities.Worktree{}, fmt.Errorf("backend %q does not support extracting worktrees", invocation.Selection.Kind)
		}
		operation, err := worktreeflow.Extract(extractCtx, worktreeflow.Options{
			Extractor: extractor,
			Paths:     deps.ProjectPaths,
			Home:      deps.Home,
		}, worktreeflow.Request{
			Repo:         repo,
			Destination:  destination,
			ReturnBranch: returnBranch,
		})
		if err != nil {
			return capabilities.Worktree{}, err
		}
		return operation.Worktree, nil
	}
	deleteTransaction := newDeleteTransaction(invocation)
	deps.DeleteFingerprint = func(_ context.Context, path string) (string, error) {
		return deletion.Fingerprint(deletion.OSFileSystem{}, path)
	}
	deps.DeletePreflight = func(deleteCtx context.Context, path, identity string) (deletion.Plan, error) {
		return deleteTransaction.Preflight(deleteCtx, path, identity)
	}
	deps.DeleteProjectPlan = func(deleteCtx context.Context, plan deletion.Plan, permanent, force bool) error {
		return deleteTransaction.Execute(deleteCtx, plan, force, permanent)
	}

	deps.OpenProject = func(openCtx context.Context, place picker.Place) error {
		opener, ok := invocation.Adapter.(capabilities.ProjectOpener)
		if !ok {
			return fmt.Errorf("backend %q does not support opening projects", invocation.Selection.Kind)
		}
		_, err := opener.OpenProject(openCtx, place.Path, capabilities.OpenOptions{})
		return err
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
		lister, ok := invocation.Adapter.(capabilities.ContainerLister)
		if !ok {
			return errors.New("container close requires a lister to classify targets")
		}
		containers, err := lister.ListContainers(actionCtx)
		if err != nil {
			return fmt.Errorf("classify close targets: %w", err)
		}
		localProxyIDs := make(map[string]bool, len(containers))
		for _, container := range containers {
			if container.Proxy {
				localProxyIDs[container.Machine+"\x00"+container.ID] = true
			}
		}
		localIDs := make([]string, 0, len(refs))
		remoteGroups := make([]containerTargetGroup, 0)
		for _, ref := range refs {
			if localProxyIDs[ref.Machine+"\x00"+ref.ID] {
				localIDs = append(localIDs, ref.ID)
				continue
			}
			index := -1
			for i := range remoteGroups {
				if remoteGroups[i].machine == ref.Machine {
					index = i
					break
				}
			}
			if index < 0 {
				remoteGroups = append(remoteGroups, containerTargetGroup{machine: ref.Machine})
				index = len(remoteGroups) - 1
			}
			remoteGroups[index].ids = append(remoteGroups[index].ids, ref.ID)
		}
		if len(localIDs) > 0 {
			actions, resolveErr := (localContainerResolver{adapter: invocation.Adapter}).ResolveContainerActions(actionCtx, picker.LocalMachine())
			if resolveErr != nil {
				return resolveErr
			}
			for _, id := range localIDs {
				if err := actions.KillContainer(actionCtx, id); err != nil {
					return err
				}
			}
		}
		for _, group := range remoteGroups {
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
	remoteOrchestrator := newRemoteOrchestrator(invocation, resolver)
	deps.NewRemoteContainer = func(actionCtx context.Context, target tui.RemoteTarget, name string) error {
		return remoteOrchestrator.New(actionCtx, remote.MachineResult{Machine: target.Machine, Kind: remote.Kind(target.Backend)}, name)
	}
	deps.FocusAgent = func(focusCtx context.Context, ref tui.AgentRef) error {
		if ref.Machine != "" && ref.Machine != picker.LocalMachine() {
			return remoteOrchestrator.FocusAgent(focusCtx, remote.MachineResult{
				Machine: ref.Machine,
				Kind:    remote.Kind(ref.Backend),
			}, ref.Agent)
		}
		focuser, ok := invocation.Adapter.(capabilities.PaneFocuser)
		if !ok {
			return errors.New("selected backend does not support focusing panes")
		}
		return focuser.FocusPane(focusCtx, ref.Agent.PaneID)
	}
	return deps, nil
}

// tuiTab validates the optional opening tab. The model owns the no-argument
// default because only it can apply the rule to the asynchronously refreshed
// agent snapshot without a duplicate source read.
func tuiTab(args []string) (string, error) {
	if len(args) > 1 {
		return "", UsageError{Err: fmt.Errorf("usage: nexo tui [agents|containers|projects]")}
	}
	if len(args) == 0 {
		return "", nil
	}
	switch args[0] {
	case "agents", "containers", "projects":
		return args[0], nil
	default:
		return "", UsageError{Err: fmt.Errorf("unknown tui tab %q", args[0])}
	}
}

package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/install"
	"github.com/vieitesss/nexo/internal/progress"
	upgradeversion "github.com/vieitesss/nexo/internal/upgrade"
)

type upgradeCommand struct{}

func init() {
	registerBuiltin(func() Command { return upgradeCommand{} })
}

func (upgradeCommand) Name() string { return "upgrade" }

func (upgradeCommand) Help() Help {
	return Help{
		Summary:     "Install a release or nightly on every machine.",
		Usage:       "nexo upgrade [flags] [VERSION]",
		Description: "Builds the requested revision on this machine and atomically installs it here and on every configured remote. Without a version it installs the latest release.",
		Flags: []FlagHelp{
			{Long: "--version", Value: "VERSION", Description: "Install a specific release tag, such as v1.2.3."},
			{Long: "--nightly", Description: "Install the current main branch instead of a release."},
			{Long: "--machine", Value: "ALIAS", Description: "Limit the upgrade to this remote; repeat for several."},
			{Long: "--local-only", Description: "Upgrade only this machine."},
			{Long: "--dry-run", Description: "Show the targets and versions without installing."},
		},
		Examples: []string{"nexo upgrade", "nexo upgrade --nightly --dry-run", "nexo update --machine devbox v1.2.3"},
		Aliases:  []string{"update"},
	}
}

// upgradeMachineTimeout bounds one machine's probe, build, and copy so an
// unreachable or stalled target cannot hold back the other result lines.
const upgradeMachineTimeout = 15 * time.Minute

func (upgradeCommand) Run(ctx context.Context, invocation Invocation) error {
	if invocation.Stdout == nil {
		return errors.New("upgrade requires stdout")
	}
	options, err := parseUpgradeArgs(invocation.Args)
	if err != nil {
		return UsageError{Err: err}
	}
	requested, err := upgradeversion.Resolve(ctx, invocation.Runner, upgradeversion.VersionRequest{
		Version: options.version,
		Nightly: options.nightly,
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return upgradeCancelled()
		}
		return err
	}
	machines, err := upgradeMachines(invocation.Config.Remote, options)
	if err != nil {
		return UsageError{Err: err}
	}
	if interactiveTerminal() {
		return runUpgradeInteractive(ctx, invocation, machines, requested, options.dryRun)
	}
	return runUpgradePlain(ctx, invocation, machines, requested, options.dryRun)
}

func runUpgradePlain(ctx context.Context, invocation Invocation, machines []upgradeMachine, requested upgradeversion.Version, dryRun bool) error {
	results := runUpgradeMachines(ctx, invocation, machines, requested, dryRun, nil)
	for _, result := range results {
		if _, err := fmt.Fprintln(invocation.Stdout, result.line()); err != nil {
			return err
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return upgradeCancelled()
	}
	return upgradeOutcome(results)
}

// upgradeOutcome aggregates finished workers; cancellation wins over failure.
func upgradeOutcome(results []upgradeResult) error {
	failed := 0
	for _, result := range results {
		if result.cancelled {
			return upgradeCancelled()
		}
		if result.err != nil {
			failed++
		}
	}
	return upgradeFailure(failed)
}

func upgradeFailure(failed int) error {
	if failed > 0 {
		return fmt.Errorf("upgrade failed on %d machine(s)", failed)
	}
	return nil
}

func upgradeCancelled() error {
	return errors.New("upgrade cancelled")
}

func runUpgradeInteractive(ctx context.Context, invocation Invocation, machines []upgradeMachine, requested upgradeversion.Version, dryRun bool) error {
	operationCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	names := make([]string, len(machines))
	for index, machine := range machines {
		names[index] = machine.Name
	}
	header := "Upgrade " + requested.Label()
	if dryRun {
		header += " (dry-run)"
	}
	model := progress.NewModel(header, names)
	model.SetCancel(cancel)
	program := tea.NewProgram(model, tea.WithInput(invocation.Stdin), tea.WithOutput(invocation.Stdout))

	// Keep a copy of every event so a failed renderer or input can still
	// produce the complete final table after the workers have joined. Send is
	// a no-op once the program has stopped.
	var eventsMu sync.Mutex
	events := make([]progress.Event, 0, len(machines)*4)
	report := func(event progress.Event) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
		program.Send(progress.EventMsg{Event: event})
	}
	workersDone := make(chan []upgradeResult, 1)
	go func() {
		results := runUpgradeMachines(operationCtx, invocation, machines, requested, dryRun, report)
		program.Send(progress.CompleteMsg{})
		workersDone <- results
	}()

	returned, programErr := program.Run()
	// The program can stop before the workers do; cancel and join them so no
	// build or SSH action outlives the command.
	cancel()
	results := <-workersDone

	finalModel := model
	if rendered, ok := returned.(progress.Model); ok {
		finalModel = rendered
	}
	eventsMu.Lock()
	for _, event := range events {
		finalModel.Apply(event)
	}
	eventsMu.Unlock()
	if output := finalModel.FinalView(); output != "" {
		if _, err := fmt.Fprint(invocation.Stdout, output); err != nil {
			return err
		}
	}

	if errors.Is(programErr, tea.ErrInterrupted) || errors.Is(ctx.Err(), context.Canceled) {
		return upgradeCancelled()
	}
	if programErr != nil {
		return fmt.Errorf("run upgrade progress: %w", programErr)
	}
	return upgradeOutcome(results)
}

type upgradeOptions struct {
	version   string
	nightly   bool
	localOnly bool
	dryRun    bool
	machines  []string
}

func parseUpgradeArgs(args []string) (upgradeOptions, error) {
	var options upgradeOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--nightly":
			options.nightly = true
		case arg == "--local-only":
			options.localOnly = true
		case arg == "--dry-run":
			options.dryRun = true
		case arg == "--version":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return upgradeOptions{}, errors.New("--version requires a value")
			}
			index++
			options.version = args[index]
		case strings.HasPrefix(arg, "--version="):
			options.version = strings.TrimPrefix(arg, "--version=")
			if options.version == "" {
				return upgradeOptions{}, errors.New("--version requires a value")
			}
		case arg == "--machine":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return upgradeOptions{}, errors.New("--machine requires a value")
			}
			index++
			options.machines = append(options.machines, args[index])
		case strings.HasPrefix(arg, "--machine="):
			machine := strings.TrimPrefix(arg, "--machine=")
			if strings.TrimSpace(machine) == "" {
				return upgradeOptions{}, errors.New("--machine requires a value")
			}
			options.machines = append(options.machines, machine)
		case strings.HasPrefix(arg, "-"):
			return upgradeOptions{}, fmt.Errorf("unknown upgrade option %q", arg)
		default:
			if options.version != "" {
				return upgradeOptions{}, fmt.Errorf("unexpected argument %q", arg)
			}
			options.version = arg
		}
	}
	if options.nightly && options.version != "" {
		return upgradeOptions{}, errors.New("--nightly cannot be combined with a release version")
	}
	if options.localOnly && len(options.machines) > 0 {
		return upgradeOptions{}, errors.New("--local-only cannot be combined with --machine")
	}
	return options, nil
}

type upgradeMachine struct {
	Name   string
	Remote bool
}

func upgradeMachines(remote config.RemoteConfig, options upgradeOptions) ([]upgradeMachine, error) {
	local := upgradeMachine{Name: "local"}
	configured := remote.ConfiguredMachines()
	if options.localOnly {
		return []upgradeMachine{local}, nil
	}
	if len(options.machines) == 0 {
		machines := []upgradeMachine{local}
		for _, machine := range configured {
			machines = append(machines, upgradeMachine{Name: machine, Remote: true})
		}
		return machines, nil
	}
	machines := make([]upgradeMachine, 0, len(options.machines))
	seen := make(map[string]bool, len(options.machines))
	for _, requested := range options.machines {
		requested = strings.TrimSpace(requested)
		if strings.EqualFold(requested, "local") || strings.EqualFold(requested, containerstate.LocalMachine()) {
			if !seen["local"] {
				machines = append(machines, local)
				seen["local"] = true
			}
			continue
		}
		var match string
		for _, machine := range configured {
			if strings.EqualFold(strings.TrimSpace(machine), requested) {
				match = machine
				break
			}
		}
		if match == "" {
			return nil, fmt.Errorf("machine %q is not configured", requested)
		}
		key := strings.ToLower(strings.TrimSpace(match))
		if !seen[key] {
			machines = append(machines, upgradeMachine{Name: match, Remote: true})
			seen[key] = true
		}
	}
	return machines, nil
}

type upgradeResult struct {
	machine   string
	old       string
	new       string
	skipped   bool
	dryRun    bool
	cancelled bool
	err       error
}

func runUpgradeMachines(ctx context.Context, invocation Invocation, machines []upgradeMachine, requested upgradeversion.Version, dryRun bool, report func(progress.Event)) []upgradeResult {
	results := make([]upgradeResult, len(machines))
	var wait sync.WaitGroup
	for index, machine := range machines {
		wait.Add(1)
		go func(index int, machine upgradeMachine) {
			defer wait.Done()
			results[index] = runUpgradeMachine(ctx, invocation, machine, requested, dryRun, report)
		}(index, machine)
	}
	wait.Wait()
	return results
}

func runUpgradeMachine(parent context.Context, invocation Invocation, machine upgradeMachine, requested upgradeversion.Version, dryRun bool, report func(progress.Event)) (result upgradeResult) {
	started := time.Now()
	currentStep := progress.StepProbing
	notify := func(event progress.Event) {
		if report != nil {
			event.Machine = machine.Name
			report(event)
		}
	}
	result = upgradeResult{machine: machine.Name, old: "unknown"}
	notify(progress.Event{State: progress.StateRunning, Step: currentStep})
	defer func() {
		if result.err != nil && errors.Is(parent.Err(), context.Canceled) {
			result.cancelled = true
		}
		state := progress.StateDone
		if result.cancelled {
			state = progress.StateCancelled
		} else if result.err != nil {
			state = progress.StateFailed
		} else if result.skipped {
			state = progress.StateSkipped
		}
		err := result.err
		if result.cancelled {
			err = nil
		}
		notify(progress.Event{State: state, Step: currentStep, Old: result.old, New: result.new, Err: err, At: time.Now(), Elapsed: time.Since(started)})
	}()
	ctx, cancel := context.WithTimeout(parent, upgradeMachineTimeout)
	defer cancel()
	target := install.NewLocalTarget(invocation.Runner)
	if machine.Remote {
		target = install.NewRemoteTarget(invocation.Runner, machine.Name, invocation.Config.Remote.SSHConfig)
	}
	if errors.Is(parent.Err(), context.Canceled) {
		result.err = parent.Err()
		result.cancelled = true
		return result
	}
	current, err := install.Detect(ctx, target)
	if err != nil {
		result.err = err
		return result
	}
	result.old, err = install.ReadVersion(ctx, target, current.Path)
	if err != nil {
		result.err = err
		return result
	}
	if alreadyOnTarget(result.old, requested) {
		result.new = result.old
		result.skipped = true
		return result
	}
	if dryRun {
		result.new = requested.Label()
		result.dryRun = true
		return result
	}
	updated, err := install.Install(ctx, target, install.Request{
		Ref:   requested.Ref,
		Local: &install.LocalBuilder{Runner: invocation.Runner},
		Progress: func(step install.ProgressStep) {
			currentStep = upgradeProgressStep(step)
			notify(progress.Event{State: progress.StateRunning, Step: currentStep, Old: result.old})
		},
	})
	if err != nil {
		result.err = err
		return result
	}
	result.new = updated.Version
	if !requested.Nightly && !alreadyOnTarget(result.new, requested) {
		result.err = fmt.Errorf("installed %s, want %s", result.new, requested.Release)
	}
	return result
}

// upgradeProgressStep maps an install phase onto the progress vocabulary. The
// two packages cannot import each other, so an unmapped phase is a programming
// error and must fail loudly rather than render silently.
func upgradeProgressStep(step install.ProgressStep) progress.Step {
	switch step {
	case install.ProgressBuilding:
		return progress.StepBuilding
	case install.ProgressCopying:
		return progress.StepCopying
	case install.ProgressInstalling:
		return progress.StepInstalling
	default:
		panic(fmt.Sprintf("upgrade: unknown install progress step %q", step))
	}
}

func alreadyOnTarget(current string, requested upgradeversion.Version) bool {
	current = strings.TrimSpace(current)
	if requested.Nightly {
		return sameRevision(current, requested.Ref)
	}
	return current == requested.Release || strings.TrimPrefix(current, "v") == strings.TrimPrefix(requested.Release, "v")
}

// sameRevision accepts either an exact commit or the twelve-character suffix
// Go embeds in the module pseudo-version of a `go install repo@commit` build.
func sameRevision(current, commit string) bool {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return false
	}
	if current == commit {
		return true
	}
	short := commit
	if len(short) > 12 {
		short = short[:12]
	}
	return strings.HasSuffix(current, short)
}

func (r upgradeResult) line() string {
	if r.cancelled {
		return fmt.Sprintf("%s: cancelled", r.machine)
	}
	if r.err != nil {
		return fmt.Sprintf("%s: %s -> error: %v", r.machine, r.old, r.err)
	}
	if r.skipped {
		return fmt.Sprintf("%s: %s (skipped; already on target)", r.machine, r.old)
	}
	if r.dryRun {
		return fmt.Sprintf("%s: %s -> %s (dry-run)", r.machine, r.old, r.new)
	}
	return fmt.Sprintf("%s: %s -> %s", r.machine, r.old, r.new)
}

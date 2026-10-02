package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/install"
	upgradeversion "github.com/vieitesss/nexo/internal/upgrade"
)

type upgradeCommand struct{ name string }

func init() {
	registerBuiltin(func() Command { return upgradeCommand{name: "upgrade"} })
	registerBuiltin(func() Command { return upgradeCommand{name: "update"} })
}

func (c upgradeCommand) Name() string { return c.name }

func (c upgradeCommand) Run(ctx context.Context, invocation Invocation) error {
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
		return err
	}
	machines, err := upgradeMachines(invocation.Config.Remote, options)
	if err != nil {
		return UsageError{Err: err}
	}
	results := runUpgradeMachines(ctx, invocation, machines, requested, options.dryRun)
	failed := 0
	for _, result := range results {
		if _, err := fmt.Fprintln(invocation.Stdout, result.line()); err != nil {
			return err
		}
		if result.err != nil {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("upgrade failed on %d machine(s)", failed)
	}
	return nil
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
	machine string
	old     string
	new     string
	skipped bool
	dryRun  bool
	err     error
}

func runUpgradeMachines(ctx context.Context, invocation Invocation, machines []upgradeMachine, requested upgradeversion.Version, dryRun bool) []upgradeResult {
	results := make([]upgradeResult, len(machines))
	var wait sync.WaitGroup
	for index, machine := range machines {
		wait.Add(1)
		go func(index int, machine upgradeMachine) {
			defer wait.Done()
			results[index] = runUpgradeMachine(ctx, invocation, machine, requested, dryRun)
		}(index, machine)
	}
	wait.Wait()
	return results
}

func runUpgradeMachine(ctx context.Context, invocation Invocation, machine upgradeMachine, requested upgradeversion.Version, dryRun bool) upgradeResult {
	target := install.NewLocalTarget(invocation.Runner)
	if machine.Remote {
		target = install.NewRemoteTarget(invocation.Runner, machine.Name, invocation.Config.Remote.SSHConfig)
	}
	result := upgradeResult{machine: machine.Name, old: "unknown"}
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
		result.new = requested.Release
		result.dryRun = true
		return result
	}
	if requested.Nightly && (current.Method == install.MethodBinary || current.Method == install.MethodHomebrew) {
		err = buildAndCopy(ctx, invocation, target, current)
	} else {
		_, err = install.Install(ctx, target, install.Request{Version: requested.Ref, Nightly: requested.Nightly})
	}
	if err != nil {
		result.err = err
		return result
	}
	updated, err := install.Detect(ctx, target)
	if err != nil {
		result.err = err
		return result
	}
	result.new, err = install.ReadVersion(ctx, target, updated.Path)
	if err != nil {
		result.err = err
		return result
	}
	if !requested.Nightly && !alreadyOnTarget(result.new, requested) {
		result.err = fmt.Errorf("installed %s, want %s", result.new, requested.Release)
	}
	return result
}

func alreadyOnTarget(current string, requested upgradeversion.Version) bool {
	current = strings.TrimSpace(current)
	if requested.Nightly {
		return current == "main"
	}
	return current == requested.Release || strings.TrimPrefix(current, "v") == strings.TrimPrefix(requested.Release, "v")
}

func buildAndCopy(ctx context.Context, invocation Invocation, target install.Target, current install.Installation) error {
	if invocation.Runner == nil {
		return errors.New("nightly fallback requires a command runner")
	}
	directory, err := os.MkdirTemp("", ".nexo-nightly-build-*")
	if err != nil {
		return fmt.Errorf("create nightly build path: %w", err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	path := filepath.Join(directory, "nexo")
	build := fmt.Sprintf("set -eu; GOOS=%s GOARCH=%s GOPRIVATE=github.com/vieitesss/nexo GOBIN=%s go install github.com/vieitesss/nexo/cmd/nexo@main", shellQuote(current.Platform.OS), shellQuote(current.Platform.Arch), shellQuote(directory))
	if _, err := invocation.Runner.Run(ctx, "sh", "-c", build); err != nil {
		return fmt.Errorf("build nightly nexo for %s: %w", current.Platform, err)
	}
	if err := install.CopyAtomic(ctx, target, path, current.Path); err != nil {
		return err
	}
	return nil
}

func (r upgradeResult) line() string {
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

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

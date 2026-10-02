package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/proxyidentity"
	"github.com/vieitesss/nexo/internal/runner"
)

// ListingConfig controls the remote machines' listing protocol.
type ListingConfig struct {
	Backend   backend.Kind
	SSHConfig string
	Timeout   time.Duration
	Backends  map[string]string
}

// OperationPolicy owns kind-aware remote operations. Backend capabilities
// handle local opening and proxying; SSH-backed creation and native Herdr
// opening stay here because they are properties of the remote machine.
type OperationPolicy struct {
	Proxy    capabilities.RemoteProxy
	Resolver capabilities.ContainerActionResolver
	Stdout   io.Writer
}

// Orchestrator is the remote-machine module. Listing, normalization, and
// operation selection cross this small interface; SSH framing, decoders, and
// remote operation construction remain inside this package.
type Orchestrator struct {
	listing machineListing
	policy  OperationPolicy
}

// NewOrchestrator creates the remote-machine module. The runner and listing
// configuration stay behind this interface; callers do not construct or
// configure listing or operation implementations themselves.
func NewOrchestrator(commandRunner runner.CommandRunner, config ListingConfig, policy OperationPolicy) Orchestrator {
	return Orchestrator{listing: newMachineListing(commandRunner, config), policy: policy}
}

// ListMachineWithAgents returns one normalized result with optional agent data.
func (o Orchestrator) ListMachineWithAgents(ctx context.Context, machine string) MachineResult {
	return o.listing.listMachine(ctx, machine, true)
}

// List returns one normalized result per configured machine.
func (o Orchestrator) List(ctx context.Context, machines []string) []MachineResult {
	return o.listing.list(ctx, machines, false)
}

// ListWithAgents returns one normalized result per configured machine with
// optional agent data from the same framed SSH round trip.
func (o Orchestrator) ListWithAgents(ctx context.Context, machines []string) []MachineResult {
	return o.listing.list(ctx, machines, true)
}

// Machine probes one machine when a runner is available. A missing runner is
// useful for callers that already have a configured remote target: its
// explicit kind is still resolved without inventing a second remote probe.
func (o Orchestrator) Machine(ctx context.Context, machine string) (MachineResult, error) {
	machine = strings.TrimSpace(machine)
	if o.listing.runner == nil {
		kind := Kind(o.listing.config.Backends[proxyidentity.NormalizeMachineName(machine)])
		if kind != KindTmux && kind != KindHerdr {
			kind = KindTmux
		}
		return MachineResult{Machine: machine, Kind: kind}, nil
	}
	result := o.listing.listMachine(ctx, machine, false)
	if result.Error != "" {
		return result, fmt.Errorf("list remote machine %q: %s", machine, result.Error)
	}
	return result, nil
}

// Open selects the native Herdr path or a tmux proxy from the machine result.
// Tmux names are canonicalized at this seam, after remote kind detection.
func (o Orchestrator) Open(ctx context.Context, result MachineResult, container string) error {
	return o.policy.open(ctx, o.listing.runner, o.listing.config.Backend, result.Machine, result.Kind, container)
}

// New creates and opens a container using the operation selected by the
// resolved remote kind. The policy owns both remote creation and the follow-up
// open, so callers cannot accidentally diverge between the two paths.
func (o Orchestrator) New(ctx context.Context, result MachineResult, name string) error {
	return o.policy.create(ctx, o.listing.runner, o.listing.config.Backend, o.listing.config.SSHConfig, result, name)
}

// FocusAgent focuses a remote pane, then opens the containing remote
// container. The agent's opaque pane identity never crosses into the local
// backend adapter.
func (o Orchestrator) FocusAgent(ctx context.Context, result MachineResult, agent capabilities.Agent) error {
	container := strings.TrimSpace(agent.Container)
	if container == "" {
		container = strings.TrimSpace(agent.ContainerID)
	}
	if container == "" {
		return errors.New("remote agent has no container name")
	}
	if err := o.policy.validateKind(o.listing.config.Backend, result.Machine, result.Kind, "focus"); err != nil {
		return err
	}
	if err := FocusRemotePane(ctx, o.listing.runner, o.listing.config.SSHConfig, result.Machine, result.Kind, agent.PaneID); err != nil {
		return err
	}
	return o.Open(ctx, result, container)
}

func (p OperationPolicy) validateKind(local backend.Kind, machine string, kind Kind, operation string) error {
	switch kind {
	case KindTmux:
		if local != backend.Tmux && local != backend.Herdr {
			return fmt.Errorf("unsupported: %s runs tmux; use tmux or Herdr to %s it", machine, operation)
		}
	case KindHerdr:
		if local != backend.Herdr {
			return fmt.Errorf("unsupported: %s runs Herdr; use Herdr to %s it", machine, operation)
		}
	default:
		return fmt.Errorf("remote machine %q has an unknown multiplexer kind", machine)
	}
	return nil
}

func (p OperationPolicy) open(ctx context.Context, commandRunner runner.CommandRunner, local backend.Kind, machine string, kind Kind, container string) error {
	if err := p.validateKind(local, machine, kind, "open"); err != nil {
		return err
	}
	return p.openValidated(ctx, commandRunner, local, machine, kind, container)
}

func (p OperationPolicy) openValidated(ctx context.Context, commandRunner runner.CommandRunner, local backend.Kind, machine string, kind Kind, container string) error {
	if kind == KindHerdr {
		return p.openHerdr(ctx, commandRunner, machine, container)
	}
	container = tmux.CanonicalContainerName(container)
	if p.Resolver != nil {
		actions, err := p.Resolver.ResolveContainerActions(ctx, machine)
		if err != nil {
			return err
		}
		return actions.OpenContainer(ctx, container)
	}
	if p.Proxy == nil {
		return fmt.Errorf("backend %q does not support remote proxies", local)
	}
	return p.Proxy.Proxy(ctx, machine, container)
}

func (p OperationPolicy) create(ctx context.Context, commandRunner runner.CommandRunner, local backend.Kind, sshConfig string, result MachineResult, name string) error {
	if err := p.validateKind(local, result.Machine, result.Kind, "open"); err != nil {
		return err
	}
	switch result.Kind {
	case KindHerdr:
		if err := createHerdrWorkspace(ctx, commandRunner, sshConfig, result.Machine, name); err != nil {
			return err
		}
		return p.openValidated(ctx, commandRunner, local, result.Machine, result.Kind, name)
	case KindTmux:
		container, err := createTmuxContainer(ctx, commandRunner, sshConfig, result.Machine, name)
		if err != nil {
			return err
		}
		return p.openValidated(ctx, commandRunner, local, result.Machine, result.Kind, container)
	default:
		return fmt.Errorf("remote machine %q has an unknown multiplexer kind", result.Machine)
	}
}

func (p OperationPolicy) openHerdr(ctx context.Context, commandRunner runner.CommandRunner, machine, container string) error {
	if commandRunner == nil {
		return errors.New("opening a Herdr remote requires a command runner")
	}
	output, err := commandRunner.Run(ctx, "herdr", "machine", "list", "--json")
	if err != nil {
		return fmt.Errorf("list Herdr machines: %w", err)
	}
	if !herdrMachineSaved(output, machine) {
		if _, err := commandRunner.Run(ctx, "herdr", "machine", "add", machine, "--label", machine); err != nil {
			return fmt.Errorf("save Herdr machine %q: %w", machine, err)
		}
		if p.Stdout != nil {
			if _, err := fmt.Fprintf(p.Stdout, "saved Herdr machine %s\n", machine); err != nil {
				return err
			}
		}
	}
	if p.Stdout == nil {
		return nil
	}
	_, err = fmt.Fprintf(p.Stdout, "%s is available under %s in Herdr's sidebar\n", container, machine)
	return err
}

func herdrMachineSaved(output []byte, machine string) bool {
	var rows []struct {
		Target  string `json:"target"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(output, &rows); err != nil {
		return false
	}
	for _, row := range rows {
		enabled := row.Enabled == nil || *row.Enabled
		if enabled && strings.TrimSpace(row.Target) == machine {
			return true
		}
	}
	return false
}

func createTmuxContainer(ctx context.Context, commandRunner runner.CommandRunner, sshConfig, machine, name string) (string, error) {
	if err := tmux.ValidateProxyIdentity(machine, name); err != nil {
		return "", err
	}
	if commandRunner == nil {
		return "", errors.New("remote new requires a command runner")
	}
	args := []string{"-o", "BatchMode=yes"}
	if sshConfig = strings.TrimSpace(sshConfig); sshConfig != "" {
		args = append(args, "-F", sshConfig)
	}
	args = append(args, machine, "tmux -u new-session -d -P -F '#{session_name}' -s "+shellQuote(tmux.EscapeSessionName(name)))
	output, err := commandRunner.Run(ctx, "ssh", args...)
	if err != nil {
		if text := strings.TrimSpace(string(output)); text != "" {
			return "", fmt.Errorf("create remote container: %s", text)
		}
		return "", fmt.Errorf("create remote container on %s: %w", machine, err)
	}
	canonical := strings.TrimSpace(string(output))
	if newline := strings.IndexByte(canonical, '\n'); newline >= 0 {
		canonical = canonical[:newline]
	}
	if canonical == "" {
		canonical = tmux.CanonicalContainerName(name)
	}
	return canonical, nil
}

func createHerdrWorkspace(ctx context.Context, commandRunner runner.CommandRunner, sshConfig, machine, name string) error {
	if commandRunner == nil {
		return errors.New("remote new requires a command runner")
	}
	args := []string{"-o", "BatchMode=yes"}
	if sshConfig = strings.TrimSpace(sshConfig); sshConfig != "" {
		args = append(args, "-F", sshConfig)
	}
	args = append(args, machine, "herdr workspace create --label "+shellQuote(name))
	output, err := commandRunner.Run(ctx, "ssh", args...)
	if err != nil {
		if text := strings.TrimSpace(string(output)); text != "" {
			return fmt.Errorf("create remote Herdr container: %s", text)
		}
		return fmt.Errorf("create remote Herdr container on %s: %w", machine, err)
	}
	return nil
}

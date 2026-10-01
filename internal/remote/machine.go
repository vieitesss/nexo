package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/runner"
)

// OperationPolicy is the adapter seam for remote-machine operations. The
// policy owns kind-aware selection; callbacks retain only backend-specific
// work that cannot be expressed by a capability interface.
type OperationPolicy struct {
	Backend   backend.Kind
	Runner    runner.CommandRunner
	SSHConfig string
	Proxy     capabilities.RemoteProxy
	Resolver  capabilities.ContainerActionResolver

	OpenHerdr   func(context.Context, string, string) error
	CreateTmux  func(context.Context, string, string) (string, error)
	CreateHerdr func(context.Context, string, string) error
}

// Orchestrator is the remote-machine module. Listing, normalization, and
// operation selection cross this small interface; SSH framing and decoders
// remain inside Lister and operation adapters remain behind OperationPolicy.
type Orchestrator struct {
	lister Lister
	policy OperationPolicy
}

// NewOrchestrator creates the remote-machine module at the listing seam.
func NewOrchestrator(lister Lister, policy OperationPolicy) Orchestrator {
	return Orchestrator{lister: lister, policy: policy}
}

// ListMachine returns one normalized result for a machine.
func (o Orchestrator) ListMachine(ctx context.Context, machine string) MachineResult {
	return o.lister.ListMachine(ctx, machine)
}

// ListMachineWithAgents returns one normalized result with optional agent data.
func (o Orchestrator) ListMachineWithAgents(ctx context.Context, machine string) MachineResult {
	return o.lister.ListMachineWithAgents(ctx, machine)
}

// List returns one normalized result per configured machine.
func (o Orchestrator) List(ctx context.Context, machines []string) []MachineResult {
	return o.lister.List(ctx, machines)
}

// ListWithAgents returns one normalized result per configured machine with
// optional agent data from the same framed SSH round trip.
func (o Orchestrator) ListWithAgents(ctx context.Context, machines []string) []MachineResult {
	return o.lister.ListWithAgents(ctx, machines)
}

// Machine probes one machine when a runner is available. A missing runner is
// useful for callers that already have a configured remote target: its
// explicit kind is still resolved without inventing a second remote probe.
func (o Orchestrator) Machine(ctx context.Context, machine string) (MachineResult, error) {
	machine = strings.TrimSpace(machine)
	if o.lister.Runner == nil {
		kind := Kind(o.lister.Backends[normalizeMachine(machine)])
		if kind != KindTmux && kind != KindHerdr {
			kind = KindTmux
		}
		return MachineResult{Machine: machine, Kind: kind}, nil
	}
	result := o.lister.ListMachine(ctx, machine)
	if result.Error != "" {
		return result, fmt.Errorf("list remote machine %q: %s", machine, result.Error)
	}
	return result, nil
}

// Open selects the native Herdr path or a tmux proxy from the machine result.
// Tmux names are canonicalized at this seam, after remote kind detection.
func (o Orchestrator) Open(ctx context.Context, result MachineResult, container string) error {
	return o.policy.open(ctx, result.Machine, result.Kind, container)
}

// New creates and opens a container using the operation selected by the
// resolved remote kind. Creation adapters return only backend-native output;
// the policy owns which adapter is valid and how its result is opened.
func (o Orchestrator) New(ctx context.Context, result MachineResult, name string) error {
	return o.policy.create(ctx, result, name)
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
	if err := o.policy.validateKind(result.Machine, result.Kind, "focus"); err != nil {
		return err
	}
	if err := FocusRemotePane(ctx, o.policy.Runner, o.policy.SSHConfig, result.Machine, result.Kind, agent.PaneID); err != nil {
		return err
	}
	return o.Open(ctx, result, container)
}

func (p OperationPolicy) validateKind(machine string, kind Kind, operation string) error {
	switch kind {
	case KindTmux:
		if p.Backend != backend.Tmux && p.Backend != backend.Herdr {
			return fmt.Errorf("unsupported: %s runs tmux; use tmux or Herdr to %s it", machine, operation)
		}
	case KindHerdr:
		if p.Backend != backend.Herdr {
			return fmt.Errorf("unsupported: %s runs Herdr; use Herdr to %s it", machine, operation)
		}
	default:
		return fmt.Errorf("remote machine %q has an unknown multiplexer kind", machine)
	}
	return nil
}

func (p OperationPolicy) open(ctx context.Context, machine string, kind Kind, container string) error {
	if err := p.validateKind(machine, kind, "open"); err != nil {
		return err
	}
	if kind == KindHerdr {
		if p.OpenHerdr == nil {
			return errors.New("opening a Herdr remote requires a command runner")
		}
		return p.OpenHerdr(ctx, machine, container)
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
		return fmt.Errorf("backend %q does not support remote proxies", p.Backend)
	}
	return p.Proxy.Proxy(ctx, machine, container)
}

func (p OperationPolicy) create(ctx context.Context, result MachineResult, name string) error {
	if err := p.validateKind(result.Machine, result.Kind, "open"); err != nil {
		// The creation error historically referred to opening Herdr. Keep that
		// user-facing policy while making operation selection local to this module.
		if result.Kind == KindHerdr && p.Backend != backend.Herdr {
			return fmt.Errorf("unsupported: %s runs Herdr; use Herdr to open it", result.Machine)
		}
		return err
	}
	switch result.Kind {
	case KindHerdr:
		if p.CreateHerdr == nil {
			return errors.New("remote new requires a command runner")
		}
		if err := p.CreateHerdr(ctx, result.Machine, name); err != nil {
			return err
		}
		return p.open(ctx, result.Machine, result.Kind, name)
	case KindTmux:
		if p.CreateTmux == nil {
			return errors.New("remote new requires a command runner")
		}
		container, err := p.CreateTmux(ctx, result.Machine, name)
		if err != nil {
			return err
		}
		return p.open(ctx, result.Machine, result.Kind, container)
	default:
		return fmt.Errorf("remote machine %q has an unknown multiplexer kind", result.Machine)
	}
}

func normalizeMachine(machine string) string {
	return strings.ToLower(strings.TrimSpace(machine))
}

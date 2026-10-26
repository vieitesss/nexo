package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/backend/herdr"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/proxy"
)

type remoteAttachOptions struct {
	fromEnv    bool
	initial    string
	machine    string
	remoteKind string
}

func parseRemoteAttachOptions(args []string) (remoteAttachOptions, error) {
	if len(args) == 0 || args[0] != "attach" {
		return remoteAttachOptions{}, errors.New("remote attach requires [--initial C] -- MACHINE or [--initial C] --from-env")
	}
	var options remoteAttachOptions
	seenInitial, seenMachine, seenFromEnv, seenRemoteKind := false, false, false, false
	for index := 1; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--initial":
			if seenInitial || index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return remoteAttachOptions{}, errors.New("remote attach requires --initial C")
			}
			options.initial = args[index+1]
			seenInitial = true
			index++
		case "--from-env":
			if seenFromEnv || seenMachine {
				return remoteAttachOptions{}, errors.New("remote attach accepts either MACHINE or --from-env")
			}
			seenFromEnv = true
			options.fromEnv = true
		case "--remote-kind":
			if seenRemoteKind || index+1 >= len(args) {
				return remoteAttachOptions{}, errors.New("remote attach requires --remote-kind tmux|herdr")
			}
			kind := strings.TrimSpace(args[index+1])
			if kind != proxy.RemoteKindTmux && kind != proxy.RemoteKindHerdr {
				return remoteAttachOptions{}, errors.New("remote attach requires --remote-kind tmux|herdr")
			}
			options.remoteKind = kind
			seenRemoteKind = true
			index++
		case "--":
			if seenFromEnv || seenMachine || index+1 != len(args)-1 || strings.TrimSpace(args[index+1]) == "" {
				return remoteAttachOptions{}, errors.New("remote attach requires [--initial C] -- MACHINE or [--initial C] --from-env")
			}
			options.machine = args[index+1]
			seenMachine = true
			index++
		default:
			if strings.HasPrefix(arg, "-") || seenFromEnv || seenMachine {
				return remoteAttachOptions{}, errors.New("remote attach requires [--initial C] -- MACHINE or [--initial C] --from-env")
			}
			options.machine = arg
			seenMachine = true
		}
	}
	if seenFromEnv == seenMachine {
		return remoteAttachOptions{}, errors.New("remote attach requires [--initial C] -- MACHINE or [--initial C] --from-env")
	}
	return options, nil
}

func runRemoteAttach(ctx context.Context, invocation Invocation) error {
	options, err := parseRemoteAttachOptions(invocation.Args)
	fromEnv := options.fromEnv
	if err != nil {
		return UsageError{Err: err}
	}
	if fromEnv && invocation.Selection.Kind != backend.Herdr {
		return errors.New("remote attach --from-env requires the Herdr backend")
	}
	// The attach command runs in the pane of a proxy nexo opened, so the local
	// backend must be one nexo creates proxies with. Every such backend owns a
	// terminal that can host the remote client.
	if _, ok := invocation.Adapter.(capabilities.RemoteProxy); !ok {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	if options.remoteKind == proxy.RemoteKindHerdr && fromEnv {
		return errors.New("remote attach --from-env attaches to tmux and cannot name a remote Herdr machine")
	}
	var machine string
	if fromEnv {
		machine = os.Getenv("NEXO_PROXY_MACHINE")
		if err := herdr.ValidateProxyIdentity(machine, ""); err != nil {
			return err
		}
	} else {
		machine = strings.TrimSpace(options.machine)
		if err := invocation.Config.Remote.ValidateMachine(machine); err != nil {
			return err
		}
	}
	if invocation.Selection.Kind == backend.Herdr && !invocation.Config.Remote.IsMachineConfigured(machine) {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	transport := proxy.NewCommandTransport(invocation.Config.Remote, string(invocation.Selection.Kind), options.remoteKind)
	initial, maximum := invocation.Config.Remote.ReconnectDelays()
	input := invocation.Stdin
	if input == nil {
		input = os.Stdin
	}
	attachErr := proxy.RunReconnect(ctx, &firstAttachTransport{transport: transport, initial: options.initial}, nil, input, machine, "", initial, maximum)
	if fromEnv {
		return finishHerdrProxyAttach(invocation, attachErr)
	}
	return attachErr
}

type firstAttachTransport struct {
	transport proxy.Transport
	initial   string
	used      bool
}

func (t *firstAttachTransport) Attach(ctx context.Context, machine, _ string) error {
	if t.used || t.initial == "" {
		return t.transport.Attach(ctx, machine, "")
	}
	t.used = true
	return t.transport.Attach(ctx, machine, t.initial)
}

func finishHerdrProxyAttach(invocation Invocation, attachErr error) error {
	closer, ok := invocation.Adapter.(capabilities.ProxyWorkspaceCloser)
	if !ok {
		return errors.New("herdr backend does not support closing the proxy workspace")
	}
	paneID := strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
	if paneID == "" {
		return errors.New("HERDR_PANE_ID is required to close the Herdr proxy workspace")
	}
	cleanupErr := closer.CloseProxyWorkspace(context.Background(), paneID)
	if attachErr != nil && cleanupErr != nil {
		return errors.Join(attachErr, cleanupErr)
	}
	if attachErr != nil {
		return attachErr
	}
	return cleanupErr
}

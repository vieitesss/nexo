package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/backend/herdr"
	"github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/proxy"
)

func runRemoteAttach(ctx context.Context, invocation Invocation) error {
	fromEnv := len(invocation.Args) == 2 && invocation.Args[1] == "--from-env"
	if !fromEnv && len(invocation.Args) != 3 {
		return errors.New("remote attach requires MACHINE CONTAINER")
	}
	if fromEnv {
		if invocation.Selection.Kind != backend.Herdr {
			return errors.New("remote attach --from-env requires the Herdr backend")
		}
	} else if invocation.Selection.Kind != backend.Tmux {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	if _, ok := invocation.Adapter.(capabilities.RemoteProxy); !ok {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	var machine, container string
	if fromEnv {
		machine = os.Getenv("NEXO_PROXY_MACHINE")
		container = os.Getenv("NEXO_PROXY_CONTAINER")
		if err := herdr.ValidateProxyIdentity(machine, container); err != nil {
			return err
		}
	} else {
		machine = strings.TrimSpace(invocation.Args[1])
		container = strings.TrimSpace(invocation.Args[2])
		if err := invocation.Config.Remote.ValidateMachine(machine); err != nil {
			return err
		}
		if container == "" {
			return errors.New("remote attach requires a container")
		}
	}
	if invocation.Selection.Kind == backend.Herdr && !invocation.Config.Remote.IsMachineConfigured(machine) {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	transport := tmux.NewCommandTransport(invocation.Config.Remote)
	if invocation.Config.Remote.EffectiveTransport() != "ssh" {
		attachErr := transport.Attach(ctx, machine, container)
		if fromEnv {
			return finishHerdrProxyAttach(invocation, attachErr)
		}
		return attachErr
	}
	initial, maximum := invocation.Config.Remote.ReconnectDelays()
	input := invocation.Stdin
	if input == nil {
		input = os.Stdin
	}
	attachErr := proxy.RunReconnect(ctx, transport, nil, input, machine, container, initial, maximum)
	if fromEnv {
		return finishHerdrProxyAttach(invocation, attachErr)
	}
	return attachErr
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

package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/vieitesss/nexo/internal/proxyidentity"
)

func runRemoteOpen(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) != 2 {
		return UsageError{Err: errors.New("remote open requires MACHINE/CONTAINER")}
	}
	target, err := proxyidentity.ParseTarget(invocation.Args[1])
	if err != nil {
		return UsageError{Err: fmt.Errorf("remote open requires MACHINE/CONTAINER: %w", err)}
	}
	machine, container := target.Machine, target.Container
	if err := invocation.Config.Remote.ValidateMachine(machine); err != nil {
		return err
	}
	orchestrator := newRemoteOrchestrator(invocation, invocation.ContainerResolver)
	result, err := orchestrator.Machine(ctx, machine)
	if err != nil {
		return err
	}
	return orchestrator.Open(ctx, result, container)
}

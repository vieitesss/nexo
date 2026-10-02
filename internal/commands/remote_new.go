package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

func runRemoteNew(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) < 2 || len(invocation.Args) > 3 {
		return errors.New("remote new requires MACHINE [NAME]")
	}
	machine := strings.TrimSpace(invocation.Args[1])
	if err := invocation.Config.Remote.ValidateMachine(machine); err != nil {
		return err
	}
	name, err := remoteContainerName(invocation)
	if err != nil {
		return err
	}
	orchestrator := newRemoteOrchestrator(invocation, nil)
	result, err := orchestrator.Machine(ctx, machine)
	if err != nil {
		return err
	}
	return orchestrator.New(ctx, result, name)
}

func remoteContainerName(invocation Invocation) (string, error) {
	if len(invocation.Args) == 3 {
		name := strings.TrimSpace(invocation.Args[2])
		if name == "" {
			return "", errors.New("remote new requires a non-empty name")
		}
		if strings.ContainsAny(name, "\r\n") {
			return "", errors.New("remote container name must be one line")
		}
		return name, nil
	}
	if invocation.Stdout == nil {
		return "", errors.New("remote new requires a name when stdout is unavailable")
	}
	_, _ = fmt.Fprint(invocation.Stdout, "Remote container name: ")
	input := invocation.Stdin
	if input == nil {
		input = os.Stdin
	}
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", fmt.Errorf("read remote container name: %w", err)
	}
	name := strings.TrimSpace(line)
	if name == "" {
		return "", errors.New("remote new requires a non-empty name")
	}
	if strings.ContainsAny(name, "\r\n") {
		return "", errors.New("remote container name must be one line")
	}
	return name, nil
}

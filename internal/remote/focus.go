package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vieitesss/nexo/internal/runner"
)

// FocusRemotePane selects one exact pane on a remote multiplexer. It never
// sends the remote pane ID to the local backend, whose pane namespace can
// collide with the remote machine's IDs.
func FocusRemotePane(ctx context.Context, commandRunner runner.CommandRunner, sshConfig, machine string, kind Kind, paneID string) error {
	if commandRunner == nil {
		return errors.New("remote pane focus requires a command runner")
	}
	machine = strings.TrimSpace(machine)
	paneID = strings.TrimSpace(paneID)
	if machine == "" {
		return errors.New("remote pane focus requires a machine")
	}
	if paneID == "" {
		return errors.New("remote pane focus requires a pane id")
	}

	var commands [][]string
	switch kind {
	case KindTmux:
		commands = [][]string{
			{"tmux", "select-window", "-t", paneID},
			{"tmux", "select-pane", "-t", paneID},
		}
	case KindHerdr:
		// Herdr's agent focus command runs against the server on the remote
		// machine when invoked there, so SSH is the machine-aware focus path.
		commands = [][]string{{"herdr", "agent", "focus", paneID}}
	default:
		return fmt.Errorf("remote machine %q has an unknown multiplexer kind", machine)
	}

	for _, command := range commands {
		output, err := commandRunner.Run(ctx, "ssh", SSHCommandArgs(sshConfig, machine, ShellCommand(command...))...)
		if err == nil {
			continue
		}
		if text := strings.TrimSpace(string(output)); text != "" {
			return fmt.Errorf("focus remote pane %s on %s: %s", paneID, machine, text)
		}
		return fmt.Errorf("focus remote pane %s on %s: %w", paneID, machine, err)
	}
	return nil
}

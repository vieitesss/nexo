package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
)

var (
	_ capabilities.ContainerLister  = (*Backend)(nil)
	_ capabilities.ContainerRenamer = (*Backend)(nil)
	_ capabilities.ContainerKiller  = (*Backend)(nil)
)

// ListContainers lists tmux sessions and records each session's active pane.
// Session IDs, rather than names, are used for subsequent actions so a rename
// cannot make a focused row target a different session.
func (b *Backend) ListContainers(ctx context.Context) ([]capabilities.Container, error) {
	if b == nil || b.Runner == nil {
		return nil, errors.New("tmux container lister requires a command runner")
	}
	output, err := b.run(ctx, "list-sessions", "-F", "#{session_id}\t#{session_name}\t#{session_activity}\t#{@nexo-proxy}")
	if err != nil {
		if isNoServer(err) {
			return []capabilities.Container{}, nil
		}
		return nil, err
	}
	machine := localMachine()
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	containers := make([]capabilities.Container, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
			continue
		}
		proxy := len(fields) >= 4 && strings.TrimSpace(fields[3]) != ""
		if proxy {
			continue
		}
		var activity *time.Time
		if len(fields) >= 3 {
			if seconds, parseErr := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64); parseErr == nil && seconds > 0 {
				value := time.Unix(seconds, 0)
				activity = &value
			}
		}
		paneID, paneErr := b.activePane(ctx, fields[0])
		if paneErr != nil {
			return nil, paneErr
		}
		containers = append(containers, capabilities.Container{
			ID:           fields[0],
			Name:         fields[1],
			Machine:      machine,
			ActivePaneID: paneID,
			Activity:     activity,
			Proxy:        proxy,
		})
	}
	return containers, nil
}

func (b *Backend) activePane(ctx context.Context, sessionID string) (string, error) {
	output, err := b.run(ctx, "list-panes", "-t", sessionID, "-F", "#{pane_id}\t#{pane_active}")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(fields) == 2 && fields[1] == "1" {
			return fields[0], nil
		}
	}
	return "", nil
}

// RenameContainer renames a tmux session by its stable session ID.
func (b *Backend) RenameContainer(ctx context.Context, id, name string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux container renamer requires a command runner")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("tmux container renamer requires an id")
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("tmux container renamer requires a name")
	}
	_, err := b.run(ctx, "rename-session", "-t", id, name)
	return err
}

// KillContainer kills one selected tmux session.
func (b *Backend) KillContainer(ctx context.Context, id string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux container killer requires a command runner")
	}
	if strings.TrimSpace(id) == "" {
		return nil
	}
	_, err := b.run(ctx, "kill-session", "-t", id)
	return err
}

// KillContainers kills each selected tmux session. The picker calls this once
// for the complete selection, so confirmation is not repeated per session.
func (b *Backend) KillContainers(ctx context.Context, ids []string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux container killer requires a command runner")
	}
	current, err := b.currentContainer(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == current {
			continue
		}
		if err := b.KillContainer(ctx, id); err != nil {
			return err
		}
	}
	if current != "" {
		for _, id := range ids {
			if id == current {
				return b.KillContainer(ctx, id)
			}
		}
	}
	return nil
}

func (b *Backend) currentContainer(ctx context.Context) (string, error) {
	pane := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if pane == "" {
		return "", nil
	}
	output, err := b.run(ctx, "display-message", "-p", "-t", pane, "#{session_id}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func localMachine() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "local"
	}
	return name
}

func isNoServer(err error) bool {
	var tmuxErr *Error
	if !errors.As(err, &tmuxErr) {
		return false
	}
	message := strings.ToLower(fmt.Sprint(tmuxErr.Stderr))
	return strings.Contains(message, "no server running") ||
		strings.Contains(message, "failed to connect to server") ||
		strings.Contains(message, "no such file or directory")
}

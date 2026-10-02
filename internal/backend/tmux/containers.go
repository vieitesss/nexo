package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/pathpolicy"
	"github.com/vieitesss/nexo/internal/proxyidentity"
)

var (
	_ capabilities.ContainerLister     = (*Backend)(nil)
	_ capabilities.ContainerRenamer    = (*Backend)(nil)
	_ capabilities.ContainerKiller     = (*Backend)(nil)
	_ capabilities.ContainerHistory    = (*Backend)(nil)
	_ capabilities.CurrentContainer    = (*Backend)(nil)
	_ capabilities.CurrentProxyMachine = (*Backend)(nil)
	_ capabilities.ProjectUseChecker   = (*Backend)(nil)
)

// ListContainers lists tmux sessions and records each session's active pane.
// Session IDs, rather than names, are used for subsequent actions so a rename
// cannot make a focused row target a different session. A session's #{pane_id}
// is the active pane of its current window, so one tmux call covers every
// session.
func (b *Backend) ListContainers(ctx context.Context) ([]capabilities.Container, error) {
	if b == nil || b.Runner == nil {
		return nil, errors.New("tmux container lister requires a command runner")
	}
	output, err := b.run(ctx, "list-sessions", "-F", "#{session_id}\t#{session_name}\t#{session_activity}\t#{@nexo-proxy}\t#{pane_id}")
	if err != nil {
		if isNoServer(err) {
			return []capabilities.Container{}, nil
		}
		return nil, err
	}
	machine := containerstate.LocalMachine()
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	containers := make([]capabilities.Container, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
			continue
		}
		proxyMachine, proxyContainer, proxy := "", "", false
		if len(fields) >= 4 && strings.TrimSpace(fields[3]) != "" {
			proxyMachine, proxyContainer, proxy = b.proxyTarget(fields[3])
			// A session carrying nexo metadata is a proxy even if its identity
			// is stale or malformed; do not leak it into the local list.
			if !proxy {
				continue
			}
		} else if identity, ok := proxyidentity.ParseValue(fields[1]); ok && b.isRemoteMachine(identity.Machine) {
			proxyMachine, proxyContainer, proxy = proxyidentity.NormalizeMachineName(identity.Machine), CanonicalContainerName(identity.Container), true
		}
		var activity *time.Time
		if len(fields) >= 3 {
			if seconds, parseErr := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64); parseErr == nil && seconds > 0 {
				value := time.Unix(seconds, 0)
				activity = &value
			}
		}
		paneID := ""
		if len(fields) >= 5 {
			paneID = strings.TrimSpace(fields[4])
		}
		name, containerMachine := fields[1], machine
		if proxy {
			name, containerMachine = proxyContainer, proxyMachine
		}
		containers = append(containers, capabilities.Container{
			ID:           fields[0],
			Name:         name,
			Machine:      containerMachine,
			ActivePaneID: paneID,
			Activity:     activity,
			Proxy:        proxy,
		})
	}
	return containers, nil
}

func (b *Backend) proxyTarget(value string) (string, string, bool) {
	identity, ok := proxyidentity.ParseValue(value)
	if !ok || !b.isRemoteMachine(identity.Machine) {
		return "", "", false
	}
	return proxyidentity.NormalizeMachineName(identity.Machine), CanonicalContainerName(identity.Container), true
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

// ProjectActive reports whether any live tmux pane currently has the project
// or one of its descendants as its working directory. It is deliberately
// read-only so deletion can reject active projects before mutation.
func (b *Backend) ProjectActive(ctx context.Context, path string) (bool, error) {
	if b == nil || b.Runner == nil {
		return false, errors.New("tmux active-project check requires a command runner")
	}
	// The pane ID listing is authoritative: a pane ID cannot contain a
	// newline, so it cannot be forged by a crafted working directory. The path
	// records must account for exactly these panes.
	idsOutput, err := b.run(ctx, "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		if isNoServer(err) {
			return false, nil
		}
		return false, err
	}
	// q: uses tmux's shell escaping for ordinary special bytes. The decoder
	// validates the pane ID and absolute path fields so any ambiguous control
	// byte fails closed instead of claiming that the project is idle.
	output, err := b.run(ctx, "list-panes", "-a", "-F", "#{pane_id}\t#{q:pane_current_path}")
	if err != nil {
		if isNoServer(err) {
			return false, nil
		}
		return false, err
	}
	livePanes, idErr := decodePaneIDList(string(idsOutput))
	if idErr != nil {
		return false, idErr
	}
	path = filepath.Clean(path)
	panePaths, decodeErr := decodePanePathRecords(string(output))
	if decodeErr != nil {
		return false, decodeErr
	}
	if matchErr := matchPanePathRecords(livePanes, panePaths); matchErr != nil {
		return false, matchErr
	}
	for _, panePath := range panePaths {
		if pathpolicy.Contains(path, panePath, isSymlink(path), pathpolicy.OSResolver{}) {
			return true, nil
		}
	}
	return false, nil
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func decodePanePathRecords(output string) (map[string]string, error) {
	parts := strings.Split(output, "\n")
	paths := make(map[string]string, len(parts))
	for index, record := range parts {
		if index == len(parts)-1 && record == "" {
			continue
		}
		fields := strings.SplitN(record, "\t", 2)
		if len(fields) != 2 || !validPaneID(fields[0]) || fields[1] == "" {
			return nil, errors.New("tmux pane path output contains an invalid record")
		}
		if _, seen := paths[fields[0]]; seen {
			return nil, errors.New("tmux pane path output contains a duplicate pane")
		}
		path, err := decodeTmuxQuotedPath(fields[1])
		if err != nil {
			return nil, err
		}
		// q: does not frame control bytes: a tab stays in the field and a
		// newline starts another record. Refuse both rather than guessing where
		// a record ends and treating a malformed observation as idle.
		if path == "" || strings.ContainsAny(path, "\n\t") || !filepath.IsAbs(path) {
			return nil, errors.New("tmux pane path output contains an unsafe path")
		}
		paths[fields[0]] = path
	}
	return paths, nil
}

// decodePaneIDList parses the authoritative pane ID listing. Every record must
// already be one complete valid ID because a pane ID cannot contain a newline.
func decodePaneIDList(output string) (map[string]struct{}, error) {
	records := strings.Split(output, "\n")
	ids := make(map[string]struct{}, len(records))
	for index, record := range records {
		if index == len(records)-1 && record == "" {
			continue
		}
		if !validPaneID(record) {
			return nil, errors.New("tmux pane id output contains an invalid record")
		}
		if _, seen := ids[record]; seen {
			return nil, errors.New("tmux pane id output contains a duplicate pane")
		}
		ids[record] = struct{}{}
	}
	return ids, nil
}

// matchPanePathRecords requires the path observations to account for exactly
// the live panes. A pane that appeared, disappeared or changed its ID between
// the two listings cannot be attributed to a path, so the check fails closed
// and the deletion stays retryable.
func matchPanePathRecords(livePanes map[string]struct{}, records map[string]string) error {
	if len(livePanes) != len(records) {
		return errors.New("tmux pane path output does not match the live pane list")
	}
	for id := range records {
		if _, ok := livePanes[id]; !ok {
			return errors.New("tmux pane path output does not match the live pane list")
		}
	}
	return nil
}

func validPaneID(value string) bool {
	if len(value) < 2 || value[0] != '%' {
		return false
	}
	for _, digit := range value[1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

// decodeTmuxQuotedPath decodes the actual q: format emitted by tmux. It is
// shell-style escaping without the single-quoted wrapper: spaces, slashes and
// quotes are each represented by a backslash followed by the original byte.
func decodeTmuxQuotedPath(record string) (string, error) {
	var decoded strings.Builder
	for index := 0; index < len(record); index++ {
		if record[index] != '\\' {
			decoded.WriteByte(record[index])
			continue
		}
		if index+1 == len(record) {
			return "", errors.New("tmux pane path has an incomplete escape")
		}
		index++
		decoded.WriteByte(record[index])
	}
	return decoded.String(), nil
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

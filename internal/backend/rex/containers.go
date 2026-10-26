package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
)

var (
	_ capabilities.ContainerLister      = (*Backend)(nil)
	_ capabilities.ContainerOpener      = (*Backend)(nil)
	_ capabilities.ContainerRenamer     = (*Backend)(nil)
	_ capabilities.ContainerKiller      = (*Backend)(nil)
	_ capabilities.ContainerBatchKiller = (*Backend)(nil)
	_ capabilities.ContainerHistory     = (*Backend)(nil)
	_ capabilities.CurrentContainer     = (*Backend)(nil)
)

// sessionInfo is one native Rex session. The label is mutable display data;
// the id is the identity every operation must keep.
type sessionInfo struct {
	ID    string `json:"session_id"`
	Label string `json:"label"`
}

type sessionList struct {
	Sessions []sessionInfo `json:"sessions"`
}

// sessionView is the session-local view needed for the focused block. Focus
// here is the session's typing target, not proof that an app displays it.
type sessionView struct {
	SessionID      string       `json:"session_id"`
	Label          string       `json:"label"`
	ActiveWindowID string       `json:"active_window_id"`
	Windows        []viewWindow `json:"windows"`
}

type viewWindow struct {
	WindowID       string `json:"window_id"`
	Label          string `json:"label"`
	FocusedBlockID string `json:"focused_block_id"`
}

func (v sessionView) focusedBlock() string {
	for _, window := range v.Windows {
		if window.WindowID == v.ActiveWindowID && window.FocusedBlockID != "" {
			return window.FocusedBlockID
		}
	}
	for _, window := range v.Windows {
		if window.FocusedBlockID != "" {
			return window.FocusedBlockID
		}
	}
	return ""
}

type block struct {
	ID        string `json:"block_id"`
	Label     string `json:"label"`
	Flavor    string `json:"flavor"`
	HostID    string `json:"host_id"`
	Placement string `json:"placement"`
	WindowID  string `json:"window_id"`
}

type blockList struct {
	Blocks []block `json:"blocks"`
}

type sessionIDInput struct {
	SessionID string `json:"session_id"`
}

type labelInput struct {
	SessionID string `json:"session_id"`
	Label     string `json:"label"`
}

type createInput struct {
	Label          string         `json:"label,omitempty"`
	InitialWindows []createWindow `json:"initial_windows,omitempty"`
}

type createWindow struct {
	Layout createLayout `json:"layout"`
}

type createLayout struct {
	Block createBlock `json:"block"`
}

type createBlock struct {
	Flavor  string        `json:"flavor"`
	Options createOptions `json:"options"`
}

type createOptions struct {
	CWD string `json:"cwd,omitempty"`
}

type createResult struct {
	SessionID      string `json:"session_id"`
	InitialWindows []struct {
		WindowID string   `json:"window_id"`
		BlockIDs []string `json:"block_ids"`
	} `json:"initial_windows"`
}

// sessions lists every native session. A null array and an empty array both
// mean "no sessions".
func (b *Backend) sessions(ctx context.Context) ([]sessionInfo, error) {
	output, err := b.api(ctx, "session.list", nil)
	if err != nil {
		return nil, err
	}
	var listed sessionList
	if err := json.Unmarshal(output, &listed); err != nil {
		return nil, fmt.Errorf("decode rex session.list: %w", err)
	}
	return listed.Sessions, nil
}

// findSession resolves one explicit native id. Labels are never consulted: a
// label is mutable presentation, so an unknown id is an error rather than a
// prompt to guess a similar session.
func (b *Backend) findSession(ctx context.Context, id string) (sessionInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return sessionInfo{}, errors.New("rex session id is empty")
	}
	sessions, err := b.sessions(ctx)
	if err != nil {
		return sessionInfo{}, err
	}
	for _, session := range sessions {
		if session.ID == id {
			return session, nil
		}
	}
	return sessionInfo{}, fmt.Errorf("rex session %q not found", id)
}

func (b *Backend) view(ctx context.Context, sessionID string) (sessionView, error) {
	output, err := b.api(ctx, "session.view", sessionIDInput{SessionID: sessionID})
	if err != nil {
		return sessionView{}, err
	}
	var view sessionView
	if err := json.Unmarshal(output, &view); err != nil {
		return sessionView{}, fmt.Errorf("decode rex session.view: %w", err)
	}
	return view, nil
}

// blocks lists every block of one session, including detached and floating
// blocks, because project-use safety cannot rely on the active window.
func (b *Backend) blocks(ctx context.Context, sessionID string) ([]block, error) {
	output, err := b.api(ctx, "session.list_blocks", sessionIDInput{SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	var listed blockList
	if err := json.Unmarshal(output, &listed); err != nil {
		return nil, fmt.Errorf("decode rex session.list_blocks: %w", err)
	}
	return listed.Blocks, nil
}

// ListContainers maps Rex sessions to nexo containers and reports each
// session's focused block for optional previews.
func (b *Backend) ListContainers(ctx context.Context) ([]capabilities.Container, error) {
	if b == nil || b.Runner == nil {
		return nil, errors.New("rex container lister requires a command runner")
	}
	sessions, err := b.sessions(ctx)
	if err != nil {
		return nil, err
	}
	machine := containerstate.LocalMachine()
	containers := make([]capabilities.Container, 0, len(sessions))
	for _, session := range sessions {
		containers = append(containers, capabilities.Container{
			ID:           session.ID,
			Name:         session.Label,
			Machine:      machine,
			ActivePaneID: b.focusedBlock(ctx, session.ID),
		})
	}
	return containers, nil
}

// focusedBlock tolerates a session disappearing between listing and viewing:
// the container is still real, it just has no preview target.
func (b *Backend) focusedBlock(ctx context.Context, sessionID string) string {
	view, err := b.view(ctx, sessionID)
	if err != nil {
		return ""
	}
	return view.focusedBlock()
}

// OpenContainer selects an existing session in the connected app. Detached
// blocks cannot be selected, so only the session is targeted.
func (b *Backend) OpenContainer(ctx context.Context, id string) error {
	if b == nil || b.Runner == nil {
		return errors.New("rex container opener requires a command runner")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("rex container opener requires an id")
	}
	session, err := b.findSession(ctx, id)
	if err != nil {
		return err
	}
	return b.focusAndRecord(ctx, session, false)
}

// RenameContainer renames one session by exact id. Rex trims labels and resets
// an empty one to a generated default, so an empty name is rejected here.
func (b *Backend) RenameContainer(ctx context.Context, id, name string) error {
	if b == nil || b.Runner == nil {
		return errors.New("rex container renamer requires a command runner")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("rex container renamer requires an id")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("rex container renamer requires a name")
	}
	_, err := b.api(ctx, "session.set_label", labelInput{SessionID: id, Label: name})
	return err
}

// KillContainer destroys one selected session and its blocks.
func (b *Backend) KillContainer(ctx context.Context, id string) error {
	if b == nil || b.Runner == nil {
		return errors.New("rex container killer requires a command runner")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	_, err := b.api(ctx, "session.destroy", sessionIDInput{SessionID: id})
	return err
}

// KillContainers destroys the confirmed selection. Rex has no atomic batch
// destroy, so ids are deduplicated and the caller's own session goes last.
func (b *Backend) KillContainers(ctx context.Context, ids []string) error {
	if b == nil || b.Runner == nil {
		return errors.New("rex container killer requires a command runner")
	}
	current, err := b.CurrentContainerID(ctx)
	if err != nil {
		return err
	}
	selected := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		selected = append(selected, id)
	}
	for _, id := range selected {
		if id == current {
			continue
		}
		if err := b.KillContainer(ctx, id); err != nil {
			return err
		}
	}
	for _, id := range selected {
		if id == current {
			return b.KillContainer(ctx, id)
		}
	}
	return nil
}

// store scopes container history to this Rex server run: native session ids
// are only unique within one server.
func (b *Backend) store(ctx context.Context) (containerstate.Store, error) {
	resolved, err := b.server(ctx)
	if err != nil {
		return containerstate.Store{}, err
	}
	return containerstate.New(b.stateDir, "rex", containerstate.LocalMachine(), resolved.namespace()), nil
}

func (b *Backend) recordContainer(ctx context.Context, entry containerstate.Entry) error {
	store, err := b.store(ctx)
	if err != nil {
		return err
	}
	return store.Record(entry)
}

// RecentContainers returns the recorded session identities of this server.
func (b *Backend) RecentContainers(ctx context.Context) ([]containerstate.Entry, error) {
	if b == nil || b.Runner == nil {
		return nil, errors.New("rex container history requires a command runner")
	}
	store, err := b.store(ctx)
	if err != nil {
		return nil, err
	}
	return store.Entries()
}

// focusAndRecord selects a session in the connected app and records it as the
// most recently used container. A no-focus open, or a host with no app, leaves
// history untouched because the user never saw the session.
func (b *Backend) focusAndRecord(ctx context.Context, session sessionInfo, noFocus bool) error {
	if noFocus {
		return nil
	}
	focused, err := b.selectSession(ctx, session.ID)
	if err != nil || !focused {
		return err
	}
	return b.recordContainer(ctx, containerstate.Entry{
		ID:      session.ID,
		Name:    session.Label,
		Machine: containerstate.LocalMachine(),
	})
}

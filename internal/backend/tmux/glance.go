package tmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/glance"
)

// glanceStateOption holds the whole glance ownership record for one tmux
// server as a single JSON value. Writing it is one atomic tmux option update,
// so another nexo invocation never observes an enabled index without its
// saved originals.
const glanceStateOption = "@nexo-glance-state"

// maxStatusRows is tmux's limit on the number of status lines.
const maxStatusRows = 5

// tmuxStatusFormat1Default is tmux's compiled-in default for the second status
// row, the pane-info row, before tmux 3.7. A slot still holding a compiled-in
// default is not owned by the user and may be reused; any other value at a
// slot is a custom row and is preserved.
const tmuxStatusFormat1Default = "#[align=centre]#{P:#{?pane_active,#[reverse],}#{pane_index}[#{pane_width}x#{pane_height}]#[default] }"

// tmuxStatusFormat1PaneListDefault and tmuxStatusFormat2SessionListDefault are
// tmux 3.7's compiled-in defaults for the second and third rows: the "P:" pane
// list and the "S:" session list.
const (
	tmuxStatusFormat1PaneListDefault    = "#[align=left]#{R: ,#{n:#{session_name}}}P: #[norange default]#[list=on align=#{status-justify}]#[list=left-marker]<#[list=right-marker]>#[list=on]#{P:#[range=pane|#{pane_id} #{E:pane-status-style}]#[push-default]#{T:window-pane-status-format}#[pop-default]#[norange list=on default]  ,#[range=pane|#{pane_id} list=focus #{?#{!=:#{E:pane-status-current-style},default},#{E:pane-status-current-style},#{E:pane-status-style}}]#[push-default]#{T:window-pane-current-status-format}#[pop-default]#[norange list=on default] }"
	tmuxStatusFormat2SessionListDefault = "#[align=left]#{R: ,#{n:#{session_name}}}S: #[norange default]#[list=on align=#{status-justify}]#[list=left-marker]<#[list=right-marker]>#[list=on]#{S:#[range=session|#{session_id} #{E:session-status-style}]#[push-default]#S#{session_alert}#[pop-default]#[norange list=on default]  ,#[range=session|#{session_id} list=focus #{?#{!=:#{E:session-status-current-style},default},#{E:session-status-current-style},#{E:session-status-style}}]#[push-default]#S*#{session_alert}#[pop-default]#[norange list=on default] }"
)

// GlanceAction is one user-bound glance command.
type GlanceAction string

const (
	GlanceOn     GlanceAction = "on"
	GlanceOff    GlanceAction = "off"
	GlanceToggle GlanceAction = "toggle"
)

// ParseGlanceAction validates the glance subcommand argument.
func ParseGlanceAction(value string) (GlanceAction, error) {
	switch GlanceAction(strings.TrimSpace(value)) {
	case GlanceOn:
		return GlanceOn, nil
	case GlanceOff:
		return GlanceOff, nil
	case GlanceToggle:
		return GlanceToggle, nil
	default:
		return "", fmt.Errorf("unknown glance action %q (want on, off or toggle)", value)
	}
}

// ConfigureGlance applies the shared glance configuration to this server.
func (s *Source) ConfigureGlance(cfg config.GlanceConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.glance = cfg.WithDefaults()
	s.glanceSynced = false
}

// Glance implements the user-bound `nexo tmux glance` command on this server.
func (s *Source) Glance(ctx context.Context, action GlanceAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.lockGlance(ctx)
	if err != nil {
		return err
	}
	defer release()
	return s.glanceLocked(ctx, action)
}

// lockGlance serializes a full ownership read/modify/write transaction against
// every other nexo process on this server. The in-process mutex only protects
// one Source; the file lock is what makes the watcher and independent CLI
// commands agree on who owns which row.
func (s *Source) lockGlance(ctx context.Context) (func(), error) {
	if s.server == "" {
		s.server = s.serverName(ctx)
	}
	return acquireFileLock(s.statePath("glance"), false)
}

func (s *Source) glanceLocked(ctx context.Context, action GlanceAction) error {
	state, err := s.readGlanceState(ctx)
	if err != nil {
		return err
	}
	switch action {
	case GlanceOn:
		return s.glanceOnLocked(ctx, state)
	case GlanceOff:
		return s.glanceOffLocked(ctx, state)
	case GlanceToggle:
		if state.Index > 0 {
			return s.glanceOffLocked(ctx, state)
		}
		return s.glanceOnLocked(ctx, state)
	default:
		return fmt.Errorf("unknown glance action %q", action)
	}
}

// glanceOption records an option's exact value and whether it existed at all,
// so restoring can distinguish absent from explicitly empty.
type glanceOption struct {
	Present bool   `json:"present"`
	Value   string `json:"value"`
}

// glanceSessionState records the server options a session overrode before
// glance changed them.
type glanceSessionState struct {
	Status glanceOption `json:"status"`
	Format glanceOption `json:"format"`
}

// glanceState is the durable ownership record for one tmux server.
type glanceState struct {
	Index    int                           `json:"index"`
	Status   glanceOption                  `json:"status"`
	Format   glanceOption                  `json:"format"`
	Row      string                        `json:"row"`
	Sessions map[string]glanceSessionState `json:"sessions,omitempty"`
}

func (s *Source) glanceOnLocked(ctx context.Context, state glanceState) error {
	if state.Index > 0 {
		// Already on: finish any interrupted activation and refresh the row.
		return s.refreshGlanceLocked(ctx, state)
	}
	return s.enableGlanceLocked(ctx)
}

// enableGlanceLocked allocates an unowned row, publishes the complete record,
// then applies it. Any failure after the record is published rolls the server
// back and clears the record, so a later on starts from a clean state.
func (s *Source) enableGlanceLocked(ctx context.Context) error {
	globalStatus, err := s.showOption(ctx, "status")
	if err != nil {
		return err
	}
	formats, err := s.globalStatusFormats(ctx)
	if err != nil {
		return err
	}
	refs, err := s.sessionRefs(ctx)
	if err != nil {
		return err
	}
	localStatus := make(map[string]glanceOption, len(refs))
	localFormats := make(map[string]map[int]bool, len(refs))
	for _, ref := range refs {
		localStatus[ref.ID], err = s.localStatus(ctx, ref.ID)
		if err != nil {
			return err
		}
		localFormats[ref.ID], err = s.localFormatPresence(ctx, ref.ID)
		if err != nil {
			return err
		}
	}
	index, err := allocateGlanceIndex(globalStatus, formats, localStatus, localFormats)
	if err != nil {
		return err
	}
	state := glanceState{
		Index:    index,
		Status:   glanceOption{Present: true, Value: globalStatus},
		Format:   formats[index],
		Sessions: make(map[string]glanceSessionState, len(refs)),
	}
	for _, ref := range refs {
		state.Sessions[ref.ID] = glanceSessionState{Status: localStatus[ref.ID]}
	}
	if err := s.writeGlanceState(ctx, state); err != nil {
		return err
	}
	agents, err := s.listAgents(ctx, false)
	if err != nil {
		return errors.Join(err, s.clearGlanceState(ctx))
	}
	if err := s.applyGlanceLocked(ctx, &state, agents); err != nil {
		return errors.Join(err, s.restoreGlanceLocked(ctx, state))
	}
	return nil
}

// refreshGlanceLocked re-applies an enabled row, raising status and repairing
// sessions as needed, so retrying on after an interrupted activation works.
func (s *Source) refreshGlanceLocked(ctx context.Context, state glanceState) error {
	agents, err := s.listAgents(ctx, false)
	if err != nil {
		return err
	}
	return s.applyGlanceLocked(ctx, &state, agents)
}

func (s *Source) glanceOffLocked(ctx context.Context, state glanceState) error {
	if state.Index <= 0 {
		return nil
	}
	return s.restoreGlanceLocked(ctx, state)
}

// reconcileGlanceLocked keeps the enabled row current. It is called by the
// watcher on every poll, so agent state changes reach tmux without another
// command. The caller holds s.mu; the file lock serializes other processes.
func (s *Source) reconcileGlanceLocked(ctx context.Context, agents []capabilities.Agent) error {
	release, err := s.lockGlance(ctx)
	if err != nil {
		return err
	}
	defer release()
	state, err := s.readGlanceState(ctx)
	if err != nil {
		return err
	}
	if state.Index <= 0 {
		return nil
	}
	return s.applyGlanceLocked(ctx, &state, agents)
}

// applyGlanceLocked brings tmux and the ownership record in line with the
// rendered row. It only writes options that actually need changing, so an idle
// watcher issues no set-option at all.
func (s *Source) applyGlanceLocked(ctx context.Context, state *glanceState, agents []capabilities.Agent) error {
	index := state.Index
	if index <= 0 {
		return nil
	}
	row := glance.Render(agents, s.glance.WithDefaults(), s.clientWidth(ctx), s.now())

	globalStatus, err := s.showOption(ctx, "status")
	if err != nil {
		return err
	}
	if statusRows(globalStatus) < index+1 {
		if _, err := s.runTmux(ctx, "set-option", "-g", "status", strconv.Itoa(index+1)); err != nil {
			return fmt.Errorf("raise tmux status rows: %w", err)
		}
	}

	changed := false
	// A fresh Source (a restarted watcher or a new CLI command) re-applies the
	// row once, then identical polls skip the write.
	if !s.glanceSynced || row != state.Row {
		if _, err := s.runTmux(ctx, "set-option", "-g", fmt.Sprintf("status-format[%d]", index), row); err != nil {
			return fmt.Errorf("update tmux glance row: %w", err)
		}
		state.Row = row
		changed = true
	}
	s.glanceSynced = true

	sessions, err := s.sessions(ctx, index)
	if err != nil {
		return err
	}
	present := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		present[session.ID] = struct{}{}
		saved := state.Sessions[session.ID]
		if statusRows(session.Status) < index+1 {
			// The session's own status value shadows the raised global one.
			if !saved.Status.Present {
				saved.Status, err = s.localStatus(ctx, session.ID)
				if err != nil {
					return err
				}
			}
			if _, err := s.runTmux(ctx, "set-option", "-t", session.ID, "status", strconv.Itoa(index+1)); err != nil {
				return fmt.Errorf("raise tmux status rows for session %s: %w", session.ID, err)
			}
		}
		if session.Format != row {
			// A session-local status-format shadows the global row.
			if !saved.Format.Present {
				saved.Format, err = s.localFormat(ctx, session.ID, index)
				if err != nil {
					return err
				}
			}
			if _, err := s.runTmux(ctx, "set-option", "-t", session.ID, fmt.Sprintf("status-format[%d]", index), row); err != nil {
				return fmt.Errorf("update tmux glance row for session %s: %w", session.ID, err)
			}
		}
		current, ok := state.Sessions[session.ID]
		if !ok || current != saved {
			if state.Sessions == nil {
				state.Sessions = make(map[string]glanceSessionState)
			}
			state.Sessions[session.ID] = saved
			changed = true
		}
	}
	for id := range state.Sessions {
		if _, ok := present[id]; !ok {
			delete(state.Sessions, id)
			changed = true
		}
	}
	if changed {
		return s.writeGlanceState(ctx, *state)
	}
	return nil
}

// restoreGlanceLocked puts every option glance changed back to its exact
// original value or absence, then clears the ownership record.
func (s *Source) restoreGlanceLocked(ctx context.Context, state glanceState) error {
	var failures []error
	if state.Status.Present {
		if _, err := s.runTmux(ctx, "set-option", "-g", "status", state.Status.Value); err != nil {
			failures = append(failures, fmt.Errorf("restore tmux status: %w", err))
		}
	} else if _, err := s.runTmux(ctx, "set-option", "-gu", "status"); err != nil {
		failures = append(failures, fmt.Errorf("restore tmux status: %w", err))
	}
	formatOption := fmt.Sprintf("status-format[%d]", state.Index)
	if state.Format.Present {
		if _, err := s.runTmux(ctx, "set-option", "-g", formatOption, state.Format.Value); err != nil {
			failures = append(failures, fmt.Errorf("restore tmux %s: %w", formatOption, err))
		}
	} else if _, err := s.runTmux(ctx, "set-option", "-gu", formatOption); err != nil {
		failures = append(failures, fmt.Errorf("restore tmux %s: %w", formatOption, err))
	}
	if len(state.Sessions) > 0 {
		refs, err := s.sessionRefs(ctx)
		if err != nil {
			failures = append(failures, err)
		} else {
			alive := make(map[string]struct{}, len(refs))
			for _, ref := range refs {
				alive[ref.ID] = struct{}{}
			}
			for id, saved := range state.Sessions {
				if _, ok := alive[id]; !ok {
					continue
				}
				if saved.Status.Present {
					if _, err := s.runTmux(ctx, "set-option", "-t", id, "status", saved.Status.Value); err != nil {
						failures = append(failures, fmt.Errorf("restore tmux session %s status: %w", id, err))
					}
				}
				if saved.Format.Present {
					if _, err := s.runTmux(ctx, "set-option", "-t", id, formatOption, saved.Format.Value); err != nil {
						failures = append(failures, fmt.Errorf("restore tmux session %s %s: %w", id, formatOption, err))
					}
				} else if _, err := s.runTmux(ctx, "set-option", "-t", id, "-u", formatOption); err != nil {
					failures = append(failures, fmt.Errorf("restore tmux session %s %s: %w", id, formatOption, err))
				}
			}
		}
	}
	s.glanceSynced = false
	if err := s.clearGlanceState(ctx); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// allocateGlanceIndex returns the lowest status row beyond every session's
// displayed rows whose status-format slot is not a custom row. A slot holding
// tmux's built-in default is not custom; an unset slot is free.
func allocateGlanceIndex(globalStatus string, formats map[int]glanceOption, localStatus map[string]glanceOption, localFormats map[string]map[int]bool) (int, error) {
	start := statusRows(globalStatus)
	if start < 1 {
		start = 1
	}
	for _, status := range localStatus {
		if status.Present {
			if rows := statusRows(status.Value); rows > start {
				start = rows
			}
		}
	}
	for index := start; index < maxStatusRows; index++ {
		if glanceSlotOwned(index, formats, localFormats) {
			continue
		}
		return index, nil
	}
	return 0, fmt.Errorf("tmux status already uses all %d rows", maxStatusRows)
}

func glanceSlotOwned(index int, formats map[int]glanceOption, localFormats map[string]map[int]bool) bool {
	if format, ok := formats[index]; ok && format.Present {
		if !isTmuxStatusFormatDefault(index, format.Value) {
			return true
		}
	}
	for _, presence := range localFormats {
		if presence[index] {
			return true
		}
	}
	return false
}

// isTmuxStatusFormatDefault reports whether value is a compiled-in default of
// some tmux version for a status-format index. Only the lowest rows have a
// default; every other set value is user-owned.
func isTmuxStatusFormatDefault(index int, value string) bool {
	switch index {
	case 1:
		return value == tmuxStatusFormat1Default || value == tmuxStatusFormat1PaneListDefault
	case 2:
		return value == tmuxStatusFormat2SessionListDefault
	default:
		return false
	}
}

// globalStatusFormats returns every global status-format slot with its exact
// value. Tmux always reports the built-in default slots.
func (s *Source) globalStatusFormats(ctx context.Context) (map[int]glanceOption, error) {
	output, err := s.runTmux(ctx, "show-options", "-g", "status-format")
	if err != nil {
		return nil, fmt.Errorf("read tmux status-format: %w", err)
	}
	formats := make(map[int]glanceOption)
	for _, index := range statusFormatIndices(output) {
		value, err := s.showOption(ctx, fmt.Sprintf("status-format[%d]", index))
		if err != nil {
			return nil, err
		}
		formats[index] = glanceOption{Present: true, Value: value}
	}
	return formats, nil
}

// statusFormatIndices extracts the configured indices from the tmux
// `show-options status-format` listing. An explicitly empty element is listed,
// so this distinguishes present from absent.
func statusFormatIndices(output []byte) []int {
	var indices []int
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "status-format[") {
			continue
		}
		rest := line[len("status-format["):]
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			continue
		}
		index, err := strconv.Atoi(rest[:end])
		if err != nil {
			continue
		}
		indices = append(indices, index)
	}
	return indices
}

// tmuxSession carries a session identity and the values a reconcile needs.
type tmuxSession struct {
	ID     string
	Name   string
	Status string
	Format string
}

// sessionRefs lists the server's sessions.
func (s *Source) sessionRefs(ctx context.Context) ([]tmuxSession, error) {
	output, err := s.runTmux(ctx, "list-sessions", "-F", "#{session_id}\t#{session_name}")
	if err != nil {
		return nil, fmt.Errorf("list tmux sessions: %w", err)
	}
	var sessions []tmuxSession
	for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		sessions = append(sessions, tmuxSession{ID: fields[0], Name: fields[1]})
	}
	return sessions, nil
}

// sessions lists each session's effective status and effective row value in
// one call, so reconcile does not probe per session.
func (s *Source) sessions(ctx context.Context, index int) ([]tmuxSession, error) {
	format := fmt.Sprintf("#{session_id}\t#{session_name}\t#{status}\t#{status-format[%d]}", index)
	output, err := s.runTmux(ctx, "list-sessions", "-F", format)
	if err != nil {
		return nil, fmt.Errorf("list tmux sessions: %w", err)
	}
	var sessions []tmuxSession
	for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 4)
		if len(fields) != 4 {
			continue
		}
		sessions = append(sessions, tmuxSession{ID: fields[0], Name: fields[1], Status: fields[2], Format: fields[3]})
	}
	return sessions, nil
}

// localStatus reads a session's own status option, absent when unset.
func (s *Source) localStatus(ctx context.Context, sessionID string) (glanceOption, error) {
	value, err := s.sessionOptionValue(ctx, sessionID, "status")
	if err != nil {
		return glanceOption{}, err
	}
	if value == "" {
		return glanceOption{}, nil
	}
	return glanceOption{Present: true, Value: value}, nil
}

// localFormatPresence reports which status-format slots a session sets itself.
func (s *Source) localFormatPresence(ctx context.Context, sessionID string) (map[int]bool, error) {
	output, err := s.runTmux(ctx, "show-options", "-t", sessionID, "status-format")
	if err != nil {
		return nil, fmt.Errorf("read tmux session %s status-format: %w", sessionID, err)
	}
	presence := make(map[int]bool)
	for _, index := range statusFormatIndices(output) {
		presence[index] = true
	}
	return presence, nil
}

// localFormat reads a session's own status-format slot, absent when unset.
func (s *Source) localFormat(ctx context.Context, sessionID string, index int) (glanceOption, error) {
	presence, err := s.localFormatPresence(ctx, sessionID)
	if err != nil {
		return glanceOption{}, err
	}
	if !presence[index] {
		return glanceOption{}, nil
	}
	name := fmt.Sprintf("status-format[%d]", index)
	value, err := s.sessionOptionValue(ctx, sessionID, name)
	if err != nil {
		return glanceOption{}, err
	}
	return glanceOption{Present: true, Value: value}, nil
}

// sessionOptionValue reads one session option's exact value, preserving
// intentional padding and emptiness.
func (s *Source) sessionOptionValue(ctx context.Context, sessionID, name string) (string, error) {
	output, err := s.runTmux(ctx, "show-options", "-qv", "-t", sessionID, name)
	if err != nil {
		return "", fmt.Errorf("read tmux session %s %s: %w", sessionID, name, err)
	}
	return strings.TrimRight(string(output), "\r\n"), nil
}

// setGlanceRow renders and writes one row. It is used by tests and by the
// enable path; reconcile goes through applyGlanceLocked.
func (s *Source) setGlanceRow(ctx context.Context, index int, agents []capabilities.Agent) error {
	row := glance.Render(agents, s.glance.WithDefaults(), s.clientWidth(ctx), s.now())
	if _, err := s.runTmux(ctx, "set-option", "-g", fmt.Sprintf("status-format[%d]", index), row); err != nil {
		return fmt.Errorf("update tmux glance row: %w", err)
	}
	return nil
}

// clientWidth returns the narrowest attached client's width, or zero when the
// width is unknown (no client, or tmux cannot report one).
func (s *Source) clientWidth(ctx context.Context) int {
	output, err := s.runTmux(ctx, "list-clients", "-F", "#{client_width}")
	if err != nil {
		return 0
	}
	width := 0
	for _, field := range strings.Fields(string(output)) {
		value, err := strconv.Atoi(field)
		if err != nil || value <= 0 {
			continue
		}
		if width == 0 || value < width {
			width = value
		}
	}
	return width
}

// showOption reads one global option's exact value, preserving intentional
// padding. Only the command-output terminator is stripped.
func (s *Source) showOption(ctx context.Context, name string) (string, error) {
	output, err := s.runTmux(ctx, "show-options", "-gqv", name)
	if err != nil {
		return "", fmt.Errorf("read tmux option %s: %w", name, err)
	}
	return strings.TrimRight(string(output), "\r\n"), nil
}

func (s *Source) readGlanceState(ctx context.Context) (glanceState, error) {
	raw, err := s.showOption(ctx, glanceStateOption)
	if err != nil {
		return glanceState{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return glanceState{}, nil
	}
	var state glanceState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		// A value nexo did not write is not ownership; treat it as off instead
		// of failing every watcher poll.
		return glanceState{}, nil
	}
	return state, nil
}

func (s *Source) writeGlanceState(ctx context.Context, state glanceState) error {
	contents, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode tmux glance state: %w", err)
	}
	if _, err := s.runTmux(ctx, "set-option", "-g", glanceStateOption, string(contents)); err != nil {
		return fmt.Errorf("save tmux glance state: %w", err)
	}
	return nil
}

func (s *Source) clearGlanceState(ctx context.Context) error {
	if _, err := s.runTmux(ctx, "set-option", "-gu", glanceStateOption); err != nil {
		return fmt.Errorf("clear tmux glance state: %w", err)
	}
	return nil
}

// statusRows converts a tmux `status` value to the number of rows it shows.
func statusRows(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off":
		return 0
	case "on":
		return 1
	default:
		rows, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || rows < 0 {
			return 1
		}
		return rows
	}
}

package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/glance"
)

// Glance option names. They are server-scoped user options, so one tmux
// server's glance state never leaks into another.
const (
	glanceIndexOption  = "@nexo-glance-index"
	glanceStatusOption = "@nexo-glance-status"
	glanceFormatOption = "@nexo-glance-format"
)

// maxStatusRows is tmux's limit on the number of status lines.
const maxStatusRows = 5

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
}

// Glance implements the user-bound `nexo tmux glance` command on this server.
func (s *Source) Glance(ctx context.Context, action GlanceAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.glanceLocked(ctx, action)
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
		if state.index > 0 {
			return s.glanceOffLocked(ctx, state)
		}
		return s.glanceOnLocked(ctx, state)
	default:
		return fmt.Errorf("unknown glance action %q", action)
	}
}

// glanceServer is the glance state of one tmux server.
type glanceServer struct {
	status      string
	index       int
	savedStatus string
	savedFormat string
}

func (s *Source) readGlanceState(ctx context.Context) (glanceServer, error) {
	status, err := s.showOption(ctx, "status")
	if err != nil {
		return glanceServer{}, err
	}
	indexRaw, err := s.showOption(ctx, glanceIndexOption)
	if err != nil {
		return glanceServer{}, err
	}
	savedStatus, err := s.showOption(ctx, glanceStatusOption)
	if err != nil {
		return glanceServer{}, err
	}
	savedFormat, err := s.showOption(ctx, glanceFormatOption)
	if err != nil {
		return glanceServer{}, err
	}
	return glanceServer{
		status:      status,
		index:       atoiOrZero(indexRaw),
		savedStatus: savedStatus,
		savedFormat: savedFormat,
	}, nil
}

func (s *Source) glanceOnLocked(ctx context.Context, state glanceServer) error {
	if state.index > 0 {
		// Already on: refresh the row in place without re-saving the server's
		// original settings.
		return s.writeGlanceRowLocked(ctx, state.index)
	}
	index, err := glanceIndexFor(statusRows(state.status))
	if err != nil {
		return err
	}
	formatOption := fmt.Sprintf("status-format[%d]", index)
	original, err := s.showOption(ctx, formatOption)
	if err != nil {
		return err
	}
	// Ownership is written before the row: an interrupted enable still leaves
	// the saved values behind, so a later off restores the server.
	for _, option := range []struct{ name, value string }{
		{glanceIndexOption, strconv.Itoa(index)},
		{glanceStatusOption, state.status},
		{glanceFormatOption, original},
	} {
		if _, err := s.runTmux(ctx, "set-option", "-g", option.name, option.value); err != nil {
			return fmt.Errorf("save tmux glance state: %w", err)
		}
	}
	if err := s.writeGlanceRowLocked(ctx, index); err != nil {
		return err
	}
	if _, err := s.runTmux(ctx, "set-option", "-g", "status", strconv.Itoa(index+1)); err != nil {
		return fmt.Errorf("raise tmux status rows: %w", err)
	}
	return nil
}

func (s *Source) glanceOffLocked(ctx context.Context, state glanceServer) error {
	if state.index <= 0 {
		return nil
	}
	formatOption := fmt.Sprintf("status-format[%d]", state.index)
	if strings.TrimSpace(state.savedFormat) == "" {
		if _, err := s.runTmux(ctx, "set-option", "-gu", formatOption); err != nil {
			return fmt.Errorf("restore tmux status format: %w", err)
		}
	} else if _, err := s.runTmux(ctx, "set-option", "-g", formatOption, state.savedFormat); err != nil {
		return fmt.Errorf("restore tmux status format: %w", err)
	}
	savedStatus := state.savedStatus
	if strings.TrimSpace(savedStatus) == "" {
		savedStatus = "on"
	}
	if _, err := s.runTmux(ctx, "set-option", "-g", "status", savedStatus); err != nil {
		return fmt.Errorf("restore tmux status rows: %w", err)
	}
	for _, option := range []string{glanceIndexOption, glanceStatusOption, glanceFormatOption} {
		if _, err := s.runTmux(ctx, "set-option", "-gu", option); err != nil {
			return fmt.Errorf("clear tmux glance state: %w", err)
		}
	}
	return nil
}

// reconcileGlanceLocked keeps the enabled row current. It is called by the
// watcher on every poll, so agent state changes reach tmux without another
// command. The caller holds s.mu.
func (s *Source) reconcileGlanceLocked(ctx context.Context, agents []capabilities.Agent) error {
	indexRaw, err := s.showOption(ctx, glanceIndexOption)
	if err != nil {
		return err
	}
	index := atoiOrZero(indexRaw)
	if index <= 0 {
		return nil
	}
	return s.setGlanceRow(ctx, index, agents)
}

// writeGlanceRowLocked lists the current agents and writes the row.
func (s *Source) writeGlanceRowLocked(ctx context.Context, index int) error {
	agents, err := s.listAgents(ctx, false)
	if err != nil {
		return err
	}
	return s.setGlanceRow(ctx, index, agents)
}

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

// showOption reads one server option, returning an empty value when it is not
// set. The `-q` flag keeps an unset user option from failing the call.
func (s *Source) showOption(ctx context.Context, name string) (string, error) {
	output, err := s.runTmux(ctx, "show-options", "-gqv", name)
	if err != nil {
		return "", fmt.Errorf("read tmux option %s: %w", name, err)
	}
	return strings.TrimSpace(string(output)), nil
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

// glanceIndexFor returns the first status row index the glance row may use.
// Rows below the current status count are already displayed, so taking the
// current count never overwrites a row the user already sees.
func glanceIndexFor(rows int) (int, error) {
	if rows < 1 {
		rows = 1
	}
	if rows >= maxStatusRows {
		return 0, fmt.Errorf("tmux status already uses all %d rows", maxStatusRows)
	}
	return rows, nil
}

func atoiOrZero(value string) int {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || number < 0 {
		return 0
	}
	return number
}

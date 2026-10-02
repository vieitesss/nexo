package tmux

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/agentstate"
)

// unseenWindowOption marks a window whose window list is highlighted by nexo.
// Its presence, not the configured colour, identifies a nexo highlight, so a
// colour change still clears the previous one.
const unseenWindowOption = "@nexo-unseen"

// windowHighlightFormat lists each window with the nexo marker and both current
// window-list styles. tmux expands every format variable.
const windowHighlightFormat = "#{window_id}\t#{@nexo-unseen}\t#{window-status-style}\t#{window-status-current-style}"

// highlightWindowOptions are the window-list style options nexo sets while a
// window holds an unseen stopped agent. The format itself is never touched, so
// a user's window-status-format and status-left stay exactly as configured.
var highlightWindowOptions = [...]string{"window-status-style", "window-status-current-style"}

// renderHighlightStyle turns a configured tmux colour into a window-list
// style. A value that already carries a style attribute is used verbatim, so a
// user may configure either "red" or a full style such as "bg=red,bold".
func renderHighlightStyle(colour string) string {
	colour = strings.TrimSpace(colour)
	if strings.Contains(colour, "=") {
		return colour
	}
	return "fg=" + colour
}

// windowHighlight is one window's observed highlight state.
type windowHighlight struct {
	id           string
	marked       bool
	style        string
	currentStyle string
}

// highlightAction is one window-list change reconcile must apply.
type highlightAction struct {
	windowID string
	clear    bool
}

func parseWindowHighlights(output []byte) []windowHighlight {
	var windows []windowHighlight
	for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 4)
		if len(fields) != 4 {
			continue
		}
		id := strings.TrimSpace(fields[0])
		if id == "" {
			continue
		}
		windows = append(windows, windowHighlight{
			id:           id,
			marked:       strings.TrimSpace(fields[1]) != "",
			style:        strings.TrimSpace(fields[2]),
			currentStyle: strings.TrimSpace(fields[3]),
		})
	}
	return windows
}

// highlightActions returns the minimal ordered set of changes that turns the
// observed window-list state into the desired highlight set. Linked windows
// appear once per link in list-windows, so they are deduplicated by stable id
// before any action is derived.
func highlightActions(windows []windowHighlight, desired []string, style string) []highlightAction {
	wanted := make(map[string]struct{}, len(desired))
	for _, id := range desired {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(windows))
	var actions []highlightAction
	for _, window := range windows {
		if _, ok := seen[window.id]; ok {
			continue
		}
		seen[window.id] = struct{}{}
		_, want := wanted[window.id]
		switch {
		case want && (!window.marked || window.style != style || window.currentStyle != style):
			actions = append(actions, highlightAction{windowID: window.id})
		case !want && window.marked:
			actions = append(actions, highlightAction{windowID: window.id, clear: true})
		}
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].windowID < actions[j].windowID })
	return actions
}

// unseenWindows returns the window ids that hold at least one unseen stopped
// agent. Results line up with observed panes in observation order.
func unseenWindows(observed []observedPane, results []agentstate.Result) []string {
	seen := make(map[string]struct{})
	limit := len(results)
	if len(observed) < limit {
		limit = len(observed)
	}
	for index := 0; index < limit; index++ {
		state := results[index].State
		if state.State != "stopped" || state.Seen {
			continue
		}
		if id := observed[index].windowID; id != "" {
			seen[id] = struct{}{}
		}
	}
	windows := make([]string, 0, len(seen))
	for id := range seen {
		windows = append(windows, id)
	}
	sort.Strings(windows)
	return windows
}

// highlightCleanupTimeout bounds the shutdown cleanup so a hung tmux server
// cannot keep the watcher from exiting.
const highlightCleanupTimeout = 5 * time.Second

// reconcileHighlights makes the tmux window list match the desired set of
// windows: every window holding an unseen stopped agent is highlighted in the
// configured colour, and every window nexo highlighted before is restored.
func (s *Source) reconcileHighlights(ctx context.Context, desired []string) error {
	style := renderHighlightStyle(s.config.WithDefaults().HighlightColor)
	output, err := s.runTmux(ctx, "list-windows", "-a", "-F", windowHighlightFormat)
	if err != nil {
		return fmt.Errorf("list tmux windows: %w", err)
	}
	for _, action := range highlightActions(parseWindowHighlights(output), desired, style) {
		if err := s.applyHighlight(ctx, action, style); err != nil {
			return err
		}
	}
	return nil
}

// clearHighlights removes every highlight nexo owns on the selected server. The
// watcher calls it on an orderly shutdown while it still holds the per-server
// watcher lock, so it never clears another live watcher's windows.
func (s *Source) clearHighlights(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconcileHighlights(ctx, nil)
}

func (s *Source) applyHighlight(ctx context.Context, action highlightAction, style string) error {
	var err error
	if action.clear {
		err = s.clearHighlight(ctx, action.windowID)
	} else {
		err = s.setHighlight(ctx, action.windowID, style)
	}
	if err == nil {
		return nil
	}
	// A window can vanish between list-windows and this change. Only a
	// successful listing that no longer names the window confirms that, so a
	// genuine style or server failure still aborts the watcher.
	gone, checkErr := s.windowGone(ctx, action.windowID)
	if checkErr == nil && gone {
		return nil
	}
	return err
}

func (s *Source) setHighlight(ctx context.Context, windowID, style string) error {
	// Ownership is written before the first style mutation: an interrupted set
	// leaves the marker behind, so the next poll repairs both styles instead of
	// leaving an unowned highlight that startup would never clear.
	if _, err := s.runTmux(ctx, "set-option", "-w", "-t", windowID, unseenWindowOption, "1"); err != nil {
		return fmt.Errorf("mark tmux window highlight: %w", err)
	}
	for _, option := range highlightWindowOptions {
		if _, err := s.runTmux(ctx, "set-option", "-w", "-t", windowID, option, style); err != nil {
			return fmt.Errorf("set tmux window highlight: %w", err)
		}
	}
	return nil
}

func (s *Source) clearHighlight(ctx context.Context, windowID string) error {
	for _, option := range highlightWindowOptions {
		if _, err := s.runTmux(ctx, "set-option", "-w", "-u", "-t", windowID, option); err != nil {
			return fmt.Errorf("clear tmux window highlight: %w", err)
		}
	}
	// The marker goes last: a crash mid-clear leaves the marker behind, so the
	// next poll retries instead of leaving a stuck highlight.
	if _, err := s.runTmux(ctx, "set-option", "-w", "-u", "-t", windowID, unseenWindowOption); err != nil {
		return fmt.Errorf("clear tmux window highlight marker: %w", err)
	}
	return nil
}

// windowGone reports whether a window disappeared between listing and applying
// a highlight. A listing error is a genuine server failure and is returned so
// the watcher does not mistake a dead server for an ordinary window close.
func (s *Source) windowGone(ctx context.Context, windowID string) (bool, error) {
	output, err := s.runTmux(ctx, "list-windows", "-a", "-F", "#{window_id}")
	if err != nil {
		return false, err
	}
	for _, id := range strings.Fields(string(output)) {
		if id == windowID {
			return false, nil
		}
	}
	return true, nil
}

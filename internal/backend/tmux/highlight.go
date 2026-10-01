package tmux

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vieitesss/nexo/internal/agentstate"
)

// unseenWindowOption marks a window whose window list is highlighted by nexo.
// Its presence, not the configured colour, identifies a nexo highlight, so a
// colour change still clears the previous one.
const unseenWindowOption = "@nexo-unseen"

// windowHighlightFormat lists each window with the nexo marker and its current
// effective window-list style. tmux expands all three format variables.
const windowHighlightFormat = "#{window_id}\t#{@nexo-unseen}\t#{window-status-style}"

// highlightWindowOptions are the window-list style options nexo sets while a
// window holds an unseen stopped agent. The format itself is never touched, so
// a user's window-status-format and status-left stay exactly as configured.
var highlightWindowOptions = [...]string{"window-status-style", "window-status-current-style"}

// renderHighlightStyle turns a configured tmux colour into a window-list
// style. A value that already carries a style attribute is used verbatim, so a
// user may configure either "red" or a full style such as "bg=red,bold".
func renderHighlightStyle(colour string) string {
	colour = strings.TrimSpace(colour)
	if colour == "" {
		colour = "red"
	}
	if strings.Contains(colour, "=") {
		return colour
	}
	return "fg=" + colour
}

// windowHighlight is one window's observed highlight state.
type windowHighlight struct {
	id     string
	marked bool
	style  string
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
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		id := strings.TrimSpace(fields[0])
		if id == "" {
			continue
		}
		windows = append(windows, windowHighlight{
			id:     id,
			marked: strings.TrimSpace(fields[1]) != "",
			style:  strings.TrimSpace(fields[2]),
		})
	}
	return windows
}

// highlightActions returns the minimal ordered set of changes that turns the
// observed window-list state into the desired highlight set.
func highlightActions(windows []windowHighlight, desired []string, style string) []highlightAction {
	wanted := make(map[string]struct{}, len(desired))
	for _, id := range desired {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	var actions []highlightAction
	for _, window := range windows {
		_, want := wanted[window.id]
		switch {
		case want && (!window.marked || window.style != style):
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

func (s *Source) applyHighlight(ctx context.Context, action highlightAction, style string) error {
	if action.clear {
		for _, option := range highlightWindowOptions {
			if _, err := s.runTmux(ctx, "set-option", "-w", "-u", "-t", action.windowID, option); err != nil {
				return fmt.Errorf("clear tmux window highlight: %w", err)
			}
		}
		// The marker goes last: a crash mid-clear leaves the marker behind, so
		// the next poll retries instead of leaving a stuck highlight.
		if _, err := s.runTmux(ctx, "set-option", "-w", "-u", "-t", action.windowID, unseenWindowOption); err != nil {
			return fmt.Errorf("clear tmux window highlight marker: %w", err)
		}
		return nil
	}
	for _, option := range highlightWindowOptions {
		if _, err := s.runTmux(ctx, "set-option", "-w", "-t", action.windowID, option, style); err != nil {
			return fmt.Errorf("set tmux window highlight: %w", err)
		}
	}
	// The marker goes last: a crash mid-set leaves the window unmarked, so the
	// next poll re-applies the highlight instead of treating it as done.
	if _, err := s.runTmux(ctx, "set-option", "-w", "-t", action.windowID, unseenWindowOption, "1"); err != nil {
		return fmt.Errorf("mark tmux window highlight: %w", err)
	}
	return nil
}

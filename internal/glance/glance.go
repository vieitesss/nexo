// Package glance renders the tmux status row that lists every agent pane with
// its status dot. Rendering is a pure function of the agent list, the glance
// configuration, the available width and the current time, so the row can be
// golden-tested without a tmux server.
package glance

import (
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

// Label opens every glance row. It is never truncated.
const Label = "nexo │"

// dot is the single glyph drawn in colour mode, where the colour carries the
// agent state.
const dot = "●"

// Symbol-mode glyphs. Without colour the glyph alone distinguishes a working
// agent from a stopped one and, among stopped agents, the unseen one that
// needs attention.
const (
	dotWorking       = "◐"
	dotUnseenStopped = "●"
	dotSeenStopped   = "○"
)

// agentState is the state an agent's dot shows.
type agentState int

const (
	stateUnseenStopped agentState = iota
	stateWorking
	stateSeenStopped
)

// entry is one rendered agent: its visible text, used to measure the row, and
// its tmux format, which carries the dot style and escaped agent text.
type entry struct {
	plain  string
	format string
}

// unknownAge is shown for an agent that has no stopped timestamp.
const unknownAge = "-"

// Render returns the tmux status-format value for the glance row. A width of
// zero or less means the width is unknown and no truncation is applied; tmux
// clips the row itself in that case.
func Render(agents []capabilities.Agent, cfg config.GlanceConfig, width int, now time.Time) string {
	cfg = cfg.WithDefaults()
	ordered := picker.SortAgents(agents)

	fields := cfg.OrderedFields()
	entries := make([]entry, 0, len(ordered))
	for _, agent := range ordered {
		entries = append(entries, renderEntry(agent, fields, cfg, now))
	}

	return colourStyle("bg", cfg.Tint) + truncateRow(escapeFormat(Label), Label, entries, width)
}

// truncateRow appends whole entries while they fit and marks omitted entries
// with an ellipsis. The label is always kept. Widths are measured on the
// visible text, never on the tmux format.
func truncateRow(row, label string, entries []entry, width int) string {
	remaining := -1
	if width > 0 {
		remaining = width - shell.StringWidth(label)
	}
	for _, entry := range entries {
		needed := 1 + shell.StringWidth(entry.plain)
		if width > 0 && remaining < needed {
			if remaining >= 2 {
				row += " …"
			}
			return row
		}
		row += " " + entry.format
		if width > 0 {
			remaining -= needed
		}
	}
	return row
}

// renderEntry renders one bracketed `[field field …]` entry. Agent text is
// escaped; only the dot carries a style.
func renderEntry(agent capabilities.Agent, fields []string, cfg config.GlanceConfig, now time.Time) entry {
	plain := make([]string, 0, len(fields))
	format := make([]string, 0, len(fields))
	for _, field := range fields {
		if field == "dot" {
			glyph, style := stateDot(agentStateOf(agent), cfg)
			plain = append(plain, glyph)
			format = append(format, style)
			continue
		}
		if value := fieldValue(agent, field, now); value != "" {
			plain = append(plain, value)
			format = append(format, escapeFormat(value))
		}
	}
	return entry{
		plain:  "[" + strings.Join(plain, " ") + "]",
		format: "[" + strings.Join(format, " ") + "]",
	}
}

func fieldValue(agent capabilities.Agent, field string, now time.Time) string {
	switch field {
	case "target":
		return strings.TrimSpace(agent.Target)
	case "harness":
		return strings.TrimSpace(agent.Harness)
	case "session":
		return strings.TrimSpace(agent.Container)
	case "window":
		return strings.TrimSpace(agent.Window)
	case "branch":
		return strings.TrimSpace(agent.Branch)
	case "age":
		return age(agent, now)
	default:
		return ""
	}
}

// stateDot returns the dot's visible glyph and its tmux format. Colour mode
// draws one glyph in the state's colour and restores the row foreground after
// it; symbol mode draws an uncoloured glyph per state.
func stateDot(state agentState, cfg config.GlanceConfig) (string, string) {
	glyph, colour := dotSeenStopped, cfg.Colors.Seen
	switch state {
	case stateUnseenStopped:
		glyph, colour = dotUnseenStopped, cfg.Colors.Unseen
	case stateWorking:
		glyph, colour = dotWorking, cfg.Colors.Working
	}
	if cfg.Dots == config.GlanceDotsSymbols {
		return glyph, glyph
	}
	style := colourStyle("fg", colour)
	if style == "" {
		return dot, dot
	}
	return dot, style + dot + "#[fg=default]"
}

// agentStateOf classifies an agent for drawing. Anything that is neither
// working nor an unseen stopped agent is drawn as seen stopped.
func agentStateOf(agent capabilities.Agent) agentState {
	if picker.AttentionRank(agent) == 0 {
		return stateUnseenStopped
	}
	if strings.EqualFold(strings.TrimSpace(agent.State), "working") {
		return stateWorking
	}
	return stateSeenStopped
}

func age(agent capabilities.Agent, now time.Time) string {
	if !strings.EqualFold(strings.TrimSpace(agent.State), "stopped") || agent.StoppedSince == nil {
		return unknownAge
	}
	return picker.StoppedAge(now, *agent.StoppedSince)
}

// colourStyle turns a configured colour into a tmux style for attribute, fg
// or bg. A tmux colour is used as is; an @palette option is expanded by tmux
// at draw time; an empty or "default" colour leaves the text unstyled.
func colourStyle(attribute, colour string) string {
	colour = strings.TrimSpace(colour)
	switch {
	case colour == "" || colour == "default":
		return ""
	case strings.HasPrefix(colour, "@"):
		return "#[" + attribute + "=#{" + colour + "}]"
	default:
		return "#[" + attribute + "=" + colour + "]"
	}
}

// escapeFormat doubles hashes so agent-controlled text cannot introduce a
// tmux style, format expression or command substitution into the row.
func escapeFormat(value string) string {
	return strings.ReplaceAll(value, "#", "##")
}

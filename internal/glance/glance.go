// Package glance renders the tmux status row that lists every agent pane with
// its status dot. Rendering is a pure function of the agent list, the glance
// configuration, the available width and the current time, so the row can be
// golden-tested without a tmux server.
package glance

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/shell"
)

// Label opens every glance row. It is never truncated.
const Label = "nexo │"

// Dot glyphs. The dot alone distinguishes a working agent from a stopped one
// and, among stopped agents, the unseen one that needs attention.
const (
	dotWorking       = "◐"
	dotUnseenStopped = "●"
	dotSeenStopped   = "○"
)

// unknownAge is shown for an agent that has no stopped timestamp.
const unknownAge = "-"

// Render returns the tmux status-format value for the glance row. A width of
// zero or less means the width is unknown and no truncation is applied; tmux
// clips the row itself in that case.
func Render(agents []capabilities.Agent, cfg config.GlanceConfig, width int, now time.Time) string {
	return render(agents, cfg, width, now)
}

func render(agents []capabilities.Agent, cfg config.GlanceConfig, width int, now time.Time) string {
	ordered := append([]capabilities.Agent(nil), agents...)
	sort.SliceStable(ordered, func(i, j int) bool { return less(ordered[i], ordered[j]) })

	fields := cfg.OrderedFields()
	plainEntries := make([]string, 0, len(ordered))
	for _, agent := range ordered {
		plainEntries = append(plainEntries, renderEntry(agent, fields, now))
	}

	plain := Label
	if len(plainEntries) > 0 {
		plain = truncateRow(plain, plainEntries, width)
	}
	return tintPrefix(cfg.Tint) + escapeFormat(plain)
}

// truncateRow appends whole entries while they fit and marks omitted entries
// with an ellipsis. The label is always kept.
func truncateRow(label string, entries []string, width int) string {
	row := label
	remaining := -1
	if width > 0 {
		remaining = width - shell.StringWidth(label)
	}
	for _, entry := range entries {
		needed := 1 + shell.StringWidth(entry)
		if width > 0 && remaining < needed {
			if remaining >= 2 {
				row += " …"
			}
			return row
		}
		row += " " + entry
		if width > 0 {
			remaining -= needed
		}
	}
	return row
}

// renderEntry renders one bracketed `[field field …]` entry.
func renderEntry(agent capabilities.Agent, fields []string, now time.Time) string {
	values := make([]string, 0, len(fields))
	for _, field := range fields {
		if value := fieldValue(agent, field, now); value != "" {
			values = append(values, value)
		}
	}
	return "[" + strings.Join(values, " ") + "]"
}

func fieldValue(agent capabilities.Agent, field string, now time.Time) string {
	switch field {
	case "dot":
		return stateDot(agent)
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

func stateDot(agent capabilities.Agent) string {
	switch {
	case strings.EqualFold(strings.TrimSpace(agent.State), "working"):
		return dotWorking
	case strings.EqualFold(strings.TrimSpace(agent.State), "stopped") && !agent.Seen:
		return dotUnseenStopped
	default:
		return dotSeenStopped
	}
}

func age(agent capabilities.Agent, now time.Time) string {
	if !strings.EqualFold(strings.TrimSpace(agent.State), "stopped") || agent.StoppedSince == nil {
		return unknownAge
	}
	return formatAge(now, *agent.StoppedSince)
}

// formatAge renders a compact duration for the stopped age.
func formatAge(now, since time.Time) string {
	seconds := int(now.Sub(since) / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return strconv.Itoa(seconds) + "s"
	}
	minutes := seconds / 60
	if minutes < 60 {
		return strconv.Itoa(minutes) + "m"
	}
	hours := minutes / 60
	if hours < 24 {
		return strconv.Itoa(hours) + "h"
	}
	return strconv.Itoa(hours/24) + "d"
}

// less orders agents the way the row reads: unseen stopped first, then
// working, then seen stopped, with a stable tie-break.
func less(a, b capabilities.Agent) bool {
	rankA, rankB := attentionRank(a), attentionRank(b)
	if rankA != rankB {
		return rankA < rankB
	}
	if a.Target != b.Target {
		return a.Target < b.Target
	}
	return a.PaneID < b.PaneID
}

func attentionRank(agent capabilities.Agent) int {
	switch strings.ToLower(strings.TrimSpace(agent.State)) {
	case "stopped":
		if !agent.Seen {
			return 0
		}
		return 2
	case "working":
		return 1
	default:
		return 3
	}
}

// tintPrefix turns the configured tint into a tmux style prefix. A tmux colour
// becomes a background; an @palette option is expanded by tmux at draw time;
// an empty or "default" tint leaves the row unstyled.
func tintPrefix(tint string) string {
	tint = strings.TrimSpace(tint)
	switch {
	case tint == "" || tint == "default":
		return ""
	case strings.HasPrefix(tint, "@"):
		return "#[bg=#{" + tint + "}]"
	default:
		return "#[bg=" + tint + "]"
	}
}

// escapeFormat doubles hashes so agent-controlled text cannot introduce a
// tmux style, format expression or command substitution into the row.
func escapeFormat(value string) string {
	return strings.ReplaceAll(value, "#", "##")
}

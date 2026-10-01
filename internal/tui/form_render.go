package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/shell"
)

// overlayForm places a border-only form over the existing tabbed frame. The
// content is laid out to the available width before framing, so truncation
// cannot eat the right border or make the edited value disappear.
func overlayForm(lines []string, form *stagedForm, width, height int) []string {
	if form == nil {
		return lines
	}
	if width <= 4 || height <= 4 {
		if form.deletePreflightEnabled && form.stage == form.deleteStageIndex() {
			form.deletePreflightSeen = false
		}
		return lines
	}
	contentWidth := max(1, width-8)
	body := formLinesForWidth(form.renderLines(), contentWidth)
	maxBody := max(1, height-4)
	form.viewportHeight = maxBody
	form.viewportWidth = contentWidth
	if form.deletePreflightEnabled && form.stage == form.deleteStageIndex() {
		maxScroll := max(0, len(body)-maxBody)
		form.deletePreflightScroll = min(maxScroll, max(0, form.deletePreflightScroll))
		if len(body) <= maxBody || form.deletePreflightScroll >= maxScroll {
			form.deletePreflightSeen = true
		}
	}
	if len(body) > maxBody {
		start := 0
		if form.deletePreflightEnabled && form.stage == form.deleteStageIndex() {
			start = form.deletePreflightScroll
		} else {
			// Keep the active field visible on every stage, including the
			// destructive confirmation stage where labels may be long.
			for index, line := range body {
				if strings.HasPrefix(line, "▸ ") {
					start = max(0, index-maxBody/2)
					break
				}
			}
		}
		start = min(start, len(body)-maxBody)
		body = body[start : start+maxBody]
	}
	style := lipgloss.NewStyle().Width(contentWidth).Padding(0, 1).Border(lipgloss.RoundedBorder()).Foreground(palette.Fg)
	modalLines := strings.Split(style.Render(strings.Join(body, "\n")), "\n")
	modalWidth := 0
	for _, line := range modalLines {
		modalWidth = max(modalWidth, shell.StringWidth(line))
	}
	modalWidth = min(modalWidth, width-2)
	startX := max(0, (width-modalWidth)/2)
	startY := max(1, (height-len(modalLines))/2)
	for index, modalLine := range modalLines {
		lineIndex := startY + index
		if lineIndex < 0 || lineIndex >= len(lines) {
			continue
		}
		// All content was fitted before framing. This only pads a line to the
		// actual framed width and never clips a border glyph.
		if shell.StringWidth(modalLine) < modalWidth {
			modalLine += strings.Repeat(" ", modalWidth-shell.StringWidth(modalLine))
		}
		lines[lineIndex] = strings.Repeat(" ", startX) + modalLine + strings.Repeat(" ", max(0, width-startX-modalWidth))
	}
	return lines
}

func formLinesForWidth(lines []string, width int) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if shell.StringWidth(line) <= width {
			out = append(out, line)
			continue
		}
		trimmed := strings.TrimSpace(line)
		// Findings are safety-critical content, not editable label/value pairs.
		// Wrap their complete text so narrow dialogs never hide a phrase.
		if strings.HasPrefix(trimmed, "!") || strings.HasPrefix(trimmed, "?") ||
			strings.HasPrefix(trimmed, "warning:") || strings.HasPrefix(trimmed, "blocker:") ||
			strings.HasPrefix(line, "    ") {
			out = append(out, wrapFormText(line, width)...)
			continue
		}
		if colon := strings.Index(line, ": "); colon >= 0 {
			label := line[:colon+2]
			value := line[colon+2:]
			available := width - shell.StringWidth(label)
			if available <= 1 {
				labelWidth := max(1, width/2)
				label = shell.Truncate(label, labelWidth, "…")
				available = max(1, width-shell.StringWidth(label))
			}
			out = append(out, label+shell.TruncateLeft(value, available, "…"))
			continue
		}
		out = append(out, shell.Truncate(line, width, "…"))
	}
	return out
}

func wrapFormText(value string, width int) []string {
	if width <= 0 {
		return nil
	}
	words := strings.Fields(value)
	if len(words) == 0 {
		return []string{""}
	}
	lines := []string{}
	current := ""
	for _, word := range words {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if shell.StringWidth(candidate) > width && current != "" {
			lines = append(lines, current)
			current = word
		} else {
			current = candidate
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

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
	if form == nil || width <= 4 || height <= 4 {
		return lines
	}
	contentWidth := max(1, width-8)
	body := formLinesForWidth(form.renderLines(), contentWidth)
	maxBody := max(1, height-4)
	if len(body) > maxBody {
		// Keep the first active-stage lines and the final controls/error line.
		keep := maxBody - 1
		if keep < 1 {
			body = body[len(body)-maxBody:]
		} else {
			body = append(append([]string(nil), body[:keep]...), body[len(body)-1])
		}
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
		if strings.HasPrefix(trimmed, "!") || strings.HasPrefix(trimmed, "?") || strings.HasPrefix(line, "    ") {
			out = append(out, wrapFormText(line, width)...)
			continue
		}
		if colon := strings.Index(line, ": "); colon >= 0 {
			label := line[:colon+2]
			available := width - shell.StringWidth(label)
			if available > 1 {
				out = append(out, label+shell.TruncateLeft(line[colon+2:], available, "…"))
				continue
			}
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

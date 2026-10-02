package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
		if form.viewportHook != nil {
			form.viewportHook(nil, 0, 0)
		}
		return lines
	}
	contentWidth := max(1, width-8)
	// Lipgloss applies padding inside the declared width. Leave those two
	// cells out of the body width so long summaries cannot be reflowed after
	// they have been truncated.
	innerWidth := max(1, contentWidth-2)
	maxBody := max(1, height-4)
	form.viewportHeight = maxBody
	form.setViewportWidth(innerWidth)
	body := formLinesForWidth(form.renderLines(), innerWidth)
	start, customStart := 0, false
	if form.viewportHook != nil {
		start, customStart = form.viewportHook(body, maxBody, innerWidth)
	}
	body = viewportFormLines(body, maxBody, start, customStart)
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

func viewportFormLines(lines []string, height, start int, customStart bool) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	if !customStart {
		start = shell.ViewportStart(len(lines), height, activeFormLine(lines))
	}
	start = max(0, min(start, len(lines)-height))
	return lines[start : start+height]
}

// activeFormLine returns the index of the line the cursor is on, preferring
// the checklist option marker over the focused-field marker. It returns -1
// when neither is present.
func activeFormLine(lines []string) int {
	for index, line := range lines {
		if strings.HasPrefix(line, "    ▸ ") {
			return index
		}
	}
	for index, line := range lines {
		if strings.HasPrefix(line, "▸ ") {
			return index
		}
	}
	return -1
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
	leadingBytes := 0
	if strings.HasPrefix(value, "    ") {
		leadingBytes = len(value) - len(strings.TrimLeft(value, " \t"))
	}
	leading := value[:leadingBytes]
	leadingWidth := min(width, shell.StringWidth(leading))
	if shell.StringWidth(leading) > leadingWidth {
		leading = shell.Truncate(leading, leadingWidth, "")
	}
	content := strings.TrimSpace(value[leadingBytes:])
	available := width - leadingWidth
	if available <= 0 {
		return []string{leading}
	}
	// Wordwrap keeps ordinary phrases intact; hardwrap then breaks aliases
	// and paths that contain no whitespace. Both preserve ANSI sequences and
	// grapheme/cell widths.
	wordWrapped := ansi.Wordwrap(content, available, " ")
	parts := strings.Split(wordWrapped, "\n")
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		hardWrapped := ansi.Hardwrap(part, available, false)
		for _, hardPart := range strings.Split(hardWrapped, "\n") {
			if hardPart == "" {
				lines = append(lines, leading)
				continue
			}
			lines = append(lines, leading+hardPart)
		}
	}
	if len(lines) == 0 {
		return []string{leading}
	}
	return lines
}

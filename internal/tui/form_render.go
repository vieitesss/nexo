package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/shell"
)

// overlayForm places a border-only form over the existing tabbed frame. The
// untouched cells retain the tab view and the form body deliberately has no
// background colour, so only the selected row paints a surface.
func overlayForm(lines []string, form *stagedForm, width, height int) []string {
	if form == nil || width <= 4 || height <= 4 {
		return lines
	}
	content := form.renderLines()
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Foreground(palette.Fg)
	modal := style.Render(strings.Join(content, "\n"))
	modalLines := strings.Split(modal, "\n")
	modalWidth := 0
	for _, line := range modalLines {
		if value := shell.StringWidth(line); value > modalWidth {
			modalWidth = value
		}
	}
	if modalWidth > width-4 {
		modalWidth = width - 4
	}
	startX := max(0, (width-modalWidth)/2)
	startY := max(1, (height-len(modalLines))/2)
	for index, modalLine := range modalLines {
		lineIndex := startY + index
		if lineIndex < 0 || lineIndex >= len(lines) {
			continue
		}
		modalLine = fit(modalLine, modalWidth)
		lines[lineIndex] = strings.Repeat(" ", startX) + modalLine + strings.Repeat(" ", max(0, width-startX-modalWidth))
	}
	return lines
}

// Package shell renders the shared full-screen picker surface.
package shell

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Item is one row in a picker list. Selectable items have a primary and,
// when space permits, a secondary line. Non-selectable items (group headers)
// only have a primary line. Separator requests one empty row after the item.
type Item struct {
	Primary    string
	Secondary  string
	Selectable bool
	Selected   bool
	Separator  bool
}

// Hint is one key/action chip in the persistent footer.
type Hint struct {
	Key   string
	Label string
}

// Options describes a complete shared picker surface.
type Options struct {
	Width, Height int
	Search        string
	Items         []Item
	Hints         []Hint
	Dialog        string
	// Paint is called after each line has been fitted and padded to Width.
	// It may apply foreground and selection-background styles.
	Paint       func(string, bool) string
	PaintSearch func(string) string
	PaintHint   func(string) string
}

// Render paints exactly Height rows of exactly Width cells. The search row is
// always first, and the hint bar is always last. The list is a viewport over
// the item lines; selected rows are kept visible when a viewport is needed.
func Render(options Options) string {
	if options.Width <= 0 || options.Height <= 0 {
		return ""
	}
	paint := options.Paint
	if paint == nil {
		paint = func(value string, _ bool) string { return value }
	}
	paintSearch := options.PaintSearch
	if paintSearch == nil {
		paintSearch = func(value string) string { return value }
	}
	paintHint := options.PaintHint
	if paintHint == nil {
		paintHint = func(value string) string { return value }
	}

	hintLines := HintLines(options.Width, options.Hints, options.Dialog)
	// The second hint row is the first expendable surface. At least one list
	// row must remain, so a tiny surface receives one footer row.
	hintRows := len(hintLines)
	if hintRows > 2 {
		hintRows = 2
	}
	if options.Height-1-hintRows < 1 {
		hintRows = 1
	}
	if hintRows < 1 {
		hintRows = 1
	}

	listHeight := options.Height - 1 - hintRows
	if listHeight < 1 {
		listHeight = 1
	}
	lines, selectedAt := itemLines(options.Items, listHeight)
	// When the list does not have room for two-line entries, secondary lines
	// collapse before footer rows are surrendered.
	showSecondary := listHeight >= 2 && hasSecondary(lines)
	if !showSecondary {
		lines, selectedAt = itemLinesCollapsed(options.Items)
	}
	if len(lines) == 0 {
		lines = []itemLine{{}}
	}
	start := viewportStart(len(lines), listHeight, selectedAt)
	body := make([]string, 0, listHeight)
	for row := 0; row < listHeight; row++ {
		index := start + row
		if index >= len(lines) {
			body = append(body, paintLine("", options.Width, false, paint))
			continue
		}
		line := lines[index]
		body = append(body, paintLine(line.Text, options.Width, line.Selected, paint))
	}

	result := make([]string, 0, options.Height)
	result = append(result, paintSearch(padOrTruncate(options.Search, options.Width)))
	result = append(result, body...)
	for index := 0; index < hintRows; index++ {
		value := ""
		if index < len(hintLines) {
			value = hintLines[index]
		}
		result = append(result, paintHint(padOrTruncate(value, options.Width)))
	}
	for len(result) < options.Height {
		result = append(result, paintLine("", options.Width, false, paint))
	}
	if len(result) > options.Height {
		result = result[:options.Height]
	}
	return strings.Join(result, "\n")
}

type itemLine struct {
	Text      string
	Selected  bool
	Item      int
	Secondary bool
}

func itemLines(items []Item, _ int) ([]itemLine, int) {
	lines := make([]itemLine, 0, len(items)*2)
	selectedAt := -1
	for index, item := range items {
		if item.Selected && selectedAt < 0 {
			selectedAt = len(lines)
		}
		lines = append(lines, itemLine{Text: item.Primary, Selected: item.Selected, Item: index})
		if item.Selectable {
			lines = append(lines, itemLine{Text: item.Secondary, Selected: item.Selected, Item: index, Secondary: true})
		}
		if item.Separator {
			lines = append(lines, itemLine{})
		}
	}
	return lines, selectedAt
}

func itemLinesCollapsed(items []Item) ([]itemLine, int) {
	lines := make([]itemLine, 0, len(items))
	selectedAt := -1
	for index, item := range items {
		if item.Selected && selectedAt < 0 {
			selectedAt = len(lines)
		}
		lines = append(lines, itemLine{Text: item.Primary, Selected: item.Selected, Item: index})
		if item.Separator {
			lines = append(lines, itemLine{})
		}
	}
	return lines, selectedAt
}

func hasSecondary(lines []itemLine) bool {
	for _, line := range lines {
		if line.Secondary {
			return true
		}
	}
	return false
}

func viewportStart(total, height, selected int) int {
	if total <= height || selected < 0 {
		return 0
	}
	start := selected - height/2
	if start < 0 {
		start = 0
	}
	if start+height > total {
		start = total - height
	}
	return start
}

// HintLines wraps chips onto at most two rows and ellipsizes a chip when it
// cannot fit. Dialog text takes precedence over normal chips.
func HintLines(width int, hints []Hint, dialog string) []string {
	if width <= 0 {
		return []string{""}
	}
	if dialog != "" {
		return []string{padOrTruncate(dialog, width)}
	}
	if len(hints) == 0 {
		return []string{""}
	}
	lines := make([]string, 1)
	for _, hint := range hints {
		chip := strings.TrimSpace(strings.TrimSpace(hint.Key) + " " + strings.TrimSpace(hint.Label))
		if chip == "" {
			continue
		}
		if StringWidth(chip) > width {
			chip = padOrTruncate(chip, width)
		}
		line := lines[len(lines)-1]
		candidate := chip
		if line != "" {
			candidate = line + "  " + chip
		}
		if StringWidth(candidate) <= width || line == "" {
			lines[len(lines)-1] = candidate
			continue
		}
		if len(lines) == 2 {
			// The final chip is represented by an ellipsis rather than leaking
			// past the right edge.
			remaining := width - StringWidth(line) - 2
			if remaining > 0 {
				lines[len(lines)-1] = line + "  " + padOrTruncate(chip, remaining)
			} else {
				lines[len(lines)-1] = Truncate(line, width, "…")
			}
			continue
		}
		lines = append(lines, chip)
	}
	return lines
}

// StringWidth returns terminal cells after ignoring ANSI control sequences.
func StringWidth(value string) int {
	width := 0
	for index := 0; index < len(value); {
		if value[index] == '\x1b' {
			index = skipANSI(value, index)
			continue
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		if r == '\n' || r == '\r' {
			index += size
			continue
		}
		width += runewidth.RuneWidth(r)
		index += size
	}
	return width
}

// Plain removes ANSI control sequences while retaining visible text. It is
// useful when a selected row must apply one uninterrupted background style.
func Plain(value string) string {
	var out strings.Builder
	for index := 0; index < len(value); {
		if value[index] == '\x1b' {
			index = skipANSI(value, index)
			continue
		}
		_, size := utf8.DecodeRuneInString(value[index:])
		out.WriteString(value[index : index+size])
		index += size
	}
	return out.String()
}

// Truncate keeps the beginning of a value and adds tail when it does not fit.
// ANSI sequences are retained while measuring and clipping.
func Truncate(value string, width int, tail string) string {
	if width <= 0 {
		return ""
	}
	if StringWidth(value) <= width {
		return value
	}
	tailWidth := StringWidth(tail)
	if tailWidth >= width {
		return clip(value, width)
	}
	return clip(value, width-tailWidth) + tail
}

func padOrTruncate(value string, width int) string {
	value = Truncate(value, width, "…")
	return value + strings.Repeat(" ", width-StringWidth(value))
}

func paintLine(value string, width int, selected bool, paint func(string, bool) string) string {
	return paint(padOrTruncate(value, width), selected)
}

func clip(value string, width int) string {
	if width <= 0 {
		return ""
	}
	var out strings.Builder
	used := 0
	for index := 0; index < len(value); {
		if value[index] == '\x1b' {
			next := skipANSI(value, index)
			out.WriteString(value[index:next])
			index = next
			continue
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		cell := runewidth.RuneWidth(r)
		if used+cell > width {
			break
		}
		out.WriteString(value[index : index+size])
		used += cell
		index += size
	}
	return out.String()
}

func skipANSI(value string, index int) int {
	if index+1 >= len(value) {
		return len(value)
	}
	// CSI and OSC are the sequences emitted by Lip Gloss styles.
	if value[index+1] == '[' {
		for index += 2; index < len(value); index++ {
			if value[index] >= '@' && value[index] <= '~' {
				return index + 1
			}
		}
		return len(value)
	}
	if value[index+1] == ']' {
		for index += 2; index < len(value); index++ {
			if value[index] == '\x07' {
				return index + 1
			}
			if value[index] == '\x1b' && index+1 < len(value) && value[index+1] == '\\' {
				return index + 2
			}
		}
		return len(value)
	}
	return index + 2
}

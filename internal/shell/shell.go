// Package shell renders the shared full-screen picker surface.
package shell

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
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
	// The second hint row is the first expendable surface. Keep the search
	// row and footer bounded even when there is no room for a list row.
	hintRows := len(hintLines)
	if hintRows > 2 {
		hintRows = 2
	}
	if options.Height <= 1 {
		hintRows = 0
	} else if options.Height-1-hintRows < 1 && hintRows > 1 {
		hintRows = 1
		// A surrendered footer row still advertises that actions are hidden.
		if options.Width > 0 {
			hintLines = []string{Truncate(hintLines[0], options.Width-1, "") + "…"}
		}
	}
	if hintRows < 0 {
		hintRows = 0
	}

	listHeight := options.Height - 1 - hintRows
	if listHeight < 0 {
		listHeight = 0
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
	start = keepSelectedItemVisible(lines, listHeight, start, selectedAt)
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
		if item.Selectable && StringWidth(item.Secondary) > 0 {
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

func keepSelectedItemVisible(lines []itemLine, height, start, selected int) int {
	if selected < 0 || selected+1 >= len(lines) || height < 2 {
		return start
	}
	secondary := lines[selected+1]
	if !secondary.Secondary || secondary.Item != lines[selected].Item {
		return start
	}
	if selected < start {
		start = selected
	}
	if selected+1 >= start+height {
		start = selected + 2 - height
	}
	if start < 0 {
		start = 0
	}
	if start+height > len(lines) {
		start = len(lines) - height
	}
	if start < 0 {
		start = 0
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
	overflow := false
	for _, hint := range hints {
		chip := strings.TrimSpace(strings.TrimSpace(hint.Key) + " " + strings.TrimSpace(hint.Label))
		if chip == "" {
			continue
		}
		chip = normalize(chip)
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
			overflow = true
			break
		}
		lines = append(lines, chip)
	}
	if overflow {
		const ellipsis = "…"
		last := len(lines) - 1
		line := lines[last]
		if line == "" {
			lines[last] = ellipsis
		} else if StringWidth(line)+2+StringWidth(ellipsis) <= width {
			lines[last] = line + "  " + ellipsis
		} else {
			lines[last] = Truncate(line, width-StringWidth(ellipsis), "") + ellipsis
		}
	}
	return lines
}

// StringWidth returns terminal cells after ANSI controls and unsafe text
// controls have been removed. Width is measured by grapheme clusters, so a
// joined emoji sequence occupies the same cells it does on the terminal.
func StringWidth(value string) int {
	return ansi.StringWidth(normalize(value))
}

// Plain removes ANSI control sequences while retaining visible text. It is
// useful when a selected row must apply one uninterrupted background style.
func Plain(value string) string {
	var out strings.Builder
	value = normalize(value)
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
	value = normalize(value)
	tail = normalize(tail)
	return ansi.Truncate(value, width, tail)
}

func padOrTruncate(value string, width int) string {
	value = Truncate(value, width, "…")
	return value + strings.Repeat(" ", width-StringWidth(value))
}

func paintLine(value string, width int, selected bool, paint func(string, bool) string) string {
	return paint(padOrTruncate(value, width), selected)
}

// PromptLine keeps the prompt and cursor visible while horizontally
// windowing the query or rename input around its end.
func PromptLine(width int, prompt, value, cursor string) string {
	if width <= 0 {
		return ""
	}
	prompt = normalize(prompt)
	value = normalize(value)
	cursor = normalize(cursor)
	cursorWidth := StringWidth(cursor)
	if cursorWidth > width {
		cursor = ansi.Truncate(cursor, width, "")
		cursorWidth = StringWidth(cursor)
	}
	available := width - cursorWidth
	if StringWidth(prompt) > available {
		prompt = ansi.Truncate(prompt, available, "")
	}
	available -= StringWidth(prompt)
	if available < 0 {
		available = 0
	}
	if StringWidth(value) > available {
		if available <= 1 {
			value = ""
		} else {
			value = ansi.TruncateLeft(value, StringWidth(value)-(available-1), "…")
		}
	}
	return padOrTruncate(prompt+value+cursor, width)
}

func normalize(value string) string {
	var out strings.Builder
	for index := 0; index < len(value); {
		if value[index] == '\x1b' || isC1Escape(value[index]) {
			next, keep := scanANSI(value, index)
			if keep {
				out.WriteString(value[index:next])
			}
			index = next
			continue
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		if unicode.IsControl(r) || r == '\u007f' {
			out.WriteByte(' ')
		} else {
			out.WriteString(value[index : index+size])
		}
		index += size
	}
	return out.String()
}

// isC1Escape reports the C1 bytes that introduce an ANSI control sequence.
// Other C1 bytes are handled as ordinary controls by normalize.
func isC1Escape(value byte) bool {
	switch value {
	case 0x90, 0x98, 0x9b, 0x9d, 0x9e, 0x9f:
		return true
	default:
		return false
	}
}

// scanANSI returns the end of the escape sequence at index and whether it is
// a safe SGR styling sequence. Non-SGR sequences are consumed in their
// entirety, including string-sequence payloads, so none of their controls can
// become rows or terminal commands in the rendered surface.
func scanANSI(value string, index int) (int, bool) {
	if index >= len(value) {
		return len(value), false
	}
	prefix := value[index]
	start := index + 1
	if prefix == '\x1b' {
		if start >= len(value) {
			return len(value), false
		}
		switch value[start] {
		case '[':
			return scanCSI(value, start+1)
		case ']', 'P', 'X', '^', '_':
			return scanStringANSI(value, start+1), false
		default:
			return scanESC(value, index), false
		}
	}
	switch prefix {
	case 0x9b: // CSI
		return scanCSI(value, start)
	case 0x90, 0x98, 0x9d, 0x9e, 0x9f: // DCS, SOS, OSC, PM, APC
		return scanStringANSI(value, start), false
	default:
		return start, false
	}
}

func scanCSI(value string, start int) (int, bool) {
	for index := start; index < len(value); index++ {
		current := value[index]
		if current < '@' || current > '~' {
			continue
		}
		if current != 'm' || !validSGR(value[start:index]) {
			return index + 1, false
		}
		return index + 1, true
	}
	return len(value), false
}

func validSGR(params string) bool {
	for index := 0; index < len(params); index++ {
		current := params[index]
		if (current < '0' || current > '9') && current != ';' && current != ':' {
			return false
		}
	}
	return true
}

func scanStringANSI(value string, start int) int {
	for index := start; index < len(value); index++ {
		switch value[index] {
		case '\x07', 0x9c: // BEL and C1 ST
			return index + 1
		case '\x1b':
			if index+1 < len(value) && value[index+1] == '\\' {
				return index + 2
			}
		}
	}
	return len(value)
}

func scanESC(value string, index int) int {
	cursor := index + 1
	if cursor >= len(value) {
		return len(value)
	}
	for cursor < len(value) {
		current := value[cursor]
		if current >= 0x30 && current <= 0x7e {
			return cursor + 1
		}
		if current < 0x20 || current > 0x2f {
			return cursor + 1
		}
		cursor++
	}
	return len(value)
}

func skipANSI(value string, index int) int {
	next, _ := scanANSI(value, index)
	return next
}

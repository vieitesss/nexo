// Package shell contains terminal-safe text and viewport helpers shared by nexo's TUI.
package shell

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

func StringWidth(value string) int { return ansi.StringWidth(normalize(value)) }

// Plain removes ANSI styling and unsafe controls while retaining visible text.
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

func Truncate(value string, width int, tail string) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(normalize(value), width, normalize(tail))
}

// TruncateLeft shortens value from the left to width cells, prefixing head.
// It is the left-anchored counterpart of Truncate for right-aligned columns
// such as search folders, where the meaningful tail must stay visible.
func TruncateLeft(value string, width int, head string) string {
	if width <= 0 {
		return ""
	}
	value = normalize(value)
	if StringWidth(value) <= width {
		return value
	}
	head = normalize(head)
	drop := StringWidth(value) - width + StringWidth(head)
	if drop <= 0 {
		return value
	}
	return ansi.TruncateLeft(value, drop, head)
}

// PromptLine keeps the prompt, input tail, and cursor visible within width.
func PromptLine(width int, prompt, value, cursor string) string {
	if width <= 0 {
		return ""
	}
	prompt, value, cursor = normalize(prompt), normalize(value), normalize(cursor)
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
	line := prompt + value + cursor
	line = Truncate(line, width, "")
	return line + strings.Repeat(" ", max(0, width-StringWidth(line)))
}

// ViewportStart keeps a selected line visible in a body viewport.
func ViewportStart(total, height, selected int) int {
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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
func isC1Escape(value byte) bool {
	switch value {
	case 0x90, 0x98, 0x9b, 0x9d, 0x9e, 0x9f:
		return true
	default:
		return false
	}
}
func scanANSI(value string, index int) (int, bool) {
	if index >= len(value) {
		return len(value), false
	}
	start := index + 1
	if value[index] == '\x1b' {
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
	switch value[index] {
	case 0x9b:
		return scanCSI(value, start)
	case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
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
		case '\x07', 0x9c:
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
func skipANSI(value string, index int) int { next, _ := scanANSI(value, index); return next }

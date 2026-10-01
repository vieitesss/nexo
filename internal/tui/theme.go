package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

const backgroundQueryTimeout = 150 * time.Millisecond

const osc11Query = "\x1b]11;?\x07"

type terminalQuery interface {
	io.ReadWriter
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// configureTheme selects the adaptive colour variant used by lipgloss. An
// explicit choice never touches the terminal; auto asks the controlling tty
// and falls back to COLORFGBG and then dark.
func configureTheme(theme string) {
	switch theme {
	case "light":
		lipgloss.SetHasDarkBackground(false)
	case "dark":
		lipgloss.SetHasDarkBackground(true)
	default:
		lipgloss.SetHasDarkBackground(detectDarkBackground())
	}
}

func detectDarkBackground() bool {
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err == nil {
		defer func() { _ = terminal.Close() }()
		if term.IsTerminal(terminal.Fd()) {
			state, rawErr := term.MakeRaw(terminal.Fd())
			if rawErr == nil {
				defer func() { _ = term.Restore(terminal.Fd(), state) }()
				query := terminalQuery(terminal)
				if dark, answered := queryDarkBackground(query, backgroundQueryTimeout); answered {
					return dark
				}
			}
		}
	}
	return colorFGBGIsDark(os.Getenv("COLORFGBG"))
}

// queryDarkBackground sends one OSC 11 request and consumes its complete
// response. The terminal's deadlines make this seam safe for both real files
// and deadline-aware fakes in tests.
func queryDarkBackground(terminal terminalQuery, timeout time.Duration) (dark, answered bool) {
	if timeout <= 0 {
		timeout = backgroundQueryTimeout
	}
	deadline := time.Now().Add(timeout)
	_ = terminal.SetWriteDeadline(deadline)
	if _, err := io.WriteString(terminal, osc11Query); err != nil {
		clearTerminalDeadlines(terminal)
		return false, false
	}
	_ = terminal.SetReadDeadline(deadline)
	defer clearTerminalDeadlines(terminal)

	data, ok := readOSC11Reply(terminal, deadline)
	if !ok {
		return false, false
	}
	// A terminal can queue more than one status response. Once the requested
	// reply is complete, consume immediately available bytes so Bubble Tea does
	// not interpret an OSC response as a key.
	drainTerminal(terminal)
	color, ok := parseOSC11Reply(data)
	if !ok {
		return false, false
	}
	return colorIsDark(color), true
}

func clearTerminalDeadlines(terminal terminalQuery) {
	_ = terminal.SetReadDeadline(time.Time{})
	_ = terminal.SetWriteDeadline(time.Time{})
}

func readOSC11Reply(terminal io.Reader, deadline time.Time) ([]byte, bool) {
	var data []byte
	buffer := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, err := terminal.Read(buffer)
		if n > 0 {
			data = append(data, buffer[:n]...)
			if _, ok := parseOSC11Reply(data); ok {
				return data, true
			}
		}
		if err != nil {
			return nil, false
		}
	}
	return nil, false
}

func drainTerminal(terminal terminalQuery) {
	_ = terminal.SetReadDeadline(time.Now())
	defer func() { _ = terminal.SetReadDeadline(time.Time{}) }()
	buffer := make([]byte, 256)
	for i := 0; i < 32; i++ {
		n, err := terminal.Read(buffer)
		if n == 0 || err != nil {
			return
		}
	}
}

// parseOSC11Reply extracts the background RGB value from an OSC 11 response.
// Terminals use either BEL or ST as the OSC terminator and may use one, two,
// or four hexadecimal digits per RGB component.
func parseOSC11Reply(data []byte) (string, bool) {
	const prefix = "\x1b]11;"
	for start := 0; start < len(data); {
		relative := bytes.Index(data[start:], []byte(prefix))
		if relative < 0 {
			return "", false
		}
		start += relative + len(prefix)
		end := -1
		for index := start; index < len(data); index++ {
			if data[index] == '\a' {
				end = index
				break
			}
			if data[index] == '\x1b' && index+1 < len(data) && data[index+1] == '\\' {
				end = index
				break
			}
		}
		if end < 0 {
			return "", false
		}
		if rgb, ok := parseOSC11RGB(string(data[start:end])); ok {
			return rgb, true
		}
		start = end + 1
	}
	return "", false
}

func parseOSC11RGB(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(value), "rgb:") {
		return "", false
	}
	parts := strings.Split(value[4:], "/")
	if len(parts) != 3 {
		return "", false
	}
	components := make([]int, len(parts))
	for index, part := range parts {
		if part == "" {
			return "", false
		}
		parsed, err := strconv.ParseUint(part, 16, 32)
		if err != nil {
			return "", false
		}
		max := uint64(1)<<(4*len(part)) - 1
		if max == 0 || parsed > max {
			return "", false
		}
		components[index] = int((parsed*255 + max/2) / max)
	}
	return fmt.Sprintf("#%02x%02x%02x", components[0], components[1], components[2]), true
}

func colorIsDark(color string) bool {
	if len(color) != 7 || color[0] != '#' {
		return true
	}
	r, errR := strconv.ParseUint(color[1:3], 16, 8)
	g, errG := strconv.ParseUint(color[3:5], 16, 8)
	b, errB := strconv.ParseUint(color[5:7], 16, 8)
	if errR != nil || errG != nil || errB != nil {
		return true
	}
	// Perceived luminance (sRGB channel values) mirrors the threshold used by
	// terminal colour detectors while keeping the decision easy to reason about.
	luminance := (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 255
	return luminance < 0.5
}

func colorFGBGIsDark(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	parts := strings.Split(value, ";")
	if len(parts) < 2 {
		return true
	}
	background := strings.TrimSpace(parts[len(parts)-1])
	index, err := strconv.Atoi(background)
	if err != nil || index < 0 || index > 15 {
		return true
	}
	return colorIsDark(ansiColorHex(index))
}

func ansiColorHex(index int) string {
	colors := [16]string{
		"#000000", "#800000", "#008000", "#808000",
		"#000080", "#800080", "#008080", "#c0c0c0",
		"#808080", "#ff0000", "#00ff00", "#ffff00",
		"#0000ff", "#ff00ff", "#00ffff", "#ffffff",
	}
	return colors[index]
}

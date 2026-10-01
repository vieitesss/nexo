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
	"golang.org/x/sys/unix"
)

const backgroundQueryTimeout = 150 * time.Millisecond

const (
	osc11Query = "\x1b]11;?\x07"
	da1Query   = "\x1b[c"
)

type terminalQuery interface {
	io.ReadWriter
	Fd() uintptr
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
	if err != nil {
		return colorFGBGIsDark(os.Getenv("COLORFGBG"))
	}
	defer func() { _ = terminal.Close() }()

	if !term.IsTerminal(terminal.Fd()) {
		return colorFGBGIsDark(os.Getenv("COLORFGBG"))
	}
	state, err := term.MakeRaw(terminal.Fd())
	if err != nil {
		return colorFGBGIsDark(os.Getenv("COLORFGBG"))
	}
	defer func() { _ = term.Restore(terminal.Fd(), state) }()

	if dark, answered := queryDarkBackground(terminal, backgroundQueryTimeout); answered {
		return dark
	}
	return colorFGBGIsDark(os.Getenv("COLORFGBG"))
}

// queryDarkBackground sends one OSC 11 request followed by a DA1 request.
// The DA1 response is a sentinel for terminals that do not answer OSC 11.
func queryDarkBackground(terminal terminalQuery, timeout time.Duration) (dark, answered bool) {
	if timeout <= 0 {
		timeout = backgroundQueryTimeout
	}
	deadline := time.Now().Add(timeout)
	if _, err := io.WriteString(terminal, osc11Query+da1Query); err != nil {
		return false, false
	}

	data, gotDA1 := readUntilDA1(terminal, deadline)
	if !gotDA1 {
		// A response can become readable at the timeout boundary. Consume it
		// without ever blocking before restoring the terminal to cooked mode.
		drainTerminal(terminal)
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

func readUntilDA1(terminal terminalQuery, deadline time.Time) ([]byte, bool) {
	var data []byte
	buffer := make([]byte, 256)
	for {
		ready, err := waitForData(terminal.Fd(), deadline)
		if err != nil || !ready {
			return data, false
		}
		n, readErr := terminal.Read(buffer)
		if n > 0 {
			data = append(data, buffer[:n]...)
			if end, ok := da1ReplyEnd(data); ok {
				return data[:end], true
			}
		}
		if readErr != nil || n == 0 {
			return data, false
		}
	}
}

// waitForData waits until fd is readable or deadline expires. The fd is
// selected immediately before each read because terminal files may block even
// after their descriptor has been obtained.
func waitForData(fd uintptr, deadline time.Time) (ready bool, err error) {
	for {
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		timeval := unix.NsecToTimeval(remaining.Nanoseconds())
		var readfds unix.FdSet
		readfds.Set(int(fd))                                                 //nolint:gosec
		n, selectErr := unix.Select(int(fd)+1, &readfds, nil, nil, &timeval) //nolint:gosec
		if selectErr == unix.EINTR {
			continue
		}
		if selectErr != nil {
			return false, selectErr
		}
		return n > 0, nil
	}
}

func drainTerminal(terminal terminalQuery) {
	buffer := make([]byte, 256)
	for i := 0; i < 32; i++ {
		ready, err := waitForData(terminal.Fd(), time.Now())
		if err != nil || !ready {
			return
		}
		n, readErr := terminal.Read(buffer)
		if readErr != nil || n == 0 {
			return
		}
	}
}

func da1ReplyEnd(data []byte) (int, bool) {
	for start := 0; start+3 < len(data); start++ {
		if data[start] != '\x1b' || data[start+1] != '[' || data[start+2] != '?' {
			continue
		}
		for end := start + 3; end < len(data); end++ {
			if data[end] == 'c' {
				return end + 1, true
			}
		}
	}
	return 0, false
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

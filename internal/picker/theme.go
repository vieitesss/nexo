package picker

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

var oscColorPattern = regexp.MustCompile(`(?i)\x1b\](10|11);([^\x07\x1b]+)(?:\x07|\x1b\\)`)

// ThemeQuery is the small terminal I/O seam used to obtain OSC 10/11 colors.
// The picker never assumes that a terminal will answer a query.
type ThemeQuery struct {
	Reader io.Reader
	Writer io.Writer
}

// ThemeFromTerminal asks for the terminal's default foreground and background.
// termenv owns terminal mode changes, OSC parsing, non-TTY detection, and its
// bounded status-report read. Non-terminal test readers can provide a
// SetReadDeadline method to exercise the same parsing seam without touching a
// live terminal.
func ThemeFromTerminal(ctx context.Context, terminal ThemeQuery, timeout time.Duration) Theme {
	if ctx == nil {
		ctx = context.Background()
	}
	fallback := DefaultTheme
	fallback.Color = true
	if terminal.Reader == nil || terminal.Writer == nil {
		return fallback
	}

	if reader, ok := terminal.Reader.(*os.File); ok {
		writer, writerOK := terminal.Writer.(*os.File)
		if !writerOK || !term.IsTerminal(reader.Fd()) || !term.IsTerminal(writer.Fd()) {
			return fallback
		}
		return themeFromTermenv(termenv.NewOutput(writer), fallback)
	}

	if timeout <= 0 {
		timeout = 100 * time.Millisecond
	}
	_, _ = io.WriteString(terminal.Writer, "\x1b]10;?\x07\x1b]11;?\x07")
	data, ok := readGenericReply(ctx, terminal.Reader, timeout)
	if !ok {
		return fallback
	}
	return themeFromReply(data, fallback)
}

func themeFromTermenv(output *termenv.Output, fallback Theme) Theme {
	fg := termenvColor(output.ForegroundColor())
	bg := termenvColor(output.BackgroundColor())
	if fg == "" || bg == "" {
		return fallback
	}
	return Theme{
		Foreground:          fg,
		Background:          bg,
		SelectionBackground: blend(bg, fg, 0.20),
		Muted:               blend(fg, bg, 0.55),
		Highlight:           highlightColor(fg),
		Color:               true,
	}
}

func termenvColor(color termenv.Color) string {
	switch value := color.(type) {
	case termenv.ANSIColor:
		return strconv.Itoa(int(value))
	case termenv.ANSI256Color:
		return strconv.Itoa(int(value))
	case termenv.RGBColor:
		return string(value)
	case termenv.NoColor:
		return ""
	default:
		return ""
	}
}

// readGenericReply has no uncancellable fallback: an arbitrary io.Reader is
// not safe to read with a deadline, so callers must provide a deadline-aware
// reader for this test seam. os.File terminals use termenv above.
func readGenericReply(ctx context.Context, reader io.Reader, timeout time.Duration) ([]byte, bool) {
	deadlineReader, ok := reader.(interface{ SetReadDeadline(time.Time) error })
	if !ok {
		return nil, false
	}
	_ = deadlineReader.SetReadDeadline(time.Now().Add(timeout))
	defer func() { _ = deadlineReader.SetReadDeadline(time.Time{}) }()
	return readReplyUntil(ctx, reader, timeout)
}

func readReplyUntil(ctx context.Context, reader io.Reader, timeout time.Duration) ([]byte, bool) {
	deadline := time.Now().Add(timeout)
	var data []byte
	buffer := make([]byte, 256)
	for {
		if bytesContainColor(data, 10) && bytesContainColor(data, 11) {
			return data, true
		}
		if contextError(ctx) != nil || time.Until(deadline) <= 0 {
			return nil, false
		}
		n, err := reader.Read(buffer)
		if n > 0 {
			data = append(data, buffer[:n]...)
			continue
		}
		if err != nil {
			return nil, false
		}
	}
}

func contextError(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func themeFromReply(data []byte, fallback Theme) Theme {
	fg, fgOK := parseOSCColor(data, 10)
	bg, bgOK := parseOSCColor(data, 11)
	if !fgOK || !bgOK {
		return fallback
	}
	return Theme{
		Foreground:          fg,
		Background:          bg,
		SelectionBackground: blend(bg, fg, 0.20),
		Muted:               blend(fg, bg, 0.55),
		Highlight:           highlightColor(fg),
		Color:               true,
	}
}

func bytesContainColor(data []byte, which int) bool {
	return strings.Contains(string(data), fmt.Sprintf("\x1b]%d;", which))
}

func parseOSCColor(data []byte, which int) (string, bool) {
	matches := oscColorPattern.FindAllStringSubmatch(string(data), -1)
	for _, match := range matches {
		if len(match) < 3 || match[1] != strconv.Itoa(which) {
			continue
		}
		if rgb, ok := parseRGB(match[2]); ok {
			return rgb, true
		}
	}
	return "", false
}

func parseRGB(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "rgb:") {
		parts := strings.Split(strings.TrimPrefix(strings.ToLower(value), "rgb:"), "/")
		if len(parts) != 3 {
			return "", false
		}
		values := make([]int64, 3)
		for index, part := range parts {
			parsed, err := strconv.ParseInt(part, 16, 32)
			if err != nil {
				return "", false
			}
			if len(part) <= 2 {
				values[index] = parsed
			} else {
				values[index] = parsed * 255 / 65535
			}
		}
		return fmt.Sprintf("#%02x%02x%02x", values[0], values[1], values[2]), true
	}
	if strings.HasPrefix(value, "#") && (len(value) == 7 || len(value) == 4) {
		return value, true
	}
	if numeric, ok := ansiColor(value); ok {
		return numeric, true
	}
	return "", false
}

func ansiColor(value string) (string, bool) {
	colors := map[string]string{
		"black": "0", "red": "1", "green": "2", "yellow": "3",
		"blue": "4", "magenta": "5", "cyan": "6", "white": "7",
		"bright-black": "8", "bright-red": "9", "bright-green": "10",
		"bright-yellow": "11", "bright-blue": "12", "bright-magenta": "13",
		"bright-cyan": "14", "bright-white": "15",
	}
	numeric, ok := colors[value]
	return numeric, ok
}

func highlightColor(foreground string) string {
	if numeric, ok := ansiColor(foreground); ok {
		if value, err := strconv.Atoi(numeric); err == nil && value < 8 {
			return "11"
		}
		return "15"
	}
	if value, err := strconv.Atoi(foreground); err == nil {
		if value < 8 {
			return "11"
		}
		return "15"
	}
	if strings.HasPrefix(foreground, "#") {
		return "#ffcc00"
	}
	return "15"
}

func blend(first, second string, amount float64) string {
	fr, fg, fb, ok := ansiRGB(first)
	if !ok {
		return "8"
	}
	sr, sg, sb, ok := ansiRGB(second)
	if !ok {
		return "8"
	}
	mix := func(a, b int) int { return int(float64(a)*(1-amount) + float64(b)*amount + 0.5) }
	return fmt.Sprintf("#%02x%02x%02x", mix(fr, sr), mix(fg, sg), mix(fb, sb))
}

func ansiRGB(value string) (int, int, int, bool) {
	if numeric, err := strconv.Atoi(value); err == nil && numeric >= 0 && numeric <= 15 {
		rgb := [16][3]int{
			{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
			{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
			{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
			{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
		}
		return rgb[numeric][0], rgb[numeric][1], rgb[numeric][2], true
	}
	return hexRGB(value)
}

func hexRGB(value string) (int, int, int, bool) {
	value = strings.TrimPrefix(value, "#")
	if len(value) == 3 {
		value = string([]byte{value[0], value[0], value[1], value[1], value[2], value[2]})
	}
	if len(value) != 6 {
		return 0, 0, 0, false
	}
	r, errR := strconv.ParseUint(value[0:2], 16, 8)
	g, errG := strconv.ParseUint(value[2:4], 16, 8)
	b, errB := strconv.ParseUint(value[4:6], 16, 8)
	if errR != nil || errG != nil || errB != nil {
		return 0, 0, 0, false
	}
	return int(r), int(g), int(b), true
}

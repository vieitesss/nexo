package picker

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var oscColorPattern = regexp.MustCompile(`(?i)\x1b\](10|11);([^\x07\x1b]+)(?:\x07|\x1b\\)`)

// ThemeQuery is the small terminal I/O seam used to obtain OSC 10/11 colors.
// The picker never assumes that a terminal will answer a query.
type ThemeQuery struct {
	Reader io.Reader
	Writer io.Writer
}

// ThemeFromTerminal asks for the terminal's default foreground and background.
// If either query does not answer before timeout, an ANSI-safe fallback is
// returned. The timeout is enforced even when Reader is a blocking stream.
func ThemeFromTerminal(ctx context.Context, terminal ThemeQuery, timeout time.Duration) Theme {
	fallback := DefaultTheme
	fallback.Color = true
	if terminal.Reader == nil || terminal.Writer == nil {
		return fallback
	}
	if timeout <= 0 {
		timeout = 100 * time.Millisecond
	}

	_, _ = io.WriteString(terminal.Writer, "\x1b]10;?\x07\x1b]11;?\x07")
	responses := make(chan []byte, 1)
	go func() {
		var data []byte
		buffer := make([]byte, 256)
		for {
			n, err := terminal.Reader.Read(buffer)
			if n > 0 {
				data = append(data, buffer[:n]...)
				if bytesContainColor(data, 10) && bytesContainColor(data, 11) {
					responses <- data
					return
				}
			}
			if err != nil {
				responses <- data
				return
			}
		}
	}()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	select {
	case <-ctx.Done():
		return fallback
	case data := <-responses:
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
			Highlight:           fg,
			Color:               true,
		}
	case <-deadline.C:
		return fallback
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
	switch value {
	case "black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
		"bright-black", "bright-red", "bright-green", "bright-yellow", "bright-blue", "bright-magenta", "bright-cyan", "bright-white":
		return value, true
	default:
		return "", false
	}
}

func blend(first, second string, amount float64) string {
	fr, fg, fb, ok := hexRGB(first)
	if !ok {
		return first
	}
	sr, sg, sb, ok := hexRGB(second)
	if !ok {
		return first
	}
	mix := func(a, b int) int { return int(float64(a)*(1-amount) + float64(b)*amount + 0.5) }
	return fmt.Sprintf("#%02x%02x%02x", mix(fr, sr), mix(fg, sg), mix(fb, sb))
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

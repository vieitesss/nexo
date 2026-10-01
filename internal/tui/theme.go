package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const tmuxThemeTimeout = 500 * time.Millisecond

// tmuxClientTheme reports tmux's view of the client terminal, "light" or
// "dark", or an empty string when tmux does not know.
var tmuxClientTheme = func() string {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxThemeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{client_theme}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// configureTheme selects the adaptive colour variant used by lipgloss. An
// explicit choice never touches the terminal; auto uses non-reading process
// hints so Bubble Tea remains the terminal's only input reader.
func configureTheme(theme string) {
	switch theme {
	case "light":
		lipgloss.SetHasDarkBackground(false)
	case "dark":
		lipgloss.SetHasDarkBackground(true)
	default:
		lipgloss.SetHasDarkBackground(fallbackDarkBackground())
	}
}

// fallbackDarkBackground uses only environment and multiplexer metadata. It
// intentionally never opens or reads the controlling terminal.
func fallbackDarkBackground() bool {
	if colorFGBG := strings.TrimSpace(os.Getenv("COLORFGBG")); colorFGBG != "" {
		return colorFGBGIsDark(colorFGBG)
	}
	if os.Getenv("TMUX") != "" {
		switch tmuxClientTheme() {
		case "light":
			return false
		case "dark":
			return true
		}
	}
	return true
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

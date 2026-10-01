package tui

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestParseOSC11Reply(t *testing.T) {
	for _, test := range []struct {
		name  string
		reply string
		want  string
	}{
		{name: "16 bit BEL", reply: "\x1b]11;rgb:ffff/0000/8080\a", want: "#ff0080"},
		{name: "8 bit ST", reply: "\x1b]11;rgb:00/80/ff\x1b\\", want: "#0080ff"},
		{name: "one digit", reply: "noise\x1b]11;rgb:0/f/8\a", want: "#00ff88"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, ok := parseOSC11Reply([]byte(test.reply)); !ok || got != test.want {
				t.Fatalf("parseOSC11Reply() = %q, %v; want %q, true", got, ok, test.want)
			}
		})
	}
}

func TestColorFGBGAndLuminance(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "15;0", want: true},
		{value: "0;15", want: false},
		{value: "7;7", want: false},
		{value: "", want: true},
		{value: "garbage", want: true},
	} {
		if got := colorFGBGIsDark(test.value); got != test.want {
			t.Errorf("colorFGBGIsDark(%q) = %v, want %v", test.value, got, test.want)
		}
	}
	if !colorIsDark("#000000") || colorIsDark("#ffffff") {
		t.Fatal("luminance failed black/white classification")
	}
}

func TestFallbackDarkBackgroundAsksTmuxFirst(t *testing.T) {
	previous := tmuxClientTheme
	t.Cleanup(func() { tmuxClientTheme = previous })
	for _, test := range []struct {
		name      string
		tmux      string
		theme     string
		colorFGBG string
		want      bool
	}{
		{name: "COLORFGBG light hint wins", tmux: "/tmp/tmux,1,0", theme: "light", colorFGBG: "15;0", want: true},
		{name: "COLORFGBG dark hint wins", tmux: "/tmp/tmux,1,0", theme: "dark", colorFGBG: "0;15", want: false},
		{name: "unknown tmux theme uses COLORFGBG", tmux: "/tmp/tmux,1,0", theme: "", colorFGBG: "0;15", want: false},
		{name: "outside tmux ignores tmux", tmux: "", theme: "light", colorFGBG: "", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TMUX", test.tmux)
			t.Setenv("COLORFGBG", test.colorFGBG)
			tmuxClientTheme = func() string { return test.theme }
			if got := fallbackDarkBackground(); got != test.want {
				t.Fatalf("fallbackDarkBackground() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestConfigureThemeLightUsesLightAdaptiveColours(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	previous := lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetHasDarkBackground(previous) })
	configureTheme("light")
	selection := lipgloss.NewStyle().Background(selectionBackground()).Render("x")
	accentToken := lipgloss.NewStyle().Foreground(palette.Accent).Render("x")
	if !bytes.Contains([]byte(selection), []byte("217")) {
		t.Fatalf("selection = %q, want #d9eaf7 escape", selection)
	}
	wantAccent := lipgloss.NewStyle().Foreground(lipgloss.Color("#4E7A00")).Render("x")
	if accentToken != wantAccent {
		t.Fatalf("accent = %q, want #4E7A00 escape %q", accentToken, wantAccent)
	}
}

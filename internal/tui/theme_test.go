package tui

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
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

func TestQueryDarkBackgroundNoAnswerTimesOut(t *testing.T) {
	terminal, _ := openThemePTY(t)
	restorePTYMode(t, terminal)

	const timeout = 30 * time.Millisecond
	started := time.Now()
	dark, answered := queryDarkBackground(terminal, timeout)
	elapsed := time.Since(started)
	if answered || dark {
		t.Fatalf("queryDarkBackground() = %v, %v; want false, false", dark, answered)
	}
	if elapsed > timeout+50*time.Millisecond {
		t.Fatalf("queryDarkBackground took %s, want no more than %s", elapsed, timeout+50*time.Millisecond)
	}
}

func TestQueryDarkBackgroundOnlyDA1FallsBack(t *testing.T) {
	terminal, peer := openThemePTY(t)
	restorePTYMode(t, terminal)
	done := make(chan struct{})
	go respondToThemeQuery(t, peer, "\x1b[?1;2c", done)

	started := time.Now()
	if dark, answered := queryDarkBackground(terminal, time.Second); answered || dark {
		t.Fatalf("queryDarkBackground() = %v, %v; want false, false", dark, answered)
	}
	if elapsed := time.Since(started); elapsed >= time.Second/2 {
		t.Fatalf("DA1 sentinel took %s, want an immediate response", elapsed)
	}
	<-done
}

func TestQueryDarkBackgroundConsumesOSC11AndDA1(t *testing.T) {
	terminal, peer := openThemePTY(t)
	restorePTYMode(t, terminal)
	done := make(chan struct{})
	go respondToThemeQuery(t, peer, "\x1b]11;rgb:ffff/ffff/ffff\x07\x1b[?1;2c", done)

	if dark, answered := queryDarkBackground(terminal, time.Second); answered != true || dark {
		t.Fatalf("queryDarkBackground() = %v, %v; want false, true", dark, answered)
	}
	<-done
	assertThemePTYEmpty(t, terminal)
}

func TestQueryDarkBackgroundHandlesSplitReply(t *testing.T) {
	terminal, peer := openThemePTY(t)
	restorePTYMode(t, terminal)
	done := make(chan struct{})
	go respondToThemeQueryInParts(t, peer, []string{
		"\x1b]11;rgb:ffff/",
		"ffff/ffff\x07\x1b[?1;",
		"2c",
	}, done)

	if dark, answered := queryDarkBackground(terminal, time.Second); answered != true || dark {
		t.Fatalf("queryDarkBackground() = %v, %v; want false, true", dark, answered)
	}
	<-done
	assertThemePTYEmpty(t, terminal)
}

func respondToThemeQuery(t *testing.T, peer io.ReadWriter, response string, done chan<- struct{}) {
	t.Helper()
	defer close(done)
	request := make([]byte, len(osc11Query)+len(da1Query))
	if _, err := io.ReadFull(peer, request); err != nil {
		t.Errorf("read query: %v", err)
		return
	}
	if string(request) != osc11Query+da1Query {
		t.Errorf("query = %q, want %q", request, osc11Query+da1Query)
		return
	}
	if _, err := io.WriteString(peer, response); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func respondToThemeQueryInParts(t *testing.T, peer io.ReadWriter, responses []string, done chan<- struct{}) {
	t.Helper()
	defer close(done)
	request := make([]byte, len(osc11Query)+len(da1Query))
	if _, err := io.ReadFull(peer, request); err != nil {
		t.Errorf("read query: %v", err)
		return
	}
	if string(request) != osc11Query+da1Query {
		t.Errorf("query = %q, want %q", request, osc11Query+da1Query)
		return
	}
	for _, response := range responses {
		if _, err := io.WriteString(peer, response); err != nil {
			t.Errorf("write response: %v", err)
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func restorePTYMode(t *testing.T, terminal *os.File) {
	t.Helper()
	state, err := term.MakeRaw(terminal.Fd())
	if err != nil {
		t.Skipf("pty cannot enter raw mode: %v", err)
	}
	t.Cleanup(func() { _ = term.Restore(terminal.Fd(), state) })
}

func assertThemePTYEmpty(t *testing.T, terminal *os.File) {
	t.Helper()
	ready, err := waitForData(terminal.Fd(), time.Now())
	if err != nil {
		t.Fatalf("select pty: %v", err)
	}
	if ready {
		t.Fatal("query left bytes unread on pty")
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
		{name: "tmux light beats dark COLORFGBG", tmux: "/tmp/tmux,1,0", theme: "light", colorFGBG: "15;0", want: false},
		{name: "tmux dark beats light COLORFGBG", tmux: "/tmp/tmux,1,0", theme: "dark", colorFGBG: "0;15", want: true},
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

// TestAutoThemeFallsBackWithoutAnswerOrHints covers the auto-mode composition:
// when the tty stays silent and no hint is available the theme is dark, while
// a real tty answer wins over the fallback chain.
func TestAutoThemeFallsBackWithoutAnswerOrHints(t *testing.T) {
	previous := tmuxClientTheme
	t.Cleanup(func() { tmuxClientTheme = previous })
	tmuxClientTheme = func() string { return "" }
	t.Setenv("TMUX", "")
	t.Setenv("COLORFGBG", "")

	if got := resolveAutoTheme(func() (bool, bool) { return false, false }); !got {
		t.Fatal("auto theme without an answer or hints = light, want dark")
	}

	t.Setenv("COLORFGBG", "15;0")
	if got := resolveAutoTheme(func() (bool, bool) { return false, true }); got {
		t.Fatal("auto theme with a light tty answer = dark, want light")
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

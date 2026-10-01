package tui

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

type themeQueryFake struct {
	response bytes.Buffer
	written  bytes.Buffer
}

func (f *themeQueryFake) Read(p []byte) (int, error) {
	if f.response.Len() == 0 {
		return 0, errors.New("timeout")
	}
	return f.response.Read(p)
}
func (f *themeQueryFake) Write(p []byte) (int, error)    { return f.written.Write(p) }
func (*themeQueryFake) SetReadDeadline(time.Time) error  { return nil }
func (*themeQueryFake) SetWriteDeadline(time.Time) error { return nil }

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

func TestQueryDarkBackgroundUsesDeadlineAwareTerminal(t *testing.T) {
	fake := &themeQueryFake{}
	fake.response.WriteString("\x1b]11;rgb:ffff/ffff/ffff\x07")
	dark, ok := queryDarkBackground(fake, time.Millisecond)
	if !ok || dark {
		t.Fatalf("queryDarkBackground() = %v, %v; want false, true", dark, ok)
	}
	if got := fake.written.String(); got != osc11Query {
		t.Fatalf("query wrote %q, want OSC 11 query %q", got, osc11Query)
	}
}

func TestQueryDarkBackgroundNoReply(t *testing.T) {
	fake := &themeQueryFake{}
	if dark, ok := queryDarkBackground(fake, time.Millisecond); ok || dark {
		t.Fatalf("queryDarkBackground() = %v, %v; want false, false", dark, ok)
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

var _ io.ReadWriter = (*themeQueryFake)(nil)

package glance

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/shell"
)

var update = flag.Bool("update", false, "update golden files")

var reference = time.Date(2026, 2, 21, 12, 0, 0, 0, time.UTC)

func at(secondsAgo int) *time.Time {
	value := reference.Add(-time.Duration(secondsAgo) * time.Second)
	return &value
}

func testAgents() []capabilities.Agent {
	return []capabilities.Agent{
		{
			PaneID: "%1", Target: "zeta:1.0", Container: "zeta", Window: "editor",
			Harness: "pi", State: "stopped", Seen: false, StoppedSince: at(65), Branch: "feature/row",
		},
		{
			PaneID: "%2", Target: "alpha:0.1", Container: "alpha", Window: "shell",
			Harness: "claude", State: "working", Seen: false, Branch: "main",
		},
		{
			PaneID: "%3", Target: "beta:2.0", Container: "beta", Window: "logs",
			Harness: "codex", State: "stopped", Seen: true, StoppedSince: at(3720), Branch: "",
		},
	}
}

func TestRenderGolden(t *testing.T) {
	cases := []struct {
		name   string
		agents []capabilities.Agent
		cfg    config.GlanceConfig
		width  int
	}{
		{name: "default", agents: testAgents(), width: 0},
		{name: "all-fields", agents: testAgents(), cfg: config.GlanceConfig{Fields: config.GlanceFieldOrder()}, width: 0},
		{name: "narrow-drops-entries", agents: testAgents(), width: 30},
		{name: "narrow-ellipsis", agents: testAgents(), width: 24},
		{name: "label-only", agents: testAgents(), width: 6},
		{name: "no-agents", agents: nil, width: 0},
		{name: "tint-colour", agents: testAgents(), cfg: config.GlanceConfig{Tint: "colour238"}, width: 0},
		{name: "tint-palette", agents: testAgents(), cfg: config.GlanceConfig{Tint: "@thm_surface_0"}, width: 0},
		{name: "tint-default", agents: testAgents(), cfg: config.GlanceConfig{Tint: "default"}, width: 0},
		{name: "dots-symbols", agents: testAgents(), cfg: config.GlanceConfig{Dots: config.GlanceDotsSymbols}, width: 0},
		{name: "colors-custom", agents: testAgents(), cfg: config.GlanceConfig{Colors: config.GlanceColors{
			Working: "@thm_yellow", Unseen: "colour160", Seen: "brightblack",
		}}, width: 0},
		{name: "colors-with-tint", agents: testAgents(), cfg: config.GlanceConfig{
			Tint: "@thm_surface_0", Fields: []string{"dot", "target", "age"},
		}, width: 0},
		{name: "escapes-format", agents: []capabilities.Agent{
			{PaneID: "%9", Target: "#(rm -rf)", Harness: "pi", State: "working"},
			{PaneID: "%8", Target: "a#b", Harness: "a#b", State: "stopped", Seen: true, StoppedSince: at(5)},
		}, cfg: config.GlanceConfig{Fields: []string{"dot", "target", "harness"}}, width: 0},
		{name: "working-age-unknown", agents: []capabilities.Agent{
			{PaneID: "%4", Target: "solo:0.0", Harness: "pi", State: "working"},
		}, cfg: config.GlanceConfig{Fields: []string{"dot", "target", "age"}}, width: 0},
		{name: "skips-missing-fields", agents: []capabilities.Agent{
			{PaneID: "%5", Target: "only:0.0", Harness: "pi", State: "working"},
		}, cfg: config.GlanceConfig{Fields: []string{"dot", "target", "session", "branch"}}, width: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := Render(test.agents, test.cfg, test.width, reference)
			path := filepath.Join("testdata", test.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(want) {
				t.Fatalf("Render() = %q, want golden %q", got, string(want))
			}
		})
	}
}

// TestRenderNeverTruncatesLabel pins that a narrow row keeps the whole label.
func TestRenderNeverTruncatesLabel(t *testing.T) {
	for _, width := range []int{0, 1, 5, 6, 7} {
		got := Render(testAgents(), config.GlanceConfig{}, width, reference)
		if len(got) < len(Label) || got[:len(Label)] != Label {
			t.Fatalf("Render(width=%d) = %q, want label prefix %q", width, got, Label)
		}
	}
}

// TestRenderFitsConfiguredWidth pins that a bounded row never exceeds its
// budget for the agent sets used by the goldens.
func TestRenderFitsConfiguredWidth(t *testing.T) {
	for width := 1; width <= 60; width++ {
		got := Render(testAgents(), config.GlanceConfig{Fields: config.GlanceFieldOrder()}, width, reference)
		if gotWidth := shell.StringWidth(visible(got)); gotWidth > width && gotWidth > len([]rune(Label)) {
			t.Fatalf("Render(width=%d) width = %d, row %q exceeds budget", width, gotWidth, got)
		}
	}
}

// TestRenderStylesOnlyTheDot pins that colour mode keeps the visible row of
// symbol mode apart from the glyph, so styles never shift the measured width.
func TestRenderStylesOnlyTheDot(t *testing.T) {
	colour := visible(Render(testAgents(), config.GlanceConfig{}, 0, reference))
	want := "nexo │ [● zeta:1.0] [● alpha:0.1] [● beta:2.0]"
	if colour != want {
		t.Fatalf("visible colour row = %q, want %q", colour, want)
	}
}

// visible returns what tmux draws for a row: styles removed and escaped
// hashes collapsed.
func visible(row string) string {
	var out strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case strings.HasPrefix(row[i:], "##"):
			out.WriteByte('#')
			i++
		case strings.HasPrefix(row[i:], "#["):
			i += strings.IndexByte(row[i:], ']')
		default:
			out.WriteByte(row[i])
		}
	}
	return out.String()
}

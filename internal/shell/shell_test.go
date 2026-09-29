package shell

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderFitsGraphemesAndSanitizesControls(t *testing.T) {
	view := Render(Options{
		Width:  10,
		Height: 4,
		Search: "query\nwith\tcontrols",
		Items:  []Item{{Primary: "👩‍💻界\nnext", Selectable: true, Secondary: "secondary\tline", Selected: true}},
		Hints:  []Hint{{Key: "↑", Label: "move"}},
	})
	lines := strings.Split(view, "\n")
	if len(lines) != 4 {
		t.Fatalf("rendered %d rows, want 4: %q", len(lines), view)
	}
	for index, line := range lines {
		if got := lipgloss.Width(line); got != 10 {
			t.Fatalf("row %d width = %d, want 10: %q", index, got, line)
		}
	}
	if strings.Contains(lines[0], "\n") || strings.Contains(lines[0], "\t") {
		t.Fatalf("control character escaped into search row: %q", lines[0])
	}
}

func TestNormalizeDropsUnsafeANSISequencesAndTheirPayloads(t *testing.T) {
	osc := "x\x1b]0;title\nnext\x07y"
	if got := Plain(osc); got != "xy" {
		t.Fatalf("OSC payload escaped into text: %q", got)
	}
	if got := Plain("x\x1b[2Jy"); got != "xy" {
		t.Fatalf("erase CSI escaped into text: %q", got)
	}
	styled := "\x1b[31mx\x1b[0m"
	if got := normalize(styled); got != styled {
		t.Fatalf("SGR styling was not retained: %q", got)
	}
	view := Render(Options{Width: 10, Height: 4, Search: osc, Items: []Item{{Primary: "x\x1b[2Jy"}}})
	lines := strings.Split(view, "\n")
	if len(lines) != 4 {
		t.Fatalf("unsafe sequence changed row count: %d, want 4: %q", len(lines), view)
	}
	if strings.Contains(view, "\x1b[2J") || strings.Contains(view, "next") {
		t.Fatalf("unsafe sequence reached rendered frame: %q", view)
	}
}

func TestRenderKeepsSelectedSecondaryVisible(t *testing.T) {
	view := Render(Options{
		Width:  40,
		Height: 4,
		Items: []Item{
			{Primary: "group"},
			{Primary: "selected", Secondary: "selected metadata", Selectable: true, Selected: true},
			{Primary: "next", Secondary: "next metadata", Selectable: true},
		},
	})
	if !strings.Contains(view, "selected metadata") {
		t.Fatalf("selected secondary line is not visible: %q", view)
	}
	if strings.Contains(view, "selected\n") {
		t.Fatalf("selected item is split at viewport edge: %q", view)
	}
}

func TestHintLinesMarksDroppedActions(t *testing.T) {
	lines := HintLines(10, []Hint{
		{Key: "↑", Label: "move"},
		{Key: "↓", Label: "move"},
		{Key: "enter", Label: "open"},
	}, "")
	if len(lines) != 2 {
		t.Fatalf("hint rows = %d, want 2: %#v", len(lines), lines)
	}
	joined := strings.Join(lines, "")
	if !strings.Contains(joined, "…") {
		t.Fatalf("dropped hint actions lack ellipsis: %#v", lines)
	}
	for index, line := range lines {
		if got := StringWidth(line); got > 10 {
			t.Fatalf("hint row %d width = %d, want <= 10: %q", index, got, line)
		}
	}
}

func TestRenderKeepsFooterAtTinyHeights(t *testing.T) {
	for _, height := range []int{1, 2, 3} {
		view := Render(Options{Width: 8, Height: height, Search: "Search: ", Hints: []Hint{{Key: "↑", Label: "move"}, {Key: "x", Label: "kill"}}})
		lines := strings.Split(view, "\n")
		if len(lines) != height {
			t.Fatalf("height %d produced %d rows: %q", height, len(lines), view)
		}
		for index, line := range lines {
			if got := lipgloss.Width(line); got != 8 {
				t.Fatalf("height %d row %d width = %d: %q", height, index, got, line)
			}
		}
		if height >= 2 && !strings.Contains(lines[len(lines)-1], "…") {
			t.Fatalf("height %d footer should advertise hidden actions: %q", height, view)
		}
	}
}

func TestPromptLineKeepsCursorVisible(t *testing.T) {
	line := PromptLine(12, "Search: ", "a very long query", "▌")
	if !strings.HasSuffix(line, "▌") {
		t.Fatalf("cursor is not visible: %q", line)
	}
	if StringWidth(line) != 12 {
		t.Fatalf("prompt width = %d, want 12: %q", StringWidth(line), line)
	}
}

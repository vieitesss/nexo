package progress

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestCompleteClearsLiveViewAndFinalViewIsComplete(t *testing.T) {
	model := NewModel("upgrade", []string{"one", "two", "three"})
	for _, name := range []string{"one", "two", "three"} {
		model.Apply(Event{Machine: name, State: StateDone, New: "v2"})
	}
	updated, _ := model.Update(CompleteMsg{})
	model = updated.(Model)
	if got := model.View(); got != "" {
		t.Fatalf("View after CompleteMsg = %q, want empty", got)
	}
	final := model.FinalView()
	for _, want := range []string{"one", "two", "three", "✓ done"} {
		if !strings.Contains(final, want) {
			t.Errorf("FinalView() = %q, missing %q", final, want)
		}
	}
}

func TestLiveViewIsHeightBounded(t *testing.T) {
	model := NewModel("upgrade", []string{"one", "two", "three", "four"})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 4})
	model = updated.(Model)
	lines := strings.Split(ansi.Strip(model.View()), "\n")
	if len(lines) > 4 {
		t.Fatalf("live view has %d lines at height 4: %q", len(lines), model.View())
	}
	if !strings.Contains(model.View(), "+3 more") {
		t.Fatalf("live view = %q, want overflow marker", model.View())
	}
}

func TestNarrowWidthsKeepStatusAndWrapLongMachineErrors(t *testing.T) {
	name := "a-machine-name-much-longer-than-any-window"
	model := NewModel("upgrade", []string{name})
	model.Apply(Event{Machine: name, State: StateFailed, Step: StepProbing, Err: errors.New("connection refused after a long network failure")})
	for _, width := range []int{20, 40, 80} {
		updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		current := updated.(Model)
		live := ansi.Strip(current.View())
		if !strings.Contains(live, "failed") {
			t.Errorf("width %d live view = %q, missing STATUS", width, live)
		}
		for _, line := range strings.Split(live, "\n") {
			if lipgloss.Width(line) > width {
				t.Errorf("width %d line width %d > width: %q", width, lipgloss.Width(line), line)
			}
		}
		for _, line := range strings.Split(ansi.Strip(current.FinalView()), "\n") {
			if lipgloss.Width(line) > width {
				t.Errorf("width %d final line width %d > width: %q", width, lipgloss.Width(line), line)
			}
		}
	}
}

func TestAbbreviatesEveryPseudoVersionForm(t *testing.T) {
	cases := []struct {
		version string
		want    string
	}{
		{"v1.2.3-20261002184137-1234bd744edf", "1234bd744edf"},
		{"v1.2.3-0.20261002184137-1234bd744edf+dirty", "1234bd744edf+dirty"},
		{"v1.2.3-pre.0.20261002184137-1234bd744edf+incompatible", "1234bd744edf+incompatible"},
		{"v1.2.3", "v1.2.3"},
	}
	for _, testCase := range cases {
		model := NewModel("upgrade", []string{"local"})
		model.Apply(Event{Machine: "local", State: StateDone, New: testCase.version})
		if got := ansi.Strip(model.FinalView()); !strings.Contains(got, testCase.want) {
			t.Errorf("version %q FinalView = %q, want %q", testCase.version, got, testCase.want)
		}
	}
}

package progress

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestEventsBecomeStableRows(t *testing.T) {
	started := time.Unix(100, 0)
	model := NewModel("upgrade v2.0.0", []string{"local", "devbox"})
	model.Apply(Event{Machine: "local", State: StateRunning, Step: StepProbing, At: started})
	model.Apply(Event{Machine: "devbox", State: StateRunning, Step: StepBuilding, At: started})
	if !strings.Contains(model.View(), "running") {
		t.Fatalf("running view = %q", model.View())
	}
	model.Apply(Event{Machine: "local", State: StateDone, Step: StepInstalling, Old: "v1", New: "v2", At: started.Add(2 * time.Second)})
	model.Apply(Event{Machine: "devbox", State: StateFailed, Step: StepProbing, Err: errors.New("offline"), At: started.Add(3 * time.Second)})

	rows := model.Rows()
	if len(rows) != 2 || rows[0].Machine != "local" || rows[1].Machine != "devbox" {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].State != StateDone || rows[0].Elapsed != 2*time.Second || rows[0].New != "v2" {
		t.Fatalf("done row = %#v", rows[0])
	}
	if rows[1].State != StateFailed || rows[1].Err == nil || rows[1].Elapsed != 3*time.Second {
		t.Fatalf("failed row = %#v", rows[1])
	}
	view := model.View()
	for _, want := range []string{"✓ done", "✗ failed", "offline", "probing"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() = %q, missing %q", view, want)
		}
	}
}

func TestColoredStatusColumnsUseVisibleWidths(t *testing.T) {
	model := NewModel("upgrade", []string{"short", "a-long-machine"})
	started := time.Unix(100, 0)
	model.Apply(Event{Machine: "short", State: StateFailed, Step: StepProbing, Err: errors.New("offline"), At: started, Elapsed: time.Second})
	model.Apply(Event{Machine: "a-long-machine", State: StateDone, Step: StepInstalling, New: "v2", At: started, Elapsed: 2 * time.Second})
	lines := strings.Split(ansi.Strip(model.View()), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines = %#v", lines)
	}
	header := lines[1]
	statusColumn := strings.Index(header, "STATUS")
	stepColumn := strings.Index(header, "STEP")
	elapsedColumn := strings.Index(header, "ELAPSED")
	for _, testCase := range []struct {
		line, status, step, elapsed string
	}{
		{lines[2], "✗ failed", "probing", "1s"},
		{lines[3], "✓ done", "", "2s"},
	} {
		if got := visibleColumn(testCase.line, testCase.status); got != visibleColumn(header, "STATUS") {
			t.Errorf("status column = %d, want %d in %q", got, statusColumn, testCase.line)
		}
		if testCase.step != "" {
			if got := visibleColumn(testCase.line, testCase.step); got != visibleColumn(header, "STEP") {
				t.Errorf("step column = %d, want %d in %q", got, stepColumn, testCase.line)
			}
		}
		if got := visibleColumn(testCase.line, testCase.elapsed); got != visibleColumn(header, "ELAPSED") {
			t.Errorf("elapsed column = %d, want %d in %q", got, elapsedColumn, testCase.line)
		}
	}
}

func visibleColumn(line, token string) int {
	index := strings.Index(line, token)
	if index < 0 {
		return -1
	}
	return lipgloss.Width(line[:index])
}

func TestDoneAndSkippedRowsLeaveStepBlank(t *testing.T) {
	model := NewModel("upgrade", []string{"done", "skipped"})
	at := time.Unix(10, 0)
	model.Apply(Event{Machine: "done", State: StateDone, Step: StepInstalling, Old: "v1", New: "v2", At: at, Elapsed: time.Second})
	model.Apply(Event{Machine: "skipped", State: StateSkipped, Step: StepProbing, Old: "v2", At: at, Elapsed: time.Second})
	lines := strings.Split(ansi.Strip(model.View()), "\n")
	if strings.Contains(lines[2], "installing") || strings.Contains(lines[3], "probing") {
		t.Fatalf("view = %q, done/skipped rows should not show a step", model.View())
	}
}

func TestTableAbbreviatesPseudoVersions(t *testing.T) {
	model := NewModel("upgrade", []string{"local"})
	model.Apply(Event{
		Machine: "local", State: StateDone,
		Old: "v0.0.0-20261002184137-1234bd744edf+dirty", New: "main@1234bd744edf",
		At: time.Unix(10, 0), Elapsed: time.Second,
	})
	got := model.View()
	if !strings.Contains(got, "1234bd744edf+dirty → main@1234bd744edf") {
		t.Fatalf("view = %q, want abbreviated pseudo-version", got)
	}
	if strings.Contains(got, "v0.0.0-20261002184137-") {
		t.Fatalf("view = %q, pseudo-version was not abbreviated", got)
	}
}

func TestDoneRowsShowVersionsAndFinalErrors(t *testing.T) {
	model := NewModel("upgrade v2.0.0 (dry-run)", []string{"local", "badhost", "already"})
	started := time.Unix(10, 0)
	model.Apply(Event{Machine: "local", State: StateDone, Old: "v1", New: "v2", At: started, Elapsed: time.Second})
	fullError := "detect operating system: " + strings.Repeat("network failure ", 20)
	model.Apply(Event{Machine: "badhost", State: StateFailed, Err: errors.New(fullError), At: started, Elapsed: time.Second})
	model.Apply(Event{Machine: "already", State: StateSkipped, Old: "v2", At: started, Elapsed: time.Second})
	updated, _ := model.Update(CompleteMsg{})
	model = updated.(Model)
	got := model.FinalView()
	if !strings.Contains(got, "v1 → v2") {
		t.Fatalf("view = %q, want old and new versions", got)
	}
	if !strings.Contains(got, "v2 (already on target)") {
		t.Fatalf("view = %q, want skipped detail", got)
	}
	if !strings.Contains(got, "badhost: "+fullError) {
		t.Fatalf("view = %q, want complete error below table", got)
	}
}

func TestCancelledRowsHideErrorDetails(t *testing.T) {
	model := NewModel("upgrade", []string{"slowhost"})
	model.Apply(Event{Machine: "slowhost", State: StateCancelled, Step: StepProbing, Err: errors.New("signal: killed"), At: time.Unix(10, 0), Elapsed: time.Second})
	updated, _ := model.Update(CompleteMsg{})
	model = updated.(Model)
	got := model.FinalView()
	if !strings.Contains(got, "– cancelled") {
		t.Fatalf("view = %q, want cancelled state", got)
	}
	if strings.Contains(got, "signal: killed") {
		t.Fatalf("view = %q, cancelled row leaked error detail", got)
	}
}

func TestDetailTruncatesToWindowAndFinalErrorsWrap(t *testing.T) {
	model := NewModel("upgrade", []string{"badhost"})
	longError := "detect operating system: " + strings.Repeat("x", 180)
	model.Apply(Event{Machine: "badhost", State: StateFailed, Step: StepProbing, Err: errors.New(longError), At: time.Unix(10, 0), Elapsed: time.Second})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 60})
	model = updated.(Model)
	updated, _ = model.Update(CompleteMsg{})
	model = updated.(Model)
	final := model.FinalView()
	lines := strings.Split(ansi.Strip(final), "\n")
	if !strings.Contains(final, "…") {
		t.Fatalf("view = %q, want an ellipsis in truncated detail", final)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > 60 {
			t.Errorf("line width = %d, want <= 60: %q", lipgloss.Width(line), line)
		}
	}
	flat := strings.NewReplacer("\n", "", " ", "").Replace(final)
	if !strings.Contains(flat, strings.ReplaceAll("badhost: "+longError, " ", "")) {
		t.Fatal("final frame did not retain complete error")
	}
	if !strings.Contains(final, "\n  ") {
		t.Fatal("final frame did not indent wrapped continuation lines")
	}
}

func TestCtrlCCallsCancel(t *testing.T) {
	called := false
	model := NewModel("upgrade", []string{"local"})
	model.SetCancel(func() { called = true })
	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !called {
		t.Fatal("Ctrl-C did not cancel the operation")
	}
}

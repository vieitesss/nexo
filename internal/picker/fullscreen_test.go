package picker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/shell"
)

func TestFullScreenViewHonorsWindowSizeAndKeepsSelectionVisible(t *testing.T) {
	places := ContainerPlaces([]capabilities.Container{
		{ID: "one", Name: "a-very-long-container-name", Machine: "host"},
		{ID: "two", Name: "another-long-container-name", Machine: "host"},
		{ID: "three", Name: "third", Machine: "host"},
	})
	model := NewModel(places, Theme{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	selected := model.Rows()[model.Cursor()].Place.ContainerID
	for _, size := range [][2]int{{200, 50}, {100, 30}, {80, 24}, {40, 6}, {40, 4}} {
		updated, _ = model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		model = updated.(Model)
		assertSurfaceSize(t, model.View(), size[0], size[1])
		if got := model.Rows()[model.Cursor()].Place.ContainerID; got != selected {
			t.Fatalf("selected container = %q after resize, want %q", got, selected)
		}
	}
}

func TestFullScreenViewKeepsContainerSelectionByIdentityOnBatch(t *testing.T) {
	places := ContainerPlaces([]capabilities.Container{{ID: "one", Name: "api", Machine: "host"}, {ID: "two", Name: "web", Machine: "host"}})
	model := NewModel(places, Theme{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	selected := model.Rows()[model.Cursor()].Place.ContainerID
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	model = updated.(Model)
	updated, _ = model.Update(RemoteBatch{Machine: "other", Places: []Place{{Kind: ContainerKind, Machine: "other", Group: "other", ContainerID: "three", ContainerName: "worker", DisplayRemainder: "worker"}}})
	model = updated.(Model)
	if got := model.Rows()[model.Cursor()].Place.ContainerID; got != selected {
		t.Fatalf("selected container = %q, want %q after resize and batch", got, selected)
	}
}

func TestFullScreenViewLongRenameKeepsCursorVisible(t *testing.T) {
	model := NewModel(ContainerPlaces([]capabilities.Container{{ID: "one", Name: "api", Machine: "host"}}), Theme{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a very long replacement name")})
	model = updated.(Model)
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 20, Height: 6})
	model = updated.(Model)
	first := strings.TrimRight(strings.Split(model.View(), "\n")[0], " ")
	if !strings.HasPrefix(first, "rename: ") || !strings.HasSuffix(first, "▌") {
		t.Fatalf("long rename line = %q, want prompt and cursor", first)
	}
}

func TestFullScreenViewBatchTypesUpdateRows(t *testing.T) {
	machine := LocalMachine()
	model := NewModel(ContainerPlaces([]capabilities.Container{{ID: "one", Name: "api", Machine: machine}}), Theme{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	model = updated.(Model)
	updated, _ = model.Update(PreviewBatch{Previews: map[ContainerRef]string{{Machine: machine, ID: "one"}: "preview"}})
	model = updated.(Model)
	if model.Places()[0].Preview != "preview" {
		t.Fatalf("preview batch did not update place: %#v", model.Places()[0])
	}
	updated, _ = model.Update(AgentBatch{Agents: []capabilities.Agent{{ContainerID: "one", Harness: "pi", State: "working"}}})
	model = updated.(Model)
	updated, _ = model.Update(RemoteBatch{Machine: "remote", Places: []Place{{Kind: ContainerKind, Machine: "remote", Group: "remote", ContainerID: "two", ContainerName: "worker"}}})
	model = updated.(Model)
	if len(model.Rows()) < 3 {
		t.Fatalf("batch updates produced %d rows, want local and remote rows", len(model.Rows()))
	}
	if !strings.Contains(model.View(), "pi working") {
		t.Fatalf("agent batch metadata missing from rendered view: %q", model.View())
	}
}

func TestFullScreenViewUsesTreeContinuationAndContainerFallback(t *testing.T) {
	model := NewModel(ContainerPlaces([]capabilities.Container{
		{ID: "one", Name: "api", Machine: "host"},
		{ID: "two", Name: "web", Machine: "host", Preview: "\nlast preview line"},
	}), Theme{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	model = updated.(Model)
	view := model.View()
	if !strings.Contains(view, "│   no agents") {
		t.Fatalf("container without agents lacks fallback metadata: %q", view)
	}
	if !strings.Contains(view, "│   last preview line") {
		t.Fatalf("container preview is not aligned under its name: %q", view)
	}
	if !strings.Contains(view, "└ + new container…") {
		t.Fatalf("new-container action is not a one-line row: %q", view)
	}
}

func TestFullScreenViewUsesGitCompactionOrder(t *testing.T) {
	model := NewModel(nil, Theme{})
	context := GitContext{Identity: "feature/a-very-long-branch-name", Adds: 123, Deletes: 45, Ahead: 8, Behind: 9}
	line := model.gitContextLine(context, 18)
	if strings.Contains(line, "↑") || strings.Contains(line, "↓") {
		t.Fatalf("divergence was not dropped first: %q", line)
	}
	if !strings.Contains(line, "+") || !strings.Contains(line, "-") {
		t.Fatalf("change summary was dropped before identity truncation: %q", line)
	}
	if got := shell.StringWidth(line); got > 18 {
		t.Fatalf("compacted Git line width = %d, want <= 18: %q", got, line)
	}
}

func TestViewAfterDegenerateResizeDoesNotReturnLegacyFrame(t *testing.T) {
	model := NewModel(nil, Theme{})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	model = updated.(Model)
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	model = updated.(Model)
	if got := model.View(); got != "" {
		t.Fatalf("degenerate full-screen view = %q, want empty frame", got)
	}
}

func assertSurfaceSize(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) != height {
		t.Fatalf("surface has %d rows, want %d: %q", len(lines), height, view)
	}
	for index, line := range lines {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("surface row %d has width %d, want %d: %q", index, got, width, line)
		}
	}
}

package picker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/capabilities"
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
	for _, size := range [][2]int{{200, 50}, {100, 30}, {80, 24}, {40, 6}, {40, 4}} {
		updated, _ = model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		model = updated.(Model)
		assertSurfaceSize(t, model.View(), size[0], size[1])
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

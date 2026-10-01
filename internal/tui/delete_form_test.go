package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/picker"
)

func TestDeleteFormUsesSelectedLexicalPathAndExplicitConfirmation(t *testing.T) {
	path := "/tmp/projects/link"
	var gotPath string
	var gotPermanent, gotForce bool
	model := NewModel(context.Background(), Deps{
		Projects: []picker.Place{{Kind: picker.ProjectKind, Path: path, Group: "/tmp/projects", DisplayRemainder: "link"}},
		DeleteProject: func(_ context.Context, value string, permanent, force bool) error {
			gotPath, gotPermanent, gotForce = value, permanent, force
			return nil
		},
	}, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlY})
	if model.input.form == nil {
		t.Fatal("Ctrl-Y did not open delete form")
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	for range []int{1, 2, 3, 4, 5} {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("permanent")})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	for range []int{1, 2} {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("yes")})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil || model.input.form == nil || !model.input.form.inFlight {
		t.Fatal("delete form did not submit")
	}
	model = updateModel(t, model, cmd())
	if model.input.form != nil {
		t.Fatal("successful delete left modal open")
	}
	if gotPath != path || !gotPermanent || !gotForce {
		t.Fatalf("delete callback = %q permanent=%v force=%v", gotPath, gotPermanent, gotForce)
	}
	model.SetSize(100, 12)
	if strings.Contains(model.View(), "delete project") {
		t.Fatalf("delete modal remained in view: %q", model.View())
	}
}

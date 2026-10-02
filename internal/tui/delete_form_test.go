package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/deletion"
	"github.com/vieitesss/nexo/internal/picker"
)

func TestDeletePreflightPrecedesDistinctConfirmation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "candidate")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	var preflightCalls, executeCalls int
	plan := deletion.Plan{Path: path, Action: "trash", Fingerprint: "selected", Warnings: []deletion.Finding{{Code: "non-empty-directory", Message: "contains data"}}, CanProceed: true}
	model := NewModel(context.Background(), Deps{
		Projects:          []picker.Place{{Kind: picker.ProjectKind, Path: path, Group: root, DisplayRemainder: "candidate"}},
		DeleteFingerprint: func(context.Context, string) (string, error) { return "selected", nil },
		DeletePreflight: func(context.Context, string, string) (deletion.Plan, error) {
			preflightCalls++
			return plan, nil
		},
		DeleteProjectPlan: func(context.Context, deletion.Plan, bool, bool) error {
			executeCalls++
			return nil
		},
	}, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	updated, preflightCmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	if preflightCmd == nil || model.input.form == nil || !model.input.form.inFlight {
		t.Fatal("Ctrl-Y did not start asynchronous preflight")
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if preflightCalls != 0 || executeCalls != 0 {
		t.Fatalf("Enter while preflight pending ran callbacks: preflight=%d execute=%d", preflightCalls, executeCalls)
	}
	model = updateModel(t, model, preflightCmd())
	if preflightCalls != 1 || model.input.form == nil || model.input.form.stage != 1 {
		t.Fatalf("preflight result = calls %d form=%v stage=%d", preflightCalls, model.input.form != nil, model.input.form.stage)
	}
	model.SetSize(100, 20)
	if !strings.Contains(model.View(), "contains data") {
		t.Fatalf("preflight warning not displayed: %q", model.View())
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeySpace})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if executeCalls != 0 {
		t.Fatal("warning acceptance advanced without distinct destructive confirmation")
	}
	for range 2 {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("yes")})
	updated, actionCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if actionCmd == nil || executeCalls != 0 {
		t.Fatalf("explicit confirmation did not submit: cmd=%v execute=%d", actionCmd != nil, executeCalls)
	}
	model = updateModel(t, model, actionCmd())
	if executeCalls != 1 {
		t.Fatalf("execute calls = %d, want one", executeCalls)
	}
}

func TestDeleteConfirmationHidesAcceptWarningsWithoutWarnings(t *testing.T) {
	form := newDeleteForm(Deps{}, context.Background(), "/tmp/project")
	form.plan = &deletion.Plan{Path: "/tmp/project", CanProceed: true}
	form.stage = form.preflightStage() + 1
	view := strings.Join(form.renderLines(), "\n")
	if strings.Contains(view, "accept warnings") {
		t.Fatalf("confirmation view shows hidden warnings toggle: %q", view)
	}
}

func TestDeletePreflightCancelReopenIgnoresLateResult(t *testing.T) {
	deps := Deps{
		DeleteFingerprint: func(context.Context, string) (string, error) { return "selected", nil },
		DeletePreflight: func(_ context.Context, path, _ string) (deletion.Plan, error) {
			return deletion.Plan{Path: path, Fingerprint: "selected", CanProceed: true}, nil
		},
	}
	a := newDeleteForm(deps, context.Background(), "/tmp/a")
	cmdA := a.startPreflight(deps)
	generationA := a.generation
	if cmdA == nil {
		t.Fatal("first preflight did not start")
	}
	if effect := a.stagedForm.key(tea.KeyMsg{Type: tea.KeyEsc}); !effect.Canceled {
		t.Fatal("Escape did not cancel first preflight")
	}
	b := newDeleteForm(deps, context.Background(), "/tmp/b")
	cmdB := b.startPreflight(deps)
	if cmdB == nil || generationA == b.generation {
		t.Fatalf("reopened preflight generation = %d, first = %d", b.generation, generationA)
	}

	model := NewModel(context.Background(), deps, ProjectsTab)
	model.input.form = b.stagedForm
	model.input.deleteForm = b
	model.input.actionInFlight = true
	model.input.actionGeneration = b.generation
	updated, _ := model.Update(cmdA())
	model = updated.(Model)
	if model.input.deleteForm != b || !b.preflightPending || b.plan != nil {
		t.Fatalf("late first result changed reopened form: pending=%v plan=%#v", b.preflightPending, b.plan)
	}
}

func TestDeleteRefreshKeepsNearestSelection(t *testing.T) {
	places := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/tmp/a", Group: "/tmp", DisplayRemainder: "a"},
		{Kind: picker.ProjectKind, Path: "/tmp/b", Group: "/tmp", DisplayRemainder: "b"},
		{Kind: picker.ProjectKind, Path: "/tmp/c", Group: "/tmp", DisplayRemainder: "c"},
	}
	model := NewModel(context.Background(), Deps{
		Projects: places,
		ProjectSource: func(context.Context) ([]picker.Place, error) {
			return []picker.Place{places[0], places[2]}, nil
		},
	}, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model.input.cursor = "project:/tmp/b"
	model.input.cursorMoved = true
	updated, refresh := model.Update(actionMsg{kind: "delete", destination: "/tmp/b"})
	model = updated.(Model)
	if refresh == nil {
		t.Fatal("delete did not start project refresh")
	}
	model = updateModel(t, model, refresh())
	if model.Cursor() != "project:/tmp/c" {
		t.Fatalf("cursor after delete = %q, want nearest surviving row", model.Cursor())
	}
}

func TestDeleteFormUsesSelectedLexicalPathAndExplicitConfirmation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "link ")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	var gotPath string
	var gotPermanent, gotForce bool
	plan := deletion.Plan{Path: path, Action: "trash", Fingerprint: "selected", CanProceed: true}
	model := NewModel(context.Background(), Deps{
		Projects: []picker.Place{{Kind: picker.ProjectKind, Path: path, Group: root, DisplayRemainder: "link "}},
		DeleteFingerprint: func(_ context.Context, value string) (string, error) {
			if value != path {
				t.Fatalf("captured path = %q, want %q", value, path)
			}
			return "selected", nil
		},
		DeletePreflight: func(_ context.Context, value, identity string) (deletion.Plan, error) {
			if value != path || identity != "selected" {
				t.Fatalf("preflight = %q identity %q", value, identity)
			}
			return plan, nil
		},
		DeleteProjectPlan: func(_ context.Context, value deletion.Plan, permanent, force bool) error {
			gotPath, gotPermanent, gotForce = value.Path, permanent, force
			return nil
		},
	}, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	updated, preflight := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model = updateModel(t, model, preflight())
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	for range 2 {
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
	if gotPath != path || !gotPermanent || gotForce {
		t.Fatalf("delete callback = %q permanent=%v force=%v", gotPath, gotPermanent, gotForce)
	}
}

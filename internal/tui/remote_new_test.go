package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

func remoteNewDeps() Deps {
	return Deps{
		Containers:     []capabilities.Container{{ID: "local-one", Name: "local-one", Machine: picker.LocalMachine()}},
		RemoteMachines: []string{"dev", "down"},
	}
}

func loadRemoteMachine(t *testing.T, model Model, machine, backend string) Model {
	t.Helper()
	return updateModel(t, model, picker.RemoteBatch{
		Machine: machine,
		Backend: backend,
		Places:  picker.ContainerPlaces([]capabilities.Container{{ID: "$1", Name: "api", Machine: machine}}),
	})
}

func newRowFor(model Model, id string) (row, bool) {
	for _, candidate := range model.projection().Rows(ContainersTab) {
		if candidate.id == id {
			return candidate, true
		}
	}
	return row{}, false
}

// TestRemoteNewRowCarriesResolvedBackend pins the projection contract: a
// successfully loaded reachable machine gets one selectable new-container row
// that carries its resolved multiplexer kind, and the local machine does not.
func TestRemoteNewRowCarriesResolvedBackend(t *testing.T) {
	model := loadRemoteMachine(t, NewModel(context.Background(), remoteNewDeps(), ContainersTab), "dev", "tmux")
	row, ok := newRowFor(model, "new:dev")
	if !ok {
		t.Fatalf("loaded remote machine has no new-container row: %v", model.Rows())
	}
	if row.kind != newContainerRow || !row.cursorable || row.backend != "tmux" || row.machine != "dev" {
		t.Fatalf("new row = %#v, want cursorable tmux row", row)
	}
	if label := shell.Plain(row.label); label != newContainerLabel {
		t.Fatalf("new row label = %q, want %q", label, newContainerLabel)
	}
	if _, ok := newRowFor(model, "new:"+picker.LocalMachine()); ok {
		t.Fatalf("local machine grew a new-container row: %v", model.Rows())
	}
}

// TestRemoteNewRowHiddenWhileLoadingAndAfterError is the reachability rule: a
// machine still loading, a machine whose listing failed, and a successful
// listing that resolved no backend all offer no row.
func TestRemoteNewRowHiddenWhileLoadingAndAfterError(t *testing.T) {
	loading := NewModel(context.Background(), remoteNewDeps(), ContainersTab)
	if _, ok := newRowFor(loading, "new:dev"); ok {
		t.Fatal("loading machine exposed a new-container row")
	}
	failed := updateModel(t, loadRemoteMachine(t, loading, "down", "tmux"), picker.RemoteBatch{Machine: "down", Error: "offline"})
	if _, ok := newRowFor(failed, "new:down"); ok {
		t.Fatal("failed machine exposed a new-container row")
	}
	unknown := loadRemoteMachine(t, NewModel(context.Background(), remoteNewDeps(), ContainersTab), "dev", "")
	if _, ok := newRowFor(unknown, "new:dev"); ok {
		t.Fatal("listing without a resolved backend exposed a new-container row")
	}
}

// TestRemoteNewRowUsesQueryAsName covers the non-empty half of the name flow:
// the current query is the proposed name and Enter runs the action directly
// without a second surface.
func TestRemoteNewRowUsesQueryAsName(t *testing.T) {
	var gotTarget RemoteTarget
	var gotName string
	deps := remoteNewDeps()
	deps.NewRemoteContainer = func(_ context.Context, target RemoteTarget, name string) error {
		gotTarget, gotName = target, name
		return nil
	}
	model := loadRemoteMachine(t, NewModel(context.Background(), deps, ContainersTab), "dev", "tmux")
	model.SetQuery("feature-api")
	model.input.cursor = "new:dev"
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.input.form != nil {
		t.Fatalf("query Enter opened a form, want a direct action")
	}
	if model.input.selectedRemoteTarget == nil || model.input.selectedRemoteTarget.Backend != "tmux" || model.input.selectedRemoteName != "feature-api" {
		t.Fatalf("selected = %#v %q", model.input.selectedRemoteTarget, model.input.selectedRemoteName)
	}
	if err := model.execute(context.Background()); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if gotTarget != (RemoteTarget{Machine: "dev", Backend: "tmux"}) || gotName != "feature-api" {
		t.Fatalf("action = %#v %q, want dev/tmux feature-api", gotTarget, gotName)
	}
}

// TestRemoteNewRowPromptsWhenQueryEmpty covers the empty half: an inline name
// prompt reuses the staged form, validates, and then runs the same action.
func TestRemoteNewRowPromptsWhenQueryEmpty(t *testing.T) {
	var gotTarget RemoteTarget
	var gotName string
	deps := remoteNewDeps()
	deps.NewRemoteContainer = func(_ context.Context, target RemoteTarget, name string) error {
		gotTarget, gotName = target, name
		return nil
	}
	model := loadRemoteMachine(t, NewModel(context.Background(), deps, ContainersTab), "dev", "herdr")
	model.input.cursor = "new:dev"
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.input.form == nil || formValue(model.input.form, "name") != "" {
		t.Fatalf("empty query did not open an empty name prompt: %#v", model.input.form)
	}
	if updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || updated.(Model).input.form.stageError != "name is required" {
		t.Fatal("blank name did not stay inline")
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feature")})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("valid name did not submit")
	}
	model = updateModel(t, model, cmd())
	if err := model.execute(context.Background()); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if gotTarget != (RemoteTarget{Machine: "dev", Backend: "herdr"}) || gotName != "feature" {
		t.Fatalf("action = %#v %q, want dev/herdr feature", gotTarget, gotName)
	}
}

// TestRemoteNewRowRendersAsMutedAction keeps the row a single unselected line
// that only paints a background when the cursor is on it.
func TestRemoteNewRowRendersAsMutedAction(t *testing.T) {
	model := loadRemoteMachine(t, NewModel(context.Background(), remoteNewDeps(), ContainersTab), "dev", "tmux")
	model.SetSize(100, 14)
	plain := shell.Plain(model.View())
	if !strings.Contains(plain, newContainerLabel) {
		t.Fatalf("view missing the new-container row: %q", plain)
	}
	model.input.cursor = "new:dev"
	model.SetSize(100, 14)
	noBackgroundSGR(t, model.View())
}

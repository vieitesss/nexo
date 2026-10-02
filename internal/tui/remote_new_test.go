package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

func remoteNewDeps() Deps {
	return Deps{
		Containers:     []capabilities.Container{{ID: "local-one", Name: "local-one", Machine: containerstate.LocalMachine()}},
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

func remoteNewPlaces(machine string, names ...string) []picker.Place {
	containers := make([]capabilities.Container, 0, len(names))
	for index, name := range names {
		containers = append(containers, capabilities.Container{ID: machine + "-" + string(rune('a'+index)), Name: name, Machine: machine})
	}
	return picker.ContainerPlaces(containers)
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
	if _, ok := newRowFor(model, "new:"+containerstate.LocalMachine()); ok {
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

// TestRemoteNewRowRefinementPrefersRealMatch is the refinement regression: a
// query edit that drops the selected container while another real match
// remains must keep the cursor on that match. The synthetic creation row sits
// right after it and must never absorb Enter.
func TestRemoteNewRowRefinementPrefersRealMatch(t *testing.T) {
	created := false
	deps := remoteNewDeps()
	deps.OpenContainer = func(context.Context, picker.Place) error { return nil }
	deps.NewRemoteContainer = func(context.Context, RemoteTarget, string) error {
		created = true
		return nil
	}
	model := NewModel(context.Background(), deps, ContainersTab)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Backend: "tmux", Places: remoteNewPlaces("dev", "alpha", "alpine")})
	model.SetQuery("a")
	model.input.cursor = containerIdentity(picker.Place{Machine: "dev", ContainerID: "dev-b"})
	model.SetQuery("alph")

	want := containerIdentity(picker.Place{Machine: "dev", ContainerID: "dev-a"})
	if got := model.Cursor(); got != want {
		t.Fatalf("cursor after refinement = %q, want alpha %q; rows=%v", got, want, model.Rows())
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("enter did not accept the refined match")
	}
	if model.input.selectedRemoteTarget != nil {
		t.Fatal("enter proposed remote creation after refinement")
	}
	if err := model.execute(context.Background()); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if created {
		t.Fatal("refinement created a container instead of opening the match")
	}
}

// TestRemoteReloadHidesNewRowsUntilCurrentResult is the reload regression: a
// coordinated reload must hide every machine's creation row until that
// machine's own current-generation listing arrives, even when an interleaved
// local refresh restores the catalog snapshot.
func TestRemoteReloadHidesNewRowsUntilCurrentResult(t *testing.T) {
	dev := picker.RemoteBatch{Machine: "dev", Backend: "tmux", Places: remoteNewPlaces("dev", "dev-one")}
	gpu := picker.RemoteBatch{Machine: "gpu", Backend: "herdr", Places: remoteNewPlaces("gpu", "gpu-one")}
	deps := remoteNewDeps()
	deps.RemoteMachines = []string{"dev", "gpu"}
	deps.ContainerSource = fixedContainerSource{containers: deps.Containers}
	deps.RemoteCommands = []tea.Cmd{
		func() tea.Msg { return dev },
		func() tea.Msg { return gpu },
	}
	model := NewModel(context.Background(), deps, ContainersTab)
	model = updateModel(t, model, dev)
	model = updateModel(t, model, gpu)
	for _, machine := range []string{"dev", "gpu"} {
		if _, ok := newRowFor(model, "new:"+machine); !ok {
			t.Fatalf("precondition: %s new row missing: %v", machine, model.Rows())
		}
	}

	if cmd := model.source.Reload(); cmd == nil {
		t.Fatal("reload scheduled no commands")
	}
	model.rebuild(false)
	for _, machine := range []string{"dev", "gpu"} {
		if _, ok := newRowFor(model, "new:"+machine); ok {
			t.Fatalf("reload exposed new row for still-loading %s: %v", machine, model.Rows())
		}
	}

	// A local refresh completing mid-reload must not restore cached statuses.
	model = updateModel(t, model, containersMsg{generation: model.source.generations.containers, containers: deps.Containers})
	for _, machine := range []string{"dev", "gpu"} {
		if _, ok := newRowFor(model, "new:"+machine); ok {
			t.Fatalf("local refresh resurrected new row for still-loading %s: %v", machine, model.Rows())
		}
	}

	model = updateModel(t, model, remoteMsg{generation: model.source.generations.remotes, batch: picker.RemoteBatch{Machine: "dev", Backend: "tmux", Places: remoteNewPlaces("dev", "dev-one")}})
	if _, ok := newRowFor(model, "new:dev"); !ok {
		t.Fatalf("dev new row missing after its current result: %v", model.Rows())
	}
	if _, ok := newRowFor(model, "new:gpu"); ok {
		t.Fatalf("gpu new row exposed before its current result: %v", model.Rows())
	}

	model = updateModel(t, model, remoteMsg{generation: model.source.generations.remotes, batch: picker.RemoteBatch{Machine: "gpu", Backend: "herdr", Places: remoteNewPlaces("gpu", "gpu-one")}})
	if _, ok := newRowFor(model, "new:gpu"); !ok {
		t.Fatalf("gpu new row missing after its current result: %v", model.Rows())
	}
}

// TestRemoteNewFormRejectsLateSubmitAfterCancel is the cancel race: leaving
// the name form before the command result is delivered must invalidate that
// result so it cannot create the container or quit a later interface.
func TestRemoteNewFormRejectsLateSubmitAfterCancel(t *testing.T) {
	created := false
	deps := remoteNewDeps()
	deps.NewRemoteContainer = func(context.Context, RemoteTarget, string) error {
		created = true
		return nil
	}
	model := loadRemoteMachine(t, NewModel(context.Background(), deps, ContainersTab), "dev", "tmux")
	model.input.cursor = "new:dev"
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.input.form == nil {
		t.Fatal("empty query did not open the name form")
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feature")})
	updated, submit := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if submit == nil {
		t.Fatal("valid name did not submit")
	}
	late := submit()
	if late == nil {
		t.Fatal("submit command returned no message before cancellation")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.input.form != nil {
		t.Fatal("escape did not close the form")
	}
	if again := submit(); again != nil {
		t.Fatal("canceled command still produced a message")
	}
	updated, cmd := model.Update(late)
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("late message quit the model")
	}
	if model.input.selectedRemoteTarget != nil || model.Cancelled() {
		t.Fatalf("late submit ran the canceled action: target=%#v cancelled=%t", model.input.selectedRemoteTarget, model.Cancelled())
	}
	if created {
		t.Fatal("late submit created a container")
	}
}

// TestRemoteNewFormRejectsLateSubmitFromReplacedForm covers replacement: the
// result of a canceled form must not be accepted by the form that replaced it,
// even though both forms submitted.
func TestRemoteNewFormRejectsLateSubmitFromReplacedForm(t *testing.T) {
	model := loadRemoteMachine(t, NewModel(context.Background(), remoteNewDeps(), ContainersTab), "dev", "tmux")
	model.input.cursor = "new:dev"
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first")})
	updated, submitFirst := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	first := submitFirst()

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.input.form == nil {
		t.Fatal("replacement form did not open")
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second")})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	updated, cmd := model.Update(first)
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("late result from the replaced form quit the model")
	}
	if model.input.selectedRemoteTarget != nil || model.input.form == nil {
		t.Fatalf("replaced-form state disturbed: target=%#v form=%v", model.input.selectedRemoteTarget, model.input.form)
	}
}

// TestRemoteNewRowRejectsMultilineQueryName pins the shared one-line policy:
// a multiline query-derived name must open the form with the error inline for
// every resolved backend instead of quitting and letting the backend reject
// the name after the TUI is gone.
func TestRemoteNewRowRejectsMultilineQueryName(t *testing.T) {
	for _, backend := range []string{"tmux", "herdr"} {
		t.Run(backend, func(t *testing.T) {
			model := loadRemoteMachine(t, NewModel(context.Background(), remoteNewDeps(), ContainersTab), "dev", backend)
			model.input.cursor = "new:dev"
			model.SetQuery("alpha\nbeta")
			model.input.cursor = "new:dev"
			updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
			model = updated.(Model)
			if cmd != nil {
				t.Fatal("multiline query quit the model")
			}
			if model.input.selectedRemoteTarget != nil {
				t.Fatal("multiline query selected a remote target")
			}
			if model.input.form == nil {
				t.Fatal("multiline query did not open the name form")
			}
			if got := model.input.form.stageError; got != "name must be one line" {
				t.Fatalf("stage error = %q, want the one-line policy", got)
			}
			if got := formValue(model.input.form, "name"); got != "alpha\nbeta" {
				t.Fatalf("prefilled name = %q", got)
			}
		})
	}
}

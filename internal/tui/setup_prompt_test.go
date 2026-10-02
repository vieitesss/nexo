package tui

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/shell"
)

func TestSetupPromptViewFitsNarrowWindowAndWrapsErrors(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Title: "bindings", Fields: []formField{{ID: "key", Label: "TUI key", Kind: formText, Error: "choose a valid binding key or leave this field blank"}}}}, nil, nil)
	model := setupPromptModel{form: form}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 32, Height: 10})
	model = updated.(setupPromptModel)
	view := strings.TrimSuffix(model.View(), "\n")
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 32 {
			t.Fatalf("line width = %d, want <= 32: %q", width, line)
		}
	}
	if !strings.Contains(view, "    choose a valid") {
		t.Fatalf("error lost its indentation: %q", view)
	}
	if !strings.Contains(view, "…") {
		t.Fatalf("narrow footer did not use help ellipsis: %q", view)
	}
}

func TestSetupViewportKeepsChecklistCursorAndFooter(t *testing.T) {
	options := make([]string, 30)
	for index := range options {
		options[index] = fmt.Sprintf("alias%02d", index+1)
	}
	form := newStagedForm(context.Background(), []formStage{{ID: "hosts", Title: "remote machines", Fields: []formField{{ID: "hosts", Label: "SSH aliases", Kind: formMultiSelect, Options: options}}}}, nil, nil)
	model := setupPromptModel{form: form}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 15})
	model = updated.(setupPromptModel)
	view := strings.TrimSuffix(model.View(), "\n")
	if lines := strings.Split(view, "\n"); len(lines) > 15 {
		t.Fatalf("top checklist view has %d lines, want <= 15", len(lines))
	}
	plainView := shell.Plain(view)
	if !strings.Contains(plainView, "alias01") || !strings.Contains(plainView, "enter submit") {
		t.Fatalf("top checklist view = %q", view)
	}
	for index := 1; index < len(options); index++ {
		form.key(tea.KeyMsg{Type: tea.KeyDown})
	}
	view = strings.TrimSuffix(model.View(), "\n")
	if lines := strings.Split(view, "\n"); len(lines) > 15 {
		t.Fatalf("bottom checklist view has %d lines, want <= 15", len(lines))
	}
	plainView = shell.Plain(view)
	if !strings.Contains(plainView, "alias30") || !strings.Contains(plainView, "enter submit") {
		t.Fatalf("bottom checklist view = %q", view)
	}
}

func TestSetupPromptShowsTypedValueWhenItFitsAtEightyColumns(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Title: "bindings", Fields: []formField{{ID: "key", Label: "TUI key (blank keeps opt-in)", Kind: formText}}}}, nil, nil)
	model := setupPromptModel{form: form}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	model = updated.(setupPromptModel)
	for _, r := range "M-g" {
		form.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if view := model.View(); !strings.Contains(view, "M-g") {
		t.Fatalf("typed value hidden at 80 columns: %q", view)
	}
}

func TestPromptSetupCancelReturnsSetupCanceled(t *testing.T) {
	_, err := PromptSetup(context.Background(), bytes.NewBufferString("\x1b"), &bytes.Buffer{}, SetupPromptDefaults{Backend: "tmux", LocalOnly: true})
	if err == nil || err.Error() != "setup canceled" {
		t.Fatalf("setup cancel error = %v, want setup canceled", err)
	}
}

func TestPromptRemoteBackendsCancelReturnsSetupCanceled(t *testing.T) {
	_, err := PromptRemoteBackends(context.Background(), bytes.NewBufferString("\x1b"), &bytes.Buffer{}, map[string]string{"dev": "tmux"})
	if err == nil || err.Error() != "setup canceled" {
		t.Fatalf("remote backend cancel error = %v, want setup canceled", err)
	}
}

func TestPromptSetupAbbreviatesHomePathDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	values, err := PromptSetup(context.Background(), bytes.NewBufferString("\r\r\r\r\r"), &bytes.Buffer{}, SetupPromptDefaults{
		Backend:   "tmux",
		LocalOnly: true,
		Paths:     []string{filepath.Join(home, "src")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(values.Paths, ", "); got != "~/src" {
		t.Fatalf("setup paths = %q, want ~/src", got)
	}
}

func TestSetupBindingErrorsRecomputeWhenBackendChanges(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{
		{ID: "backend", Fields: []formField{{ID: "backend", Kind: formChoice, Default: "tmux", Options: []string{"tmux", "herdr"}}}},
		{ID: "bindings", Fields: []formField{{ID: "tui-key", Kind: formText, Default: "Return", Validate: setupBindingValidator("tmux")}, {ID: "agents-key", Kind: formText, Default: ""}}},
	}, func(f *stagedForm) formValidation { return validateSetupForm(f, "tmux") }, nil)
	form.stage = 1
	if form.applyValidation() || form.stages[1].Fields[0].Error == "" || form.stages[1].Fields[0].input.Err == nil {
		t.Fatalf("tmux validation = %#v, want synchronized error", form.stages[1].Fields[0])
	}
	form.stage = 0
	form.key(tea.KeyMsg{Type: tea.KeyRight})
	form.stage = 1
	if !form.applyValidation() || form.stages[1].Fields[0].Error != "" || form.stages[1].Fields[0].input.Err != nil {
		t.Fatalf("herdr validation = %#v, want cleared error", form.stages[1].Fields[0])
	}
	form.stage = 0
	form.key(tea.KeyMsg{Type: tea.KeyLeft})
	form.stage = 1
	if form.applyValidation() || form.stages[1].Fields[0].Error == "" || form.stages[1].Fields[0].input.Err == nil {
		t.Fatalf("tmux revalidation = %#v, want error restored", form.stages[1].Fields[0])
	}
}

func TestSetupBindingPresentationFollowsBackend(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{
		{ID: "backend", Fields: []formField{{ID: "backend", Kind: formChoice, Default: "tmux", Options: []string{"tmux", "herdr"}}}},
		{ID: "bindings", Title: bindingTitle("tmux"), Fields: []formField{{ID: "tui-key", Kind: formText, Placeholder: "C-s"}, {ID: "agents-key", Kind: formText, Placeholder: "a"}}},
	}, nil, nil)
	model := setupPromptModel{form: form, defaultBackend: "tmux"}
	model.refreshBindingPresentation()
	model.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := form.stages[1].Title; got != bindingTitle("herdr") {
		t.Fatalf("binding title = %q, want herdr title", got)
	}
	if got := form.stages[1].Fields[0].input.Placeholder; got != "ctrl+s" {
		t.Fatalf("binding placeholder = %q, want native suggestion", got)
	}
	if got := form.stages[1].Fields[1].input.Placeholder; got != "prefix+a" {
		t.Fatalf("agents placeholder = %q, want native suggestion", got)
	}
}

func TestSetupForwardsInitialFocusCommand(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{{ID: "name", Kind: formText}}}}, nil, nil)
	model := setupPromptModel{form: form}
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("Init returned no focus command")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("focus command returned no message")
	}
	updated, _ := model.Update(msg)
	if !updated.(setupPromptModel).form.currentField().input.Focused() {
		t.Fatal("focused textinput did not receive command result")
	}
}

func TestPromptSetupBuildsValuesFromChoicesAndHosts(t *testing.T) {
	// Enter accepts each stage. Tab reaches the second binding and optional
	// alias fields because Enter advances a staged form; Space selects the
	// currently highlighted machine in the checklist.
	input := bytes.NewBufferString("\r\r\r\t\r\talias\r\r")
	values, err := PromptSetup(context.Background(), input, &bytes.Buffer{}, SetupPromptDefaults{
		Backend:        "tmux",
		Notify:         true,
		Paths:          []string{"/work"},
		TUIKey:         "C-s",
		AgentsKey:      "a",
		Hosts:          []string{"dev", "staging"},
		SelectedHosts:  []string{"staging"},
		RemoteBackends: map[string]string{"dev": "herdr"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := SetupPromptValues{
		Backend:        "tmux",
		Notify:         true,
		TUIKey:         "C-s",
		AgentsKey:      "a",
		Hosts:          []string{"staging", "alias"},
		Paths:          []string{"/work"},
		RemoteBackends: map[string]string{"dev": "herdr"},
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("setup values = %#v, want %#v", values, want)
	}
}

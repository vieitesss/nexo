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

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/shell"
)

func TestSetupPromptViewFitsNarrowWindowAndWrapsErrors(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Title: "bindings", Fields: []formField{{ID: "key", Label: "TUI key", Kind: formText, Error: "choose a valid binding key or leave this field blank"}}}}, nil, nil)
	model := setupPromptModel{form: form}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 20, Height: 10})
	model = updated.(setupPromptModel)
	view := strings.TrimSuffix(model.View(), "\n")
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 20 {
			t.Fatalf("line width = %d, want <= 20: %q", width, line)
		}
	}
	if !strings.Contains(shell.Plain(view), "    choose a valid") {
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

func TestStagedFormAlignsCurrentStageExamples(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{
		{Title: "Project roots", Examples: []formExample{{Input: "~/src", Meaning: "direct"}, {Input: "~/work/*", Meaning: "deeper"}}, Fields: []formField{{ID: "paths", Kind: formText}}},
		{Title: "Agents", Examples: []formExample{{Input: "hidden", Meaning: "later stage"}}},
	}, nil, nil)
	view := shell.Plain(strings.Join(form.renderLines(), "\n"))
	for _, want := range []string{"    ~/src     direct", "    ~/work/*  deeper"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "later stage") {
		t.Fatalf("pending stage showed its examples:\n%s", view)
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

func TestPromptSetupSubmitsTheRexBackend(t *testing.T) {
	values, err := PromptSetup(context.Background(), bytes.NewBufferString("\r\r\r\r\r"), &bytes.Buffer{}, SetupPromptDefaults{
		Backend:   "rex",
		LocalOnly: true,
		Paths:     []string{"/work"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if values.Backend != "rex" {
		t.Fatalf("setup backend = %q, want rex", values.Backend)
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
		{ID: "bindings", Examples: bindingExamples("tmux"), Fields: []formField{{ID: "tui-key", Kind: formText, Placeholder: "C-s"}, {ID: "agents-key", Kind: formText, Placeholder: "prefix+a"}}},
	}, nil, nil)
	model := setupPromptModel{form: form, defaultBackend: "tmux"}
	model.refreshBindingPresentation()
	model.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := form.stages[1].Examples; !reflect.DeepEqual(got, bindingExamples("herdr")) {
		t.Fatalf("binding examples = %#v, want herdr spelling", got)
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
		Bindings:       map[string]SetupBindings{"tmux": {TUIKey: "C-s", AgentsKey: "prefix+a"}},
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
		AgentsKey:      "prefix+a",
		Hosts:          []string{"staging", "alias"},
		Paths:          []string{"/work"},
		RemoteBackends: map[string]string{"dev": "herdr"},
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("setup values = %#v, want %#v", values, want)
	}
}

func TestRemoteBackendStageAlwaysStartsOnAnOption(t *testing.T) {
	for backend, want := range map[string]string{"": "tmux", "herdr": "herdr", " Herdr ": "herdr", "tmux": "tmux"} {
		if got := remoteBackendStage("pi", backend).Fields[0].Default; got != want {
			t.Fatalf("remoteBackendStage(%q) default = %q, want %q", backend, got, want)
		}
	}
}

func TestPromptSetupSwitchesBindingsWithTheBackend(t *testing.T) {
	bindings := map[string]SetupBindings{"tmux": {TUIKey: "C-s", AgentsKey: "prefix+a"}, "herdr": {TUIKey: "ctrl+g", AgentsKey: "prefix+n"}}
	for name, test := range map[string]struct {
		keys string
		want SetupPromptValues
	}{
		"to herdr": {"\x1b[C", SetupPromptValues{Backend: "herdr", TUIKey: "ctrl+g", AgentsKey: "prefix+n"}},
		"and back": {"\x1b[C\x1b[D", SetupPromptValues{Backend: "tmux", TUIKey: "C-s", AgentsKey: "prefix+a"}},
	} {
		t.Run(name, func(t *testing.T) {
			values, err := PromptSetup(context.Background(), bytes.NewBufferString(test.keys+"\r\r\r\r\r"), &bytes.Buffer{}, SetupPromptDefaults{Backend: "tmux", LocalOnly: true, Paths: []string{"/work"}, Bindings: bindings})
			if err != nil {
				t.Fatal(err)
			}
			if values.Backend != test.want.Backend || values.TUIKey != test.want.TUIKey || values.AgentsKey != test.want.AgentsKey {
				t.Fatalf("values = %s %q %q, want %s %q %q", values.Backend, values.TUIKey, values.AgentsKey, test.want.Backend, test.want.TUIKey, test.want.AgentsKey)
			}
		})
	}
}

func TestBindingSwitchKeepsATypedKey(t *testing.T) {
	model := setupPromptModel{
		form:           newStagedForm(context.Background(), []formStage{{ID: "backend", Fields: []formField{{ID: "backend", Default: "tmux", Kind: formChoice, Options: []string{"tmux", "herdr"}}}}, {ID: "bindings", Fields: []formField{{ID: "tui-key", Default: "M-g", Kind: formText}, {ID: "agents-key", Default: "prefix+a", Kind: formText}}}}, nil, nil),
		defaultBackend: "tmux",
		bindings:       &shownBindings{backend: "tmux", byBackend: map[string]SetupBindings{"tmux": {TUIKey: "C-s", AgentsKey: "prefix+a"}, "herdr": {TUIKey: "ctrl+g"}}},
	}
	model.form.stages[0].Fields[0].setValue("herdr")
	model.refreshBindingPresentation()
	values := model.form.values()
	if got := values.String("tui-key"); got != "M-g" {
		t.Fatalf("typed TUI key = %q, want it kept", got)
	}
	if got := values.String("agents-key"); got != "" {
		t.Fatalf("agents key = %q, want herdr's (unset)", got)
	}
}

func TestRexSetupPromptUsesRexKeyGrammar(t *testing.T) {
	tuiKey, agentsKey := bindingPlaceholders("rex")
	for _, key := range []string{tuiKey, agentsKey} {
		if err := config.ValidateBindingKeyForBackend("rex", key); err != nil {
			t.Fatalf("Rex placeholder %q is invalid: %v", key, err)
		}
	}
	for _, example := range bindingExamples("rex") {
		if strings.Contains(example.Input, "prefix+") {
			t.Fatalf("Rex example uses a Herdr binding: %+v", example)
		}
	}
	if !strings.Contains(bindingPrompt("rex"), "ctrl+space>") {
		t.Fatalf("Rex binding prompt = %q", bindingPrompt("rex"))
	}
	form := newStagedForm(context.Background(), []formStage{
		{ID: "backend", Fields: []formField{{ID: "backend", Kind: formChoice, Options: []string{"tmux", "herdr"}, Default: "rex"}}},
		{ID: "bindings", Fields: []formField{{ID: "tui-key", Kind: formText, Default: "ctrl+space>o"}, {ID: "agents-key", Kind: formText, Default: "ctrl+space>a"}}},
	}, nil, nil)
	if got := validateSetupForm(form, "rex"); got.Error != "" {
		t.Fatalf("Rex setup form error = %q", got.Error)
	}
	bad := newStagedForm(context.Background(), []formStage{
		{ID: "backend", Fields: []formField{{ID: "backend", Kind: formChoice, Options: []string{"tmux", "herdr"}, Default: "rex"}}},
		{ID: "bindings", Fields: []formField{{ID: "tui-key", Kind: formText, Default: "prefix+a"}}},
	}, nil, nil)
	bad.stage = 1
	if got := validateSetupForm(bad, "rex"); got.Error == "" {
		t.Fatal("Rex setup form accepted a Herdr binding")
	}
}

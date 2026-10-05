package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/shell"
)

func TestStagedFormKeepsInvalidDefaultUntilTextChanges(t *testing.T) {
	defaultValue := "alpha\nbeta"
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{{ID: "name", Kind: formText, Default: defaultValue}}}}, nil, nil)
	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyLeft},
		{Type: tea.KeyHome},
		{Type: tea.KeyCtrlV},
	} {
		form.key(msg)
		if got := formValue(form, "name"); got != defaultValue {
			t.Fatalf("after %s value = %q, want invalid default %q", msg.String(), got, defaultValue)
		}
	}
	form.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if got := formValue(form, "name"); got == defaultValue {
		t.Fatal("typing without changing text kept invalid default")
	}
}

func TestStagedFormForwardsComponentCommandResult(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{{ID: "name", Kind: formText}}}}, nil, nil)
	cmd := form.initialFocusCmd()
	if cmd == nil {
		t.Fatal("initial focus returned no command")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("focus command returned no message")
	}
	if effect := form.updateMsg(msg); !effect.Changed {
		t.Fatal("component command result was not forwarded")
	}
	if !form.currentField().input.Focused() {
		t.Fatal("textinput did not remain focused after command result")
	}
}

func TestStagedFormShowsCursorWhileEditing(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{{ID: "name", Kind: formText}}}}, nil, nil)
	if form.currentField().input.Cursor.Blink {
		t.Fatal("focused field starts with its cursor hidden")
	}
	form.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if form.currentField().input.Cursor.Blink {
		t.Fatal("typing hid the cursor")
	}
}

func TestStagedFormKeepsErrorAcrossCursorBlink(t *testing.T) {
	validate := func(form *stagedForm) formValidation {
		if formValue(form, "name") == "" {
			return formValidation{Error: "name is required"}
		}
		return formValidation{}
	}
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{{ID: "name", Kind: formText}}}}, validate, nil)
	cmd := form.initialFocusCmd()
	form.key(tea.KeyMsg{Type: tea.KeyEnter})
	if form.stageError == "" {
		t.Fatal("Enter on an empty field set no error")
	}
	form.updateMsg(cmd())
	if form.stageError == "" {
		t.Fatal("cursor blink cleared the validation error")
	}
	form.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if form.stageError != "" {
		t.Fatalf("typing kept error %q", form.stageError)
	}
}

func TestStagedFormIgnoresStaleComponentCommandResult(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{{ID: "name", Kind: formText}}}}, nil, nil)
	cmd := form.initialFocusCmd()
	form.nextGeneration()
	if cmd == nil {
		t.Fatal("initial focus command did not return a command")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("initial focus command did not return a result")
	}
	if effect := form.updateMsg(msg); effect.Changed {
		t.Fatal("stale component result changed replacement generation")
	}
}

func TestStagedFormTextUsesTextInputEditing(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{{ID: "name", Label: "name", Kind: formText, Default: "hello world", Placeholder: "name"}}}}, nil, nil)
	if effect := form.key(tea.KeyMsg{Type: tea.KeyCtrlW}); !effect.Changed {
		t.Fatal("ctrl+w did not edit text")
	}
	if got := formValue(form, "name"); !strings.HasPrefix(got, "hello") {
		t.Fatalf("word delete value = %q", got)
	}
	if form.currentField().input.Focused() == false {
		t.Fatal("text input is not focused")
	}
}

func TestStagedFormChoiceRendersAllOptionsInline(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{{ID: "backend", Label: "backend", Kind: formChoice, Default: "tmux", Options: []string{"tmux", "herdr"}}}}}, nil, nil)
	view := strings.Join(form.renderLines(), "\n")
	plain := shell.Plain(view)
	if !strings.Contains(plain, "backend: (x) tmux  ( ) herdr") {
		t.Fatalf("choice view = %q, want selected and alternative", view)
	}
	if strings.Contains(view, "48;") {
		t.Fatalf("choice view paints a background: %q", view)
	}
}

func TestStagedFormChoiceCyclesWithArrowKeys(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{{ID: "backend", Label: "backend", Kind: formChoice, Default: "tmux", Options: []string{"tmux", "herdr"}}}}}, nil, nil)
	form.key(tea.KeyMsg{Type: tea.KeyRight})
	if got := formValue(form, "backend"); got != "herdr" {
		t.Fatalf("choice = %q, want herdr", got)
	}
	form.key(tea.KeyMsg{Type: tea.KeyLeft})
	if got := formValue(form, "backend"); got != "tmux" {
		t.Fatalf("choice = %q, want tmux", got)
	}
}

func TestStagedFormToggleAndMultiSelect(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{
		{ID: "notify", Label: "notify", Kind: formToggle, Default: "no"},
		{ID: "hosts", Label: "hosts", Kind: formMultiSelect, Options: []string{"one", "two"}},
	}}}, nil, nil)
	form.key(tea.KeyMsg{Type: tea.KeySpace})
	if got := formValue(form, "notify"); got != "yes" {
		t.Fatalf("toggle = %q, want yes", got)
	}
	form.key(tea.KeyMsg{Type: tea.KeyTab})
	form.key(tea.KeyMsg{Type: tea.KeySpace})
	if got := strings.Join(form.values().List("hosts"), ","); got != "one" {
		t.Fatalf("multi-select = %q, want one", got)
	}
	form.key(tea.KeyMsg{Type: tea.KeyDown})
	form.key(tea.KeyMsg{Type: tea.KeySpace})
	if got := strings.Join(form.values().List("hosts"), ","); got != "one,two" {
		t.Fatalf("multi-select values = %q, want one,two", got)
	}
}

func TestStagedFormValuesAndSummaryShareSelectedOptions(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{{ID: "hosts", Label: "hosts", Kind: formMultiSelect, Options: []string{"one", "two"}, InitialSelected: []string{"two"}}}}}, nil, nil)
	values := form.values().List("hosts")
	if strings.Join(values, ",") != "two" {
		t.Fatalf("selected values = %#v, want [two]", values)
	}
	field := form.stages[0].Fields[0]
	if got := form.fieldStringValue(field); got != "two" || form.summaryFieldValue(field) != got {
		t.Fatalf("selected summary = %q, field value = %q", form.summaryFieldValue(field), got)
	}
}

func TestStagedFormValidationBlocksSubmit(t *testing.T) {
	submitted := false
	form := newStagedForm(context.Background(), []formStage{{Fields: []formField{{ID: "name", Label: "name", Kind: formText}}}}, func(form *stagedForm) formValidation {
		if strings.TrimSpace(formValue(form, "name")) == "" {
			return formValidation{Error: "name is required"}
		}
		return formValidation{}
	}, func(context.Context, formValues, uint64) tea.Cmd {
		submitted = true
		return nil
	})
	if effect := form.key(tea.KeyMsg{Type: tea.KeyEnter}); !effect.Changed || effect.Submitted {
		t.Fatalf("invalid form effect = %#v", effect)
	}
	if submitted || form.stageError != "name is required" {
		t.Fatalf("invalid form submitted=%v error=%q", submitted, form.stageError)
	}
}

func TestStagedFormSkipsDetectedStage(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{
		{ID: "backend", Title: "Backend", Detected: "the only one installed", Fields: []formField{{ID: "backend", Kind: formChoice, Default: "tmux", Options: []string{"tmux", "herdr"}}}},
		{ID: "notify", Title: "Agents", Fields: []formField{{ID: "notify", Kind: formToggle, Default: "yes"}}},
	}, nil, nil)
	form.title = "nexo setup"
	if form.stage != 1 {
		t.Fatalf("form starts on stage %d, want the first asked stage", form.stage)
	}
	view := shell.Plain(strings.Join(form.renderLines(), "\n"))
	for _, want := range []string{"nexo setup  step 1 of 1", "✓ Backend  tmux  the only one installed", "❯ Agents"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "shift+tab") {
		t.Fatalf("footer offers going back to a detected stage:\n%s", view)
	}
	form.key(tea.KeyMsg{Type: tea.KeyShiftTab})
	if form.stage != 1 {
		t.Fatalf("shift+tab moved to detected stage %d", form.stage)
	}
	if got := formValue(form, "backend"); got != "tmux" {
		t.Fatalf("detected value = %q, want tmux", got)
	}
}

func TestStagedFormWrapsExampleMeaningsUnderTheirColumn(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Title: "Key bindings", Examples: bindingExamples("tmux"), Fields: []formField{{ID: "tui-key", Kind: formText}}}}, nil, nil)
	form.setViewportWidth(48)
	lines := form.exampleLines(form.stages[0].Examples)
	column := strings.Index(shell.Plain(lines[0]), "Ctrl-s")
	if len(lines) < 3 {
		t.Fatalf("long meaning did not wrap at 48 columns: %q", lines)
	}
	for _, line := range lines {
		plain := shell.Plain(line)
		if width := shell.StringWidth(plain); width > 48 {
			t.Fatalf("example line is %d wide, want <= 48: %q", width, plain)
		}
		continuation := !strings.HasPrefix(plain, "    C-s") && !strings.HasPrefix(plain, "    prefix+a")
		if indent := len(plain) - len(strings.TrimLeft(plain, " ")); continuation && indent != column {
			t.Fatalf("continuation %q is indented %d, want the meaning column %d", plain, indent, column)
		}
	}
}

func TestStagedFormStacksExamplesWithNoRoomBesideThem(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Examples: []formExample{{Input: "~/work/*", Meaning: "one level deeper"}}, Fields: []formField{{ID: "paths", Kind: formText}}}}, nil, nil)
	form.setViewportWidth(24)
	got := plainLines(form.exampleLines(form.stages[0].Examples))
	if want := "    ~/work/*\n      one level deeper"; got != want {
		t.Fatalf("stacked examples = %q, want %q", got, want)
	}
}

func TestStagedFormSummaryPutsEachFieldOnItsOwnLineWhenNarrow(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{Title: "Key bindings", Fields: []formField{{ID: "tui-key", Label: "TUI key", Kind: formText, Default: "C-s"}, {ID: "agents-key", Label: "agents key", Kind: formText, Default: "prefix+a"}}}}, nil, nil)
	form.setViewportWidth(48)
	got := plainLines(form.summaryLines())
	if want := "✓ Key bindings  TUI key: C-s, agents key: prefix+a"; shell.StringWidth(want) <= 48 || got == want {
		t.Fatalf("summary = %q, want it split at 48 columns", got)
	}
	if want := "✓ Key bindings  TUI key: C-s\n                agents key: prefix+a"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	form.setViewportWidth(80)
	if got := plainLines(form.summaryLines()); got != "✓ Key bindings  TUI key: C-s, agents key: prefix+a" {
		t.Fatalf("wide summary = %q, want one line", got)
	}
}

func plainLines(lines []string) string {
	plain := make([]string, len(lines))
	for index, line := range lines {
		plain[index] = shell.Plain(line)
	}
	return strings.Join(plain, "\n")
}

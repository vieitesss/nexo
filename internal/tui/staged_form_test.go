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
	if !strings.Contains(plain, "backend: tmux") || !strings.Contains(plain, "herdr") {
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

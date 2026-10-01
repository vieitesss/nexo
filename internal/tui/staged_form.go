package tui

import (
	"context"
	"strings"

	"github.com/vieitesss/nexo/internal/deletion"

	tea "github.com/charmbracelet/bubbletea"
)

// formField and formStage are the small interface used by stagedForm. A form
// owns field editing and stage transitions; its caller supplies validation and
// the asynchronous submit command.
type formField struct {
	ID         string
	Label      string
	Value      string
	Error      string
	Expandable bool
	ReadOnly   bool
}

type formStage struct {
	ID     string
	Title  string
	Fields []formField
}

type formValidation struct {
	Error  string
	Notice string
	Values map[string]string
}

type formEffect struct {
	Cmd       tea.Cmd
	Changed   bool
	Canceled  bool
	Submitted bool
}

type stagedForm struct {
	ctx        context.Context
	stages     []formStage
	stage      int
	field      int
	inFlight   bool
	stageError string
	notice     string
	validate   func(*stagedForm) formValidation
	submit     func(context.Context, map[string]string, uint64) tea.Cmd
	cancel     context.CancelFunc
	generation uint64
	busyLabel  string
	expanded   map[string]bool

	deletePreflightEnabled bool
	deletePreflightPending bool
	deleteIdentity         string
	deleteIdentityValid    bool
	deletePlan             *deletion.Plan
}

func newStagedForm(ctx context.Context, stages []formStage, validate func(*stagedForm) formValidation, submit func(context.Context, map[string]string, uint64) tea.Cmd) *stagedForm {
	if ctx == nil {
		ctx = context.Background()
	}
	copied := make([]formStage, len(stages))
	for index, stage := range stages {
		copied[index] = stage
		copied[index].Fields = append([]formField(nil), stage.Fields...)
	}
	return &stagedForm{ctx: ctx, stages: copied, validate: validate, submit: submit, busyLabel: "working…", expanded: make(map[string]bool)}
}

func (f *stagedForm) currentField() *formField {
	if f == nil || f.stage < 0 || f.stage >= len(f.stages) {
		return nil
	}
	fields := f.stages[f.stage].Fields
	if f.field < 0 || f.field >= len(fields) || !f.fieldVisible(f.field) {
		return nil
	}
	return &f.stages[f.stage].Fields[f.field]
}

func (f *stagedForm) fieldVisible(index int) bool {
	if f == nil || f.stage < 0 || f.stage >= len(f.stages) || index < 0 || index >= len(f.stages[f.stage].Fields) {
		return false
	}
	return f.fieldShown(f.stage, index)
}

func (f *stagedForm) fieldShown(stage, index int) bool {
	if f == nil || stage < 0 || stage >= len(f.stages) || index < 0 || index >= len(f.stages[stage].Fields) {
		return false
	}
	field := f.stages[stage].Fields[index]
	return !field.Expandable || f.expanded[field.ID]
}

func (f *stagedForm) moveField(delta int) formEffect {
	index := f.field
	for {
		index += delta
		if index < 0 || index >= len(f.stages[f.stage].Fields) {
			if delta < 0 {
				return formEffect{}
			}
			return f.advance()
		}
		if f.fieldVisible(index) {
			f.field = index
			return formEffect{Changed: true}
		}
	}
}

func (f *stagedForm) values() map[string]string {
	values := make(map[string]string)
	for _, stage := range f.stages {
		for _, field := range stage.Fields {
			values[field.ID] = field.Value
		}
	}
	return values
}

func (f *stagedForm) applyValidation() bool {
	if f.validate == nil {
		return true
	}
	result := f.validate(f)
	f.stageError, f.notice = result.Error, result.Notice
	for id, value := range result.Values {
		for stageIndex := range f.stages {
			for fieldIndex := range f.stages[stageIndex].Fields {
				if f.stages[stageIndex].Fields[fieldIndex].ID == id {
					f.stages[stageIndex].Fields[fieldIndex].Value = value
				}
			}
		}
	}
	return result.Error == ""
}

func (f *stagedForm) key(msg tea.KeyMsg) formEffect {
	if f == nil {
		return formEffect{}
	}
	// Escape is a form action even while the backend operation is running.
	// Cancel before dropping the form so a late command result cannot mutate a
	// newly opened form.
	if msg.Type == tea.KeyEsc {
		if f.cancel != nil {
			f.cancel()
			f.cancel = nil
		}
		f.generation++
		return formEffect{Canceled: true}
	}
	if f.inFlight {
		return formEffect{}
	}
	field := f.currentField()
	switch msg.Type {
	case tea.KeyTab:
		return f.nextField()
	case tea.KeyCtrlP:
		for index, candidate := range f.stages[f.stage].Fields {
			if !candidate.Expandable {
				continue
			}
			f.expanded[candidate.ID] = !f.expanded[candidate.ID]
			if f.expanded[candidate.ID] {
				f.field = index
			} else if !f.fieldVisible(f.field) {
				f.field = 0
			}
			return formEffect{Changed: true}
		}
	case tea.KeyRunes:
		if field != nil && !field.ReadOnly && len(msg.Runes) > 0 {
			field.Value += string(msg.Runes)
			field.Error = ""
			f.stageError = ""
			return formEffect{Changed: true}
		}
	case tea.KeySpace:
		if field != nil && !field.ReadOnly {
			field.Value += " "
			field.Error = ""
			f.stageError = ""
			return formEffect{Changed: true}
		}
	case tea.KeyBackspace, tea.KeyDelete:
		if field != nil && !field.ReadOnly && field.Value != "" {
			runes := []rune(field.Value)
			field.Value = string(runes[:len(runes)-1])
			field.Error = ""
			f.stageError = ""
			return formEffect{Changed: true}
		}
	case tea.KeyUp:
		return f.moveField(-1)
	case tea.KeyDown:
		return f.moveField(1)
	case tea.KeyLeft:
		if f.stage > 0 {
			f.stage--
			f.field = 0
			f.stageError = ""
			return formEffect{Changed: true}
		}
	case tea.KeyRight:
		return f.advance()
	case tea.KeyEnter:
		return f.advance()
	}
	if msg.String() == "shift+tab" {
		return f.previousField()
	}
	return formEffect{}
}

func (f *stagedForm) advance() formEffect {
	if !f.applyValidation() {
		return formEffect{Changed: true}
	}
	if f.stage+1 < len(f.stages) {
		f.stage++
		f.field = 0
		f.stageError = ""
		return formEffect{Changed: true}
	}
	f.inFlight = true
	f.generation++
	generation := f.generation
	ctx, cancel := context.WithCancel(f.ctx)
	f.cancel = cancel
	if f.submit == nil {
		return formEffect{Submitted: true}
	}
	return formEffect{Cmd: f.submit(ctx, f.values(), generation), Submitted: true}
}

func (f *stagedForm) nextField() formEffect { return f.moveField(1) }

func (f *stagedForm) previousField() formEffect {
	if f.field > 0 {
		for index := f.field - 1; index >= 0; index-- {
			if f.fieldVisible(index) {
				f.field = index
				return formEffect{Changed: true}
			}
		}
	}
	if f.stage > 0 {
		f.stage--
		f.field = len(f.stages[f.stage].Fields) - 1
		for !f.fieldVisible(f.field) && f.field > 0 {
			f.field--
		}
		f.stageError = ""
		return formEffect{Changed: true}
	}
	return formEffect{}
}

func (f *stagedForm) finish(err error) {
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
	f.inFlight = false
	if err != nil {
		f.stageError = err.Error()
	}
}

func (f stagedForm) renderLines() []string {
	lines := []string{}
	for stageIndex, stage := range f.stages {
		if stageIndex < f.stage {
			parts := []string{}
			for fieldIndex, field := range stage.Fields {
				if !f.fieldShown(stageIndex, fieldIndex) {
					continue
				}
				if field.Value != "" {
					parts = append(parts, field.Label+": "+field.Value)
				}
			}
			lines = append(lines, "✓ "+stage.Title+summaryJoin(parts))
			continue
		}
		if stageIndex > f.stage {
			lines = append(lines, "○ "+stage.Title)
			continue
		}
		lines = append(lines, "› "+stage.Title)
		for fieldIndex, field := range stage.Fields {
			if !f.fieldVisible(fieldIndex) {
				continue
			}
			prefix := "  "
			if fieldIndex == f.field {
				prefix = "▸ "
			}
			lines = append(lines, prefix+field.Label+": "+field.Value)
			if field.Error != "" {
				lines = append(lines, "    "+field.Error)
			}
		}
		if stageIndex == f.stage {
			if f.deletePlan != nil && f.deletePreflightEnabled && stage.ID == "preflight" {
				for _, finding := range f.deletePlan.Warnings {
					lines = append(lines, "  warning: "+finding.Message)
				}
				for _, finding := range f.deletePlan.Blockers {
					lines = append(lines, "  blocker: "+finding.Message)
				}
				if len(f.deletePlan.Warnings) == 0 && len(f.deletePlan.Blockers) == 0 {
					lines = append(lines, "  no warnings or blockers")
				}
			}
			for _, field := range stage.Fields {
				if field.Expandable && !f.expanded[field.ID] {
					lines = append(lines, "  Ctrl-P edit "+field.Label)
					break
				}
			}
		}
	}
	if f.stageError != "" {
		lines = append(lines, "! "+f.stageError)
	}
	if f.notice != "" {
		lines = append(lines, "? "+f.notice)
	}
	if f.inFlight {
		lines = append(lines, "  "+f.busyLabel)
	} else {
		lines = append(lines, "  Enter submit  Esc cancel")
	}
	return lines
}

func summaryJoin(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return " — " + strings.Join(parts, ", ")
}

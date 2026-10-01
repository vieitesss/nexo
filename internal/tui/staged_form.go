package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// formField and formStage are the small interface used by stagedForm. A form
// owns field editing and stage transitions; its caller supplies only validation
// and the asynchronous submit command.
type formField struct {
	ID    string
	Label string
	Value string
	Error string
}

type formStage struct {
	ID     string
	Title  string
	Fields []formField
}

type formEffect struct {
	Cmd       tea.Cmd
	Changed   bool
	Canceled  bool
	Submitted bool
}

type stagedForm struct {
	stages     []formStage
	stage      int
	field      int
	inFlight   bool
	stageError string
	notice     string
	validate   func(*stagedForm) string
	submit     func(map[string]string) tea.Cmd
}

func newStagedForm(stages []formStage, validate func(*stagedForm) string, submit func(map[string]string) tea.Cmd) *stagedForm {
	copied := make([]formStage, len(stages))
	for index, stage := range stages {
		copied[index] = stage
		copied[index].Fields = append([]formField(nil), stage.Fields...)
	}
	return &stagedForm{stages: copied, validate: validate, submit: submit}
}

func (f *stagedForm) currentField() *formField {
	if f == nil || f.stage < 0 || f.stage >= len(f.stages) {
		return nil
	}
	fields := f.stages[f.stage].Fields
	if f.field < 0 || f.field >= len(fields) {
		return nil
	}
	return &f.stages[f.stage].Fields[f.field]
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

func (f *stagedForm) key(msg tea.KeyMsg) formEffect {
	if f == nil {
		return formEffect{}
	}
	if f.inFlight {
		return formEffect{}
	}
	if msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEsc {
		return formEffect{Canceled: true}
	}
	field := f.currentField()
	switch msg.Type {
	case tea.KeyTab:
		return f.nextField()
	case tea.KeyRunes:
		if field != nil && len(msg.Runes) > 0 {
			field.Value += string(msg.Runes)
			field.Error = ""
			f.stageError = ""
			return formEffect{Changed: true}
		}
	case tea.KeySpace:
		if field != nil {
			field.Value += " "
			field.Error = ""
			f.stageError = ""
			return formEffect{Changed: true}
		}
	case tea.KeyBackspace, tea.KeyDelete:
		if field != nil && field.Value != "" {
			runes := []rune(field.Value)
			field.Value = string(runes[:len(runes)-1])
			field.Error = ""
			f.stageError = ""
			return formEffect{Changed: true}
		}
	case tea.KeyUp:
		if f.field > 0 {
			f.field--
			return formEffect{Changed: true}
		}
	case tea.KeyDown:
		if f.field+1 < len(f.stages[f.stage].Fields) {
			f.field++
			return formEffect{Changed: true}
		}
	case tea.KeyLeft:
		if f.stage > 0 {
			f.stage--
			f.field = 0
			f.stageError = ""
			return formEffect{Changed: true}
		}
	case tea.KeyRight:
		if f.stage+1 < len(f.stages) {
			f.stage++
			f.field = 0
			f.stageError = ""
			return formEffect{Changed: true}
		}
	case tea.KeyEnter:
		if f.validate != nil {
			if errText := f.validate(f); errText != "" {
				f.stageError = errText
				return formEffect{Changed: true}
			}
		}
		if f.stage+1 < len(f.stages) {
			f.stage++
			f.field = 0
			f.stageError = ""
			return formEffect{Changed: true}
		}
		f.inFlight = true
		if f.submit == nil {
			return formEffect{Submitted: true}
		}
		return formEffect{Cmd: f.submit(f.values()), Submitted: true}
	}
	if msg.String() == "shift+tab" {
		return f.previousField()
	}
	return formEffect{}
}

func (f *stagedForm) nextField() formEffect {
	if f.field+1 < len(f.stages[f.stage].Fields) {
		f.field++
		return formEffect{Changed: true}
	}
	if f.stage+1 < len(f.stages) {
		f.stage++
		f.field = 0
		f.stageError = ""
		return formEffect{Changed: true}
	}
	return formEffect{}
}

func (f *stagedForm) previousField() formEffect {
	if f.field > 0 {
		f.field--
		return formEffect{Changed: true}
	}
	if f.stage > 0 {
		f.stage--
		f.field = len(f.stages[f.stage].Fields) - 1
		f.stageError = ""
		return formEffect{Changed: true}
	}
	return formEffect{}
}

func (f *stagedForm) finish(err error) {
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
			for _, field := range stage.Fields {
				if value := strings.TrimSpace(field.Value); value != "" {
					parts = append(parts, field.Label+": "+value)
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
			prefix := "  "
			if fieldIndex == f.field {
				prefix = "▸ "
			}
			lines = append(lines, prefix+field.Label+": "+field.Value)
			if field.Error != "" {
				lines = append(lines, "    "+field.Error)
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
		lines = append(lines, "  cloning…")
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

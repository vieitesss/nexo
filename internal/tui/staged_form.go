package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/shell"
)

// formSerial gives every form transition a process-unique generation. Actions
// replace one form with another, so a per-form counter could hand two forms the
// same value and let a late result match the wrong action.
var formSerial atomic.Uint64

type formFieldKind string

const (
	formText        formFieldKind = "text"
	formChoice      formFieldKind = "choice"
	formToggle      formFieldKind = "toggle"
	formMultiSelect formFieldKind = "multi-select"
	formDisplay     formFieldKind = "display"
)

type formValues struct {
	text  map[string]string
	lists map[string][]string
}

func (v formValues) String(id string) string {
	return v.text[id]
}

func (v formValues) List(id string) []string {
	return append([]string(nil), v.lists[id]...)
}

func (v formValues) Bool(id string) bool {
	return strings.EqualFold(strings.TrimSpace(v.text[id]), "yes")
}

// formField describes one focused control. Mutable state belongs to the
// control for its kind: textinput owns text, choice owns its selected index,
// toggle owns its boolean, and a checklist owns selected bits plus its cursor.
// Default seeds text/toggle/choice state or holds a display value, and
// InitialSelected seeds a checklist; neither is a second value.
type formField struct {
	ID         string
	Label      string
	Error      string
	Expandable bool

	Kind            formFieldKind
	Options         []string
	Placeholder     string
	Default         string
	InitialSelected []string
	AbbreviateHome  bool
	Validate        func(string, formValues) error

	input          textinput.Model
	invalidDefault string
	choiceIndex    int
	toggleValue    bool
	multiSelected  []bool
	multiCursor    int
}

// setValue applies a string to the control that owns this kind's state.
func (field *formField) setValue(value string) {
	switch field.Kind {
	case formText:
		field.invalidDefault = ""
		field.input.SetValue(value)
	case formChoice:
		field.choiceIndex = optionIndex(field.Options, value)
	case formToggle:
		field.toggleValue = strings.EqualFold(strings.TrimSpace(value), "yes")
	case formDisplay:
		field.Default = value
	}
}

// setError keeps a textinput's own validation state in lockstep with the error
// the staged form displays, so editing after a cross-field change cannot
// surface a stale message.
func (field *formField) setError(message string) {
	field.Error = message
	if field.Kind != formText {
		return
	}
	field.input.Err = nil
	if message != "" {
		field.input.Err = errors.New(message)
	}
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

// stagedFormMsg keeps component messages tied to the form that created their
// command. Textinput blink and paste messages are asynchronous, so a message
// from a canceled form must not update a replacement form that happens to have
// a focused text field too.
type stagedFormMsg struct {
	generation uint64
	msg        tea.Msg
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
	submit     func(context.Context, formValues, uint64) tea.Cmd
	cancel     context.CancelFunc
	generation uint64
	busyLabel  string
	expanded   map[string]bool
	help       help.Model

	keyHook             func(tea.KeyMsg) (formEffect, bool)
	stageLinesHook      func(formStage) []string
	viewportHook        func([]string, int, int) (int, bool)
	fieldVisibilityHook func(formStage, formField) bool
	viewportHeight      int
	viewportWidth       int
	initialFocus        tea.Cmd
}

func newStagedForm(ctx context.Context, stages []formStage, validate func(*stagedForm) formValidation, submit func(context.Context, formValues, uint64) tea.Cmd) *stagedForm {
	if ctx == nil {
		ctx = context.Background()
	}
	copied := make([]formStage, len(stages))
	for stageIndex, stage := range stages {
		copied[stageIndex] = stage
		copied[stageIndex].Fields = append([]formField(nil), stage.Fields...)
		for fieldIndex := range copied[stageIndex].Fields {
			field := &copied[stageIndex].Fields[fieldIndex]
			field.Options = append([]string(nil), field.Options...)
			field.InitialSelected = append([]string(nil), field.InitialSelected...)
		}
	}
	form := &stagedForm{
		ctx: ctx, stages: copied, validate: validate, submit: submit,
		busyLabel: "working…", expanded: make(map[string]bool), help: formHelp(),
		generation: formSerial.Add(1),
	}
	for stageIndex := range form.stages {
		for fieldIndex := range form.stages[stageIndex].Fields {
			field := &form.stages[stageIndex].Fields[fieldIndex]
			switch field.Kind {
			case formText:
				field.input = newFormTextInput(*field, func(value string) error {
					if field.Validate == nil {
						return nil
					}
					return field.Validate(value, form.values())
				})
				if field.Default != field.input.Value() {
					// bubbles/textinput sanitizes newlines in pasted defaults. Keep
					// the rejected prefill only until the user edits it, so forms
					// can still report the original invalid query inline.
					field.invalidDefault = field.Default
				}
				if field.input.Err != nil {
					field.Error = field.input.Err.Error()
				}
			case formChoice, formToggle:
				field.setValue(field.Default)
			case formMultiSelect:
				field.multiSelected = make([]bool, len(field.Options))
				for _, selected := range field.InitialSelected {
					if index := optionIndex(field.Options, selected); index >= 0 {
						field.multiSelected[index] = true
					}
				}
			}
		}
	}
	form.initialFocus = form.focusCurrent()
	return form
}

func newFormTextInput(field formField, validate textinput.ValidateFunc) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = field.Placeholder
	input.Validate = validate
	input.Width = 64
	input.PromptStyle = normal()
	input.TextStyle = normal()
	input.PlaceholderStyle = muted()
	input.CompletionStyle = muted()
	input.Cursor.Style = accent()
	input.SetValue(field.Default)
	input.Blur()
	return input
}

func formHelp() help.Model {
	model := help.New()
	model.Styles.ShortKey = accent()
	model.Styles.ShortDesc = muted()
	model.Styles.ShortSeparator = muted()
	model.Styles.Ellipsis = muted()
	model.Styles.FullKey = model.Styles.ShortKey
	model.Styles.FullDesc = model.Styles.ShortDesc
	model.Styles.FullSeparator = model.Styles.ShortSeparator
	return model
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
	if f.fieldVisibilityHook != nil && !f.fieldVisibilityHook(f.stages[stage], field) {
		return false
	}
	return !field.Expandable || f.expanded[field.ID]
}

func (f *stagedForm) setViewportWidth(width int) {
	f.viewportWidth = width
	if width <= 0 {
		return
	}
	for stageIndex := range f.stages {
		for fieldIndex := range f.stages[stageIndex].Fields {
			field := &f.stages[stageIndex].Fields[fieldIndex]
			if field.Kind == formText {
				labelWidth := shell.StringWidth("▸ " + field.Label + ": ")
				// textinput renders one cursor cell in addition to Width when
				// the cursor is at the end of the value.
				field.input.Width = max(1, width-labelWidth-1)
			}
		}
	}
}

func (f *stagedForm) focusCurrent() tea.Cmd {
	var cmd tea.Cmd
	for stageIndex := range f.stages {
		for fieldIndex := range f.stages[stageIndex].Fields {
			field := &f.stages[stageIndex].Fields[fieldIndex]
			if field.Kind != formText {
				continue
			}
			if stageIndex == f.stage && fieldIndex == f.field {
				cmd = field.input.Focus()
			} else {
				field.input.Blur()
			}
		}
	}
	return f.wrapComponentCmd(cmd)
}

func (f *stagedForm) wrapComponentCmd(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	generation := f.generation
	return func() tea.Msg {
		msg := cmd()
		if msg == nil {
			return nil
		}
		return stagedFormMsg{generation: generation, msg: msg}
	}
}

func (f *stagedForm) initialFocusCmd() tea.Cmd {
	cmd := f.initialFocus
	f.initialFocus = nil
	return cmd
}

func (f *stagedForm) moveField(delta int) formEffect {
	if f.stage < 0 || f.stage >= len(f.stages) {
		return f.advance()
	}
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
			return formEffect{Changed: true, Cmd: f.focusCurrent()}
		}
	}
}

func (f *stagedForm) values() formValues {
	values := formValues{text: make(map[string]string), lists: make(map[string][]string)}
	for _, stage := range f.stages {
		for _, field := range stage.Fields {
			if field.Kind == formMultiSelect {
				values.lists[field.ID] = f.selectedOptions(field)
				continue
			}
			values.text[field.ID] = f.fieldStringValue(field)
		}
	}
	return values
}

// formValue reads one field's current value. Validators and submits use it so
// they do not need to know a form's stage layout.
func formValue(form *stagedForm, id string) string {
	if form == nil {
		return ""
	}
	return form.values().String(id)
}

func (f *stagedForm) setFieldValue(id, value string) {
	for stageIndex := range f.stages {
		for fieldIndex := range f.stages[stageIndex].Fields {
			if field := &f.stages[stageIndex].Fields[fieldIndex]; field.ID == id {
				field.setValue(value)
			}
		}
	}
}

func (f *stagedForm) applyValidation() bool {
	if f.validate == nil {
		return true
	}
	result := f.validate(f)
	f.stageError, f.notice = result.Error, result.Notice
	for id, value := range result.Values {
		f.setFieldValue(id, value)
	}
	if result.Error != "" {
		return false
	}
	// A textinput validator runs while the user edits, but all other form
	// validators run here. Never let a live inline error be bypassed by Enter.
	if f.stage < 0 || f.stage >= len(f.stages) {
		return true
	}
	for _, field := range f.stages[f.stage].Fields {
		if field.Error != "" {
			return false
		}
	}
	return true
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
		f.nextGeneration()
		return formEffect{Canceled: true}
	}
	if f.inFlight {
		return formEffect{}
	}
	if f.keyHook != nil {
		if effect, handled := f.keyHook(msg); handled {
			return effect
		}
	}
	field := f.currentField()

	// Navigation shared by empty diagnostic stages and ordinary fields.
	switch msg.Type {
	case tea.KeyTab:
		return f.nextField()
	case tea.KeyUp:
		if field != nil && field.Kind == formMultiSelect {
			return f.moveOption(field, false)
		}
		return f.moveField(-1)
	case tea.KeyDown:
		if field != nil && field.Kind == formMultiSelect {
			return f.moveOption(field, true)
		}
		return f.moveField(1)
	case tea.KeyLeft:
		if field != nil && (field.Kind == formChoice || field.Kind == formToggle) {
			return f.cycle(field, false)
		}
		if field == nil && f.stage > 0 {
			f.stage--
			f.field = 0
			f.stageError = ""
			return formEffect{Changed: true, Cmd: f.focusCurrent()}
		}
	case tea.KeyRight:
		if field != nil && (field.Kind == formChoice || field.Kind == formToggle) {
			return f.cycle(field, true)
		}
		if field == nil {
			return f.advance()
		}
	case tea.KeyEnter:
		return f.advance()
	case tea.KeyCtrlP:
		if field != nil {
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
				return formEffect{Changed: true, Cmd: f.focusCurrent()}
			}
		}
	case tea.KeySpace:
		if field != nil {
			switch field.Kind {
			case formToggle:
				return f.cycle(field, true)
			case formMultiSelect:
				return f.toggleOption(field)
			}
		}
	}
	if msg.String() == "shift+tab" {
		return f.previousField()
	}
	if field != nil && field.Kind == formText {
		return f.updateInput(msg)
	}
	return formEffect{}
}

// updateInput forwards a key or component message to the focused textinput.
// Only the current form's text field accepts it, so a blink or paste result
// from a canceled form can never mutate a replacement one.
func (f *stagedForm) updateInput(msg tea.Msg) formEffect {
	field := f.currentField()
	if field == nil || field.Kind != formText {
		return formEffect{}
	}
	before := field.input.Value()
	updated, cmd := field.input.Update(msg)
	updated.Cursor.Blink = true
	field.input = updated
	if before != updated.Value() {
		field.invalidDefault = ""
	}
	field.Error = ""
	if updated.Err != nil {
		field.Error = updated.Err.Error()
	}
	f.stageError = ""
	return formEffect{Changed: true, Cmd: f.wrapComponentCmd(cmd)}
}

// updateMsg unwraps a command result whose generation still belongs to this
// form before forwarding it to the focused textinput.
func (f *stagedForm) updateMsg(msg tea.Msg) formEffect {
	component, ok := msg.(stagedFormMsg)
	if !ok || component.generation != f.generation {
		return formEffect{}
	}
	return f.updateInput(component.msg)
}

func optionIndex(options []string, value string) int {
	for index, option := range options {
		if strings.EqualFold(strings.TrimSpace(option), strings.TrimSpace(value)) {
			return index
		}
	}
	return -1
}

func (f *stagedForm) cycle(field *formField, forward bool) formEffect {
	if field.Kind == formToggle {
		field.toggleValue = !field.toggleValue
		field.Error = ""
		f.stageError = ""
		return formEffect{Changed: true}
	}
	if len(field.Options) == 0 {
		return formEffect{}
	}
	index := field.choiceIndex
	if index < 0 || index >= len(field.Options) {
		index = 0
	} else if forward {
		index = (index + 1) % len(field.Options)
	} else {
		index = (index - 1 + len(field.Options)) % len(field.Options)
	}
	field.choiceIndex = index
	field.Error = ""
	f.stageError = ""
	return formEffect{Changed: true}
}

func (f *stagedForm) moveOption(field *formField, forward bool) formEffect {
	if len(field.Options) == 0 {
		return formEffect{}
	}
	if forward {
		field.multiCursor = (field.multiCursor + 1) % len(field.Options)
	} else {
		field.multiCursor = (field.multiCursor - 1 + len(field.Options)) % len(field.Options)
	}
	return formEffect{Changed: true}
}

func (f *stagedForm) toggleOption(field *formField) formEffect {
	if len(field.Options) == 0 {
		return formEffect{}
	}
	if field.multiCursor < 0 || field.multiCursor >= len(field.Options) {
		field.multiCursor = 0
	}
	field.multiSelected[field.multiCursor] = !field.multiSelected[field.multiCursor]
	field.Error = ""
	f.stageError = ""
	return formEffect{Changed: true}
}

func (f *stagedForm) advance() formEffect {
	if !f.applyValidation() {
		return formEffect{Changed: true}
	}
	if f.stage+1 < len(f.stages) {
		f.stage++
		f.field = 0
		f.stageError = ""
		return formEffect{Changed: true, Cmd: f.focusCurrent()}
	}
	f.inFlight = true
	generation := f.nextGeneration()
	ctx, cancel := context.WithCancel(f.ctx)
	f.cancel = cancel
	if f.submit == nil {
		return formEffect{Submitted: true}
	}
	return formEffect{Cmd: f.submit(ctx, f.values(), generation), Submitted: true}
}

// nextGeneration returns a process-unique token for one form transition. The
// token is carried through submitted messages so a replacement form can never
// accept a late result from the form it replaced.
func (f *stagedForm) nextGeneration() uint64 {
	f.generation = formSerial.Add(1)
	return f.generation
}

func (f *stagedForm) nextField() formEffect { return f.moveField(1) }

func (f *stagedForm) previousField() formEffect {
	if f.field > 0 {
		for index := f.field - 1; index >= 0; index-- {
			if f.fieldVisible(index) {
				f.field = index
				return formEffect{Changed: true, Cmd: f.focusCurrent()}
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
		return formEffect{Changed: true, Cmd: f.focusCurrent()}
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
				value := f.summaryFieldValue(field)
				if value != "" {
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
			if !f.fieldVisible(fieldIndex) {
				continue
			}
			active := fieldIndex == f.field
			prefix := "  "
			if active {
				prefix = "▸ "
			}
			if field.Kind == formMultiSelect {
				lines = append(lines, prefix+field.Label)
				for index, option := range field.Options {
					marker := "○"
					if index < len(field.multiSelected) && field.multiSelected[index] {
						marker = "●"
					}
					if active && field.multiCursor == index {
						lines = append(lines, "    ▸ "+marker+" "+option)
					} else {
						lines = append(lines, "      "+marker+" "+option)
					}
				}
			} else {
				lines = append(lines, prefix+field.Label+": "+f.fieldValue(field, active))
			}
			if field.Error != "" {
				lines = append(lines, "    "+field.Error)
			}
		}
		if stageIndex == f.stage {
			if f.stageLinesHook != nil {
				lines = append(lines, f.stageLinesHook(stage)...)
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
		if f.viewportWidth > 0 {
			f.help.Width = f.viewportWidth
		} else {
			f.help.Width = 0
		}
		final := f.stage >= len(f.stages)-1
		if footer := f.help.View(focusedFormKeyMap(f.currentField(), final)); footer != "" {
			lines = append(lines, "  "+footer)
		}
	}
	return lines
}

func (f stagedForm) summaryFieldValue(field formField) string {
	value := f.fieldStringValue(field)
	if field.AbbreviateHome {
		value = abbreviatePathList(value)
	}
	return value
}

func (f stagedForm) fieldStringValue(field formField) string {
	switch field.Kind {
	case formText:
		if field.invalidDefault != "" {
			return field.invalidDefault
		}
		return field.input.Value()
	case formChoice:
		if field.choiceIndex >= 0 && field.choiceIndex < len(field.Options) {
			return field.Options[field.choiceIndex]
		}
	case formToggle:
		return boolLabel(field.toggleValue)
	case formMultiSelect:
		return strings.Join(f.selectedOptions(field), ", ")
	case formDisplay:
		return field.Default
	}
	return ""
}

func boolLabel(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func (f stagedForm) selectedOptions(field formField) []string {
	selected := make([]string, 0, len(field.Options))
	for index, option := range field.Options {
		if index < len(field.multiSelected) && field.multiSelected[index] {
			selected = append(selected, option)
		}
	}
	return selected
}

func (f stagedForm) fieldValue(field formField, active bool) string {
	if field.Kind == formText && active {
		return field.input.View()
	}
	if field.Kind == formToggle {
		return "[" + boolLabel(field.toggleValue) + "]"
	}
	if field.Kind == formChoice {
		return choiceView(field)
	}
	return f.fieldStringValue(field)
}

func choiceView(field formField) string {
	if len(field.Options) == 0 {
		return ""
	}
	selected := lipgloss.NewStyle().Foreground(palette.Accent).Bold(true)
	muted := lipgloss.NewStyle().Foreground(palette.Muted)
	options := make([]string, 0, len(field.Options))
	for index, option := range field.Options {
		if index == field.choiceIndex {
			options = append(options, selected.Render(option))
		} else {
			options = append(options, muted.Render(option))
		}
	}
	return strings.Join(options, muted.Render(" · "))
}

func abbreviatePathList(value string) string {
	parts := strings.Split(value, ",")
	for index, part := range parts {
		parts[index] = abbreviateHomePath(strings.TrimSpace(part))
	}
	return strings.Join(parts, ", ")
}

func abbreviateHomePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !filepath.IsAbs(path) {
		return path
	}
	home = filepath.Clean(home)
	path = filepath.Clean(path)
	if path == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

type formKeyMap struct{ bindings []key.Binding }

func (m formKeyMap) ShortHelp() []key.Binding  { return m.bindings }
func (m formKeyMap) FullHelp() [][]key.Binding { return [][]key.Binding{m.bindings} }

func focusedFormKeyMap(field *formField, final bool) formKeyMap {
	bindings := []key.Binding{}
	if field != nil {
		switch field.Kind {
		case formText:
			bindings = append(bindings,
				key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "move")),
				key.NewBinding(key.WithKeys("ctrl+w"), key.WithHelp("ctrl+w", "delete word")),
			)
		case formChoice:
			bindings = append(bindings, key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "choose")))
		case formToggle:
			bindings = append(bindings, key.NewBinding(key.WithKeys("space", "left", "right"), key.WithHelp("space", "toggle")))
		case formMultiSelect:
			bindings = append(bindings, key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "move")), key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "select")))
		}
	}
	enterDesc := "next"
	if final {
		enterDesc = "submit"
	}
	bindings = append(bindings,
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next")),
		key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "previous")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", enterDesc)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	)
	return formKeyMap{bindings: bindings}
}

func summaryJoin(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return " — " + strings.Join(parts, ", ")
}

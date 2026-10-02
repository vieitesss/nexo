package tui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/deletion"
)

// deleteForm owns the destructive flow's identity, preflight plan, and
// scrolling state. stagedForm only provides the generic field and stage
// mechanics; the hooks below let this flow add its one safety-critical stage.
type deleteForm struct {
	*stagedForm

	preflightPending bool
	identity         string
	identityValid    bool
	plan             *deletion.Plan
	preflightScroll  int
	preflightSeen    bool
}

// newDeleteForm captures the selected path identity before the modal is
// displayed, then gathers a complete read-only plan before showing the
// destructive confirmation fields.
func newDeleteForm(deps Deps, ctx context.Context, path string) *deleteForm {
	identity := ""
	identityValid := false
	if deps.DeleteFingerprint != nil {
		captured, err := deps.DeleteFingerprint(ctx, path)
		if err == nil && captured != "" {
			identity, identityValid = captured, true
		}
	}
	state := &deleteForm{
		identity:      identity,
		identityValid: identityValid,
	}
	state.stagedForm = newStagedForm(ctx, []formStage{
		{ID: "target", Title: "delete project", Fields: []formField{
			{ID: "path", Label: "path", Value: path, ReadOnly: true},
		}},
		{ID: "preflight", Title: "preflight", Fields: nil},
		{ID: "confirmation", Title: "confirm deletion", Fields: []formField{
			{ID: "mode", Label: "mode (trash/permanent)", Value: "trash"},
			{ID: "force", Label: "accept warnings (yes/no)", Value: "no"},
			{ID: "confirm", Label: "type yes to delete", Value: "no"},
		}},
	}, state.validate, nil)
	state.keyHook = state.key
	state.advanceHook = state.advance
	state.stageLinesHook = state.stageLines
	state.viewportHook = state.viewport
	state.scrollOffsetHook = state.scrollOffset
	state.startHook = state.startPreflight
	state.actionHook = state.finishAction
	state.submit = deleteFormSubmit(deps, state)
	state.busyLabel = "preflighting and deleting…"
	return state
}

func (d *deleteForm) preflightStage() int {
	for index, stage := range d.stages {
		if stage.ID == "preflight" {
			return index
		}
	}
	return -1
}

func (d *deleteForm) validate(form *stagedForm) formValidation {
	result := formValidation{Values: map[string]string{}}
	path := formValue(form, "path")
	if path == "" {
		result.Error = "path is required"
		return result
	}
	if d.preflightPending {
		result.Error = "preflight is still running"
		return result
	}
	if d.plan == nil {
		result.Error = "preflight is required before confirmation"
		return result
	}
	if form.stage == 0 {
		return result
	}
	if form.stage == d.preflightStage() {
		if len(d.plan.Blockers) > 0 {
			result.Error = "delete is blocked; cancel or fix the selected candidate"
		}
		return result
	}
	mode := strings.ToLower(formValue(form, "mode"))
	if mode != "trash" && mode != "permanent" {
		result.Error = "mode must be trash or permanent"
		return result
	}
	force := strings.ToLower(formValue(form, "force"))
	if force != "yes" && force != "no" {
		result.Error = "accept warnings must be yes or no"
		return result
	}
	confirm := strings.ToLower(formValue(form, "confirm"))
	if confirm != "yes" {
		result.Error = "type yes to confirm deletion"
		return result
	}
	if len(d.plan.Warnings) > 0 && force != "yes" {
		result.Error = "accept warnings must be yes"
		return result
	}
	result.Notice = "preflight rejects non-candidates, active containers, changed paths, remote failures, and Git refusals"
	return result
}

func deleteFormSubmit(deps Deps, form *deleteForm) func(context.Context, map[string]string, uint64) tea.Cmd {
	return func(actionCtx context.Context, values map[string]string, generation uint64) tea.Cmd {
		path := values["path"]
		permanent := strings.EqualFold(values["mode"], "permanent")
		force := strings.EqualFold(values["force"], "yes")
		plan := form.plan
		return func() tea.Msg {
			if deps.DeleteProjectPlan == nil || plan == nil {
				return actionMsg{kind: "delete", destination: path, generation: generation, err: errors.New("project deletion is not configured")}
			}
			return actionMsg{kind: "delete", destination: path, generation: generation, err: deps.DeleteProjectPlan(actionCtx, *plan, permanent, force)}
		}
	}
}

func (d *deleteForm) key(msg tea.KeyMsg) (formEffect, bool) {
	if d.stage != d.preflightStage() {
		return formEffect{}, false
	}
	switch msg.Type {
	case tea.KeyUp:
		return d.scroll(-1), true
	case tea.KeyDown:
		return d.scroll(1), true
	case tea.KeyPgUp:
		return d.scroll(-max(1, d.viewportHeight-1)), true
	case tea.KeyPgDown:
		return d.scroll(max(1, d.viewportHeight-1)), true
	case tea.KeyEnter, tea.KeyRight, tea.KeyTab:
		if !d.preflightSeen {
			d.stageError = "scroll through the complete preflight before continuing"
			return formEffect{Changed: true}, true
		}
		if msg.Type == tea.KeyTab {
			return d.nextField(), true
		}
		effect, handled := d.advance()
		if !handled {
			return d.stagedForm.advance(), true
		}
		return effect, true
	}
	return formEffect{}, false
}

func (d *deleteForm) advance() (formEffect, bool) {
	if d.stage == d.preflightStage() && !d.preflightSeen {
		d.stageError = "scroll through the complete preflight before continuing"
		return formEffect{Changed: true}, true
	}
	return formEffect{}, false
}

func (d *deleteForm) stageLines(stage formStage) []string {
	if d.plan == nil || stage.ID != "preflight" {
		return nil
	}
	lines := make([]string, 0, len(d.plan.Warnings)+len(d.plan.Blockers)+1)
	for _, finding := range d.plan.Warnings {
		lines = append(lines, "  warning: "+finding.Message)
	}
	for _, finding := range d.plan.Blockers {
		lines = append(lines, "  blocker: "+finding.Message)
	}
	if len(lines) == 0 {
		lines = append(lines, "  no warnings or blockers")
	}
	return lines
}

func (d *deleteForm) viewport(body []string, maxBody, width int) {
	if d.stage != d.preflightStage() {
		return
	}
	if maxBody <= 0 || width <= 0 {
		d.preflightSeen = false
		return
	}
	maxScroll := max(0, len(body)-maxBody)
	d.preflightScroll = min(maxScroll, max(0, d.preflightScroll))
	if len(body) <= maxBody || d.preflightScroll >= maxScroll {
		d.preflightSeen = true
	}
}

func (d *deleteForm) scrollOffset() (int, bool) {
	if d.stage != d.preflightStage() {
		return 0, false
	}
	return d.preflightScroll, true
}

func (d *deleteForm) scroll(delta int) formEffect {
	if d.viewportHeight <= 0 {
		return formEffect{}
	}
	width := d.viewportWidth
	if width <= 0 {
		width = 80
	}
	lines := formLinesForWidth(d.renderLines(), width)
	maxScroll := max(0, len(lines)-d.viewportHeight)
	d.preflightScroll = min(maxScroll, max(0, d.preflightScroll+delta))
	if d.preflightScroll >= maxScroll {
		d.preflightSeen = true
	}
	d.stageError = ""
	return formEffect{Changed: true}
}

// startPreflight launches the read-only plan gather before confirmation.
func (d *deleteForm) startPreflight(deps Deps) tea.Cmd {
	if d == nil || deps.DeletePreflight == nil {
		return nil
	}
	d.preflightPending = true
	d.inFlight = true
	d.generation++
	generation := d.generation
	ctx, cancel := context.WithCancel(d.ctx)
	d.cancel = cancel
	path := formValue(d.stagedForm, "path")
	identity := d.identity
	return func() tea.Msg {
		if !d.identityValid {
			return actionMsg{kind: "delete-preflight", destination: path, generation: generation, err: errors.New("cannot verify selected project identity")}
		}
		plan, err := deps.DeletePreflight(ctx, path, identity)
		return actionMsg{kind: "delete-preflight", destination: path, generation: generation, plan: &plan, err: err}
	}
}

func (d *deleteForm) finishAction(msg actionMsg) {
	if msg.kind != "delete-preflight" {
		return
	}
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	d.inFlight = false
	d.preflightPending = false
	d.preflightScroll = 0
	d.preflightSeen = d.viewportHeight == 0
	if msg.err != nil {
		d.stageError = msg.err.Error()
		return
	}
	d.plan = msg.plan
	if d.stage < d.preflightStage() {
		d.stage = d.preflightStage()
		d.field = 0
	}
}

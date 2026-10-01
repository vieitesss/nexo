package tui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/deletion"
)

// newDeleteForm captures the selected path identity before the modal is
// displayed, then gathers a complete read-only plan before showing the
// destructive confirmation fields.
func newDeleteForm(deps Deps, ctx context.Context, path string) *stagedForm {
	identity := ""
	identityValid := false
	if deps.DeleteFingerprint != nil {
		captured, err := deps.DeleteFingerprint(ctx, path)
		if err == nil && captured != "" {
			identity, identityValid = captured, true
		}
	}
	form := newStagedForm(ctx, []formStage{
		{ID: "target", Title: "delete project", Fields: []formField{
			{ID: "path", Label: "path", Value: path, ReadOnly: true},
		}},
		{ID: "preflight", Title: "preflight", Fields: nil},
		{ID: "confirmation", Title: "confirm deletion", Fields: []formField{
			{ID: "mode", Label: "mode (trash/permanent)", Value: "trash"},
			{ID: "force", Label: "accept warnings (yes/no)", Value: "no"},
			{ID: "confirm", Label: "type yes to delete", Value: "no"},
		}},
	}, deleteFormValidation, nil)
	form.deletePreflightEnabled = true
	form.deleteIdentity = identity
	form.deleteIdentityValid = identityValid
	form.submit = deleteFormSubmit(deps, form)
	form.busyLabel = "preflighting and deleting…"
	return form
}

func deleteFormValidation(form *stagedForm) formValidation {
	result := formValidation{Values: map[string]string{}}
	path := formValue(form, "path")
	if path == "" {
		result.Error = "path is required"
		return result
	}
	if form.deletePreflightPending {
		result.Error = "preflight is still running"
		return result
	}
	if form.deletePlan == nil {
		result.Error = "preflight is required before confirmation"
		return result
	}
	if form.stage == 0 {
		return result
	}
	if form.stage == 1 {
		if len(form.deletePlan.Blockers) > 0 {
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
	if len(form.deletePlan.Warnings) > 0 && force != "yes" {
		result.Error = "accept warnings must be yes"
		return result
	}
	result.Notice = "preflight rejects non-candidates, active containers, changed paths, remote failures, and Git refusals"
	return result
}

func deleteFormSubmit(deps Deps, form *stagedForm) func(context.Context, map[string]string, uint64) tea.Cmd {
	return func(actionCtx context.Context, values map[string]string, generation uint64) tea.Cmd {
		path := values["path"]
		permanent := strings.EqualFold(values["mode"], "permanent")
		force := strings.EqualFold(values["force"], "yes")
		plan := form.deletePlan
		return func() tea.Msg {
			if deps.DeleteProjectPlan == nil || plan == nil {
				return actionMsg{kind: "delete", destination: path, generation: generation, err: errors.New("project deletion is not configured")}
			}
			return actionMsg{kind: "delete", destination: path, generation: generation, err: deps.DeleteProjectPlan(actionCtx, *plan, permanent, force)}
		}
	}
}

// startDeletePreflight launches the read-only plan gather before confirmation.
func (f *stagedForm) startDeletePreflight(deps Deps) tea.Cmd {
	if f == nil || deps.DeletePreflight == nil {
		return nil
	}
	f.deletePreflightPending = true
	f.inFlight = true
	f.generation++
	generation := f.generation
	ctx, cancel := context.WithCancel(f.ctx)
	f.cancel = cancel
	path := formValue(f, "path")
	identity := f.deleteIdentity
	return func() tea.Msg {
		if !f.deleteIdentityValid {
			return actionMsg{kind: "delete-preflight", destination: path, generation: generation, err: errors.New("cannot verify selected project identity")}
		}
		plan, err := deps.DeletePreflight(ctx, path, identity)
		return actionMsg{kind: "delete-preflight", destination: path, generation: generation, plan: &plan, err: err}
	}
}

func (f *stagedForm) finishDeletePreflight(plan *deletion.Plan, err error) {
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
	f.inFlight = false
	f.deletePreflightPending = false
	if err != nil {
		f.stageError = err.Error()
		return
	}
	f.deletePlan = plan
	if f.stage < 1 {
		f.stage = 1
		f.field = 0
	}
}

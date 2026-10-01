package tui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// newDeleteForm reuses stagedForm for the destructive confirmation. The
// selected lexical path is displayed unchanged; the action callback performs a
// fresh exhaustive preflight immediately before mutation.
func newDeleteForm(deps Deps, ctx context.Context, path string) *stagedForm {
	form := newStagedForm(ctx, []formStage{
		{ID: "target", Title: "delete project", Fields: []formField{
			{ID: "path", Label: "path", Value: path},
		}},
		{ID: "confirmation", Title: "preflight and confirmation", Fields: []formField{
			{ID: "mode", Label: "mode (trash/permanent)", Value: "trash"},
			{ID: "force", Label: "accept warnings (yes/no)", Value: "no"},
		}},
	}, deleteFormValidation, deleteFormSubmit(deps))
	form.busyLabel = "preflighting and deleting…"
	return form
}

func deleteFormValidation(form *stagedForm) formValidation {
	result := formValidation{Values: map[string]string{}}
	path := strings.TrimSpace(formValue(form, "path"))
	if path == "" {
		result.Error = "path is required"
		return result
	}
	if form.stage == 0 {
		return result
	}
	mode := strings.ToLower(strings.TrimSpace(formValue(form, "mode")))
	if mode != "trash" && mode != "permanent" {
		result.Error = "mode must be trash or permanent"
		return result
	}
	force := strings.ToLower(strings.TrimSpace(formValue(form, "force")))
	if force != "yes" && force != "no" {
		result.Error = "accept warnings must be yes or no"
		return result
	}
	result.Notice = "preflight rejects non-candidates, active containers, changed paths, remote failures, and Git refusals"
	return result
}

func deleteFormSubmit(deps Deps) func(context.Context, map[string]string, uint64) tea.Cmd {
	return func(actionCtx context.Context, values map[string]string, generation uint64) tea.Cmd {
		path := strings.TrimSpace(values["path"])
		permanent := strings.EqualFold(strings.TrimSpace(values["mode"]), "permanent")
		force := strings.EqualFold(strings.TrimSpace(values["force"]), "yes")
		deleteProject := deps.DeleteProject
		return func() tea.Msg {
			if deleteProject == nil {
				return actionMsg{kind: "delete", destination: path, generation: generation, err: errors.New("project deletion is not supported")}
			}
			return actionMsg{kind: "delete", destination: path, generation: generation, err: deleteProject(actionCtx, path, permanent, force)}
		}
	}
}

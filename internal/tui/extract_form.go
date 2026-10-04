package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/picker"
	worktreeflow "github.com/vieitesss/nexo/internal/worktree"
)

func newExtractForm(deps Deps, place picker.Place, ctx context.Context) *stagedForm {
	branch := ""
	if place.GitContext != nil {
		branch = strings.TrimSpace(place.GitContext.Identity)
	}
	if branch == "" {
		branch = "worktree"
	}
	destinationSuggestion := extractDestinationSuggestion(place.Path, branch)
	returnBranch := ""
	var suggestionErr error
	if deps.ExtractReturnBranch != nil {
		returnBranch, suggestionErr = deps.ExtractReturnBranch(ctx, place.Path)
		returnBranch = strings.TrimSpace(returnBranch)
	}
	form := newStagedForm(ctx, []formStage{
		{ID: "destination", Title: "Destination", Fields: []formField{{ID: "destination", Label: "path", Default: destinationSuggestion, Kind: formText, Placeholder: "worktree directory", AbbreviateHome: true}}},
		{ID: "return-branch", Title: "Return branch", Fields: []formField{{ID: "return-branch", Label: "branch", Default: returnBranch, Kind: formText, Placeholder: "optional branch"}}},
	}, extractFormValidation(deps), extractFormSubmit(deps, place.Path))
	form.busyLabel = "extracting…"
	if suggestionErr != nil {
		form.notice = suggestionErr.Error()
	}
	return form
}

func extractDestinationSuggestion(repo, branch string) string {
	repo = filepath.Clean(repo)
	name := filepath.Base(repo)
	branch = strings.NewReplacer("/", "-", "\\", "-").Replace(strings.TrimSpace(branch))
	if branch == "" {
		branch = "worktree"
	}
	return filepath.Join(filepath.Dir(repo), name+"-"+branch)
}

func extractFormValidation(deps Deps) func(*stagedForm) formValidation {
	return func(form *stagedForm) formValidation {
		result := formValidation{Values: map[string]string{}}
		destinationValue := formValue(form, "destination")
		request, err := worktreeflow.Validate(worktreeflow.Request{
			Destination: destinationValue,
		}, deps.ProjectPaths, deps.Home)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		result.Values["destination"] = request.Destination
		return result
	}
}

func extractFormSubmit(deps Deps, repo string) func(context.Context, formValues, uint64) tea.Cmd {
	return func(actionCtx context.Context, values formValues, generation uint64) tea.Cmd {
		destinationValue := strings.TrimSpace(values.String("destination"))
		returnBranch := strings.TrimSpace(values.String("return-branch"))
		extract := deps.ExtractWorktree
		return func() tea.Msg {
			if extract == nil {
				return actionMsg{kind: "extract", destination: destinationValue, generation: generation, err: errors.New("worktree extraction is not supported")}
			}
			_, err := extract(actionCtx, repo, destinationValue, returnBranch)
			if err != nil {
				return actionMsg{kind: "extract", destination: destinationValue, generation: generation, err: err}
			}
			return actionMsg{kind: "extract", destination: destinationValue, generation: generation}
		}
	}
}

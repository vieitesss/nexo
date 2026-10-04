package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/destination"
)

// newDirectoryForm reuses the clone staged form: one destination stage with an
// optional add-parent field. Creation is a plain filesystem action; it never
// touches Git or a multiplexer and never opens or focuses a container.
func newDirectoryForm(deps Deps, ctx context.Context) *stagedForm {
	fields := []formField{{ID: "destination", Label: "path", Default: "", Kind: formText, Placeholder: "project directory", AbbreviateHome: true}}
	if deps.AddProjectPath != nil {
		fields = append(fields, formField{ID: "add-parent", Label: "add parent", Default: "no", Kind: formToggle})
	}
	form := newStagedForm(ctx, []formStage{
		{ID: "destination", Title: "New directory", Fields: fields},
	}, directoryFormValidation(deps), directoryFormSubmit(deps))
	form.busyLabel = "creating…"
	return form
}

func directoryFormValidation(deps Deps) func(*stagedForm) formValidation {
	return func(form *stagedForm) formValidation {
		result := formValidation{Values: map[string]string{}}
		value := strings.TrimSpace(formValue(form, "destination"))
		if value == "" {
			result.Error = "path is required"
			return result
		}
		// Missing parents are allowed because the destination is created
		// recursively; Resolver still rejects an existing destination and a
		// file standing where an ancestor belongs.
		resolved, err := destination.New(destination.Options{Paths: deps.ProjectPaths, Home: deps.Home, AllowMissingParent: true}).Resolve(value)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		result.Values["destination"] = resolved.Path
		if deps.AddProjectPath != nil && resolved.AddParent {
			result.Notice = fmt.Sprintf("parent %q is not in project paths; choose whether to add it", resolved.Parent)
		}
		return result
	}
}

func directoryFormSubmit(deps Deps) func(context.Context, formValues, uint64) tea.Cmd {
	return func(actionCtx context.Context, values formValues, generation uint64) tea.Cmd {
		target := strings.TrimSpace(values.String("destination"))
		addParent := values.Bool("add-parent")
		createDirectory, addPath := deps.CreateDirectory, deps.AddProjectPath
		return func() tea.Msg {
			if createDirectory == nil {
				return actionMsg{kind: "mkdir", destination: target, generation: generation, err: errors.New("creating directories is not supported")}
			}
			if err := createDirectory(actionCtx, target); err != nil {
				return actionMsg{kind: "mkdir", destination: target, generation: generation, err: err}
			}
			if addParent && addPath != nil {
				if err := addPath(actionCtx, filepath.Dir(target)); err != nil {
					return actionMsg{kind: "mkdir", destination: target, generation: generation, err: fmt.Errorf("add project parent: %w", err)}
				}
			}
			return actionMsg{kind: "mkdir", destination: target, generation: generation}
		}
	}
}

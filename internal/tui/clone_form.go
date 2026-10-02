package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	cloneflow "github.com/vieitesss/nexo/internal/clone"
	"github.com/vieitesss/nexo/internal/config"
)

func newCloneForm(deps Deps, ctx context.Context) *stagedForm {
	clone := deps.CloneConfig.WithDefaults()
	destinationFields := []formField{{ID: "destination", Label: "path", Value: ""}}
	if deps.AddProjectPath != nil {
		destinationFields = append(destinationFields, formField{ID: "add-parent", Label: "add parent (yes/no)", Value: "no"})
	}
	form := newStagedForm(ctx, []formStage{
		{
			ID: "source", Title: "source", Fields: []formField{
				{ID: "protocol", Label: "protocol", Value: clone.EffectiveProtocol()},
				{ID: "source", Label: "repository or Git operand", Value: ""},
				{ID: "prefix", Label: "prefix (editable)", Value: clone.Prefix(clone.EffectiveProtocol()), Expandable: true},
			},
		},
		{ID: "destination", Title: "destination", Fields: destinationFields},
	}, cloneFormValidation(deps), cloneFormSubmit(deps))
	form.busyLabel = "cloning…"
	return form
}

func cloneFormValidation(deps Deps) func(*stagedForm) formValidation {
	return func(form *stagedForm) formValidation {
		protocol := strings.ToLower(strings.TrimSpace(formValue(form, "protocol")))
		source := formValue(form, "source")
		prefix := formValue(form, "prefix")
		result := formValidation{Values: map[string]string{}}
		if protocol != config.CloneProtocolSSH && protocol != config.CloneProtocolHTTPS {
			result.Error = "protocol must be ssh or https"
			return result
		}
		validatedSource, err := cloneflow.ValidateSource(source)
		if err != nil {
			result.Error = cloneFormValidationError(err)
			return result
		}
		source = validatedSource
		defaults := deps.CloneConfig.WithDefaults()
		if prefix == "" || (protocol == config.CloneProtocolHTTPS && prefix == config.DefaultSSHPrefix) || (protocol == config.CloneProtocolSSH && prefix == config.DefaultHTTPSPrefix) {
			prefix = defaults.Prefix(protocol)
			result.Values["prefix"] = prefix
		}
		if form.stage == 0 {
			return result
		}
		destinationValue := formValue(form, "destination")
		request, resolved, err := cloneflow.Validate(cloneflow.Request{
			Source:      source,
			Prefix:      prefix,
			Destination: destinationValue,
		}, deps.ProjectPaths, deps.Home)
		if err != nil {
			result.Error = cloneFormValidationError(err)
			return result
		}
		result.Values["destination"] = request.Destination
		if deps.AddProjectPath != nil {
			choice := strings.ToLower(strings.TrimSpace(formValue(form, "add-parent")))
			if choice == "" {
				choice = "no"
				result.Values["add-parent"] = choice
			}
			if choice != "yes" && choice != "no" {
				result.Error = "add parent must be yes or no"
				return result
			}
			if resolved.AddParent {
				result.Notice = fmt.Sprintf("parent %q is not in project paths; choose whether to add it", resolved.Parent)
			}
		}
		return result
	}
}

func cloneFormValidationError(err error) string {
	if errors.Is(err, cloneflow.ErrSourceRequired) {
		return "repository source is required"
	}
	return err.Error()
}

func cloneFormSubmit(deps Deps) func(context.Context, map[string]string, uint64) tea.Cmd {
	return func(actionCtx context.Context, values map[string]string, generation uint64) tea.Cmd {
		source := strings.TrimSpace(values["source"])
		prefix := strings.TrimSpace(values["prefix"])
		destinationValue := strings.TrimSpace(values["destination"])
		addParent := strings.EqualFold(strings.TrimSpace(values["add-parent"]), "yes")
		cloneProject := deps.CloneProject
		return func() tea.Msg {
			if cloneProject == nil {
				return actionMsg{kind: "clone", destination: destinationValue, generation: generation, err: errors.New("cloning is not supported")}
			}
			if err := cloneProject(actionCtx, cloneflow.Request{
				Source:      source,
				Prefix:      prefix,
				Destination: destinationValue,
				AddParent:   addParent,
			}); err != nil {
				return actionMsg{kind: "clone", destination: destinationValue, generation: generation, err: err}
			}
			return actionMsg{kind: "clone", destination: destinationValue, generation: generation}
		}
	}
}

func formValue(form *stagedForm, id string) string {
	for _, stage := range form.stages {
		for _, field := range stage.Fields {
			if field.ID == id {
				return field.Value
			}
		}
	}
	return ""
}

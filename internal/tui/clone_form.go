package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/destination"
)

func newCloneForm(deps Deps, ctx context.Context) *stagedForm {
	clone := deps.CloneConfig.WithDefaults()
	return newStagedForm([]formStage{
		{
			ID: "source", Title: "source", Fields: []formField{
				{ID: "protocol", Label: "protocol", Value: clone.EffectiveProtocol()},
				{ID: "source", Label: "repository (owner/repo)", Value: ""},
			},
		},
		{ID: "destination", Title: "destination", Fields: []formField{
			{ID: "destination", Label: "path", Value: ""},
		}},
	}, cloneFormValidation(deps), cloneFormSubmit(deps, ctx))
}

func cloneFormValidation(deps Deps) func(*stagedForm) string {
	return func(form *stagedForm) string {
		protocol := strings.ToLower(strings.TrimSpace(formValue(form, "protocol")))
		source := strings.TrimSpace(formValue(form, "source"))
		if protocol != config.CloneProtocolSSH && protocol != config.CloneProtocolHTTPS {
			return "protocol must be ssh or https"
		}
		if source == "" {
			return "repository source is required"
		}
		if !strings.Contains(source, "/") && !strings.Contains(source, ":") {
			return "repository source must be owner/repo or a complete Git operand"
		}
		if form.stage == 0 {
			return ""
		}
		destinationValue := strings.TrimSpace(formValue(form, "destination"))
		if destinationValue == "" {
			return "destination is required"
		}
		resolved, err := destination.New(destination.Options{Paths: deps.ProjectPaths, Home: deps.Home}).Resolve(destinationValue)
		if err != nil {
			form.notice = ""
			return err.Error()
		}
		setFormValue(form, "destination", resolved.Path)
		if resolved.AddParent {
			form.notice = fmt.Sprintf("parent %q is not in project paths; add it after cloning if desired", resolved.Parent)
		} else {
			form.notice = ""
		}
		return ""
	}
}

func cloneFormSubmit(deps Deps, ctx context.Context) func(map[string]string) tea.Cmd {
	return func(values map[string]string) tea.Cmd {
		protocol := strings.ToLower(strings.TrimSpace(values["protocol"]))
		source := strings.TrimSpace(values["source"])
		clone := deps.CloneConfig.WithDefaults()
		operand := clone.Prefix(protocol) + source
		if strings.Contains(source, "://") || strings.HasPrefix(source, "git@") || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || strings.HasPrefix(source, "/") || strings.HasPrefix(source, "~") {
			operand = source
		}
		destinationValue := values["destination"]
		return func() tea.Msg {
			if deps.CloneProject == nil {
				return actionMsg{kind: "clone", destination: destinationValue, err: errors.New("cloning is not supported")}
			}
			err := deps.CloneProject(ctx, operand, destinationValue)
			return actionMsg{kind: "clone", destination: destinationValue, err: err}
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

func setFormValue(form *stagedForm, id, value string) {
	for stageIndex := range form.stages {
		for fieldIndex := range form.stages[stageIndex].Fields {
			if form.stages[stageIndex].Fields[fieldIndex].ID == id {
				form.stages[stageIndex].Fields[fieldIndex].Value = value
				return
			}
		}
	}
}

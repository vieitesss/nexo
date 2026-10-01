package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/destination"
)

var scpCloneOperand = regexp.MustCompile(`^[^/@\s]+@[^:\s]+:.+$`)

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
		source := strings.TrimSpace(formValue(form, "source"))
		prefix := strings.TrimSpace(formValue(form, "prefix"))
		result := formValidation{Values: map[string]string{}}
		if protocol != config.CloneProtocolSSH && protocol != config.CloneProtocolHTTPS {
			result.Error = "protocol must be ssh or https"
			return result
		}
		if source == "" {
			result.Error = "repository source is required"
			return result
		}
		defaults := deps.CloneConfig.WithDefaults()
		if prefix == "" || (protocol == config.CloneProtocolHTTPS && prefix == config.DefaultSSHPrefix) || (protocol == config.CloneProtocolSSH && prefix == config.DefaultHTTPSPrefix) {
			prefix = defaults.Prefix(protocol)
			result.Values["prefix"] = prefix
		}
		if form.stage == 0 {
			return result
		}
		destinationValue := strings.TrimSpace(formValue(form, "destination"))
		if destinationValue == "" {
			result.Error = "destination is required"
			return result
		}
		resolved, err := destination.New(destination.Options{Paths: deps.ProjectPaths, Home: deps.Home}).Resolve(destinationValue)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		result.Values["destination"] = resolved.Path
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

func cloneFormSubmit(deps Deps) func(context.Context, map[string]string, uint64) tea.Cmd {
	return func(actionCtx context.Context, values map[string]string, generation uint64) tea.Cmd {
		protocol := strings.ToLower(strings.TrimSpace(values["protocol"]))
		source := strings.TrimSpace(values["source"])
		clone := deps.CloneConfig.WithDefaults()
		prefix := strings.TrimSpace(values["prefix"])
		if prefix == "" {
			prefix = clone.Prefix(protocol)
		}
		operand := cloneSourceOperand(protocol, prefix, source, deps.Home)
		destinationValue := strings.TrimSpace(values["destination"])
		addParent := strings.EqualFold(strings.TrimSpace(values["add-parent"]), "yes")
		cloneProject, addPath := deps.CloneProject, deps.AddProjectPath
		return func() tea.Msg {
			if cloneProject == nil {
				return actionMsg{kind: "clone", destination: destinationValue, generation: generation, err: errors.New("cloning is not supported")}
			}
			if err := cloneProject(actionCtx, operand, destinationValue); err != nil {
				return actionMsg{kind: "clone", destination: destinationValue, generation: generation, err: err}
			}
			if addParent && addPath != nil {
				if err := addPath(actionCtx, filepath.Dir(destinationValue)); err != nil {
					return actionMsg{kind: "clone", destination: destinationValue, generation: generation, err: fmt.Errorf("add project parent: %w", err)}
				}
			}
			return actionMsg{kind: "clone", destination: destinationValue, generation: generation}
		}
	}
}

func cloneSourceOperand(_ string, prefix, source, home string) string {
	if completeCloneOperand(source) {
		return destination.ExpandHome(source, home)
	}
	return prefix + source
}

func completeCloneOperand(source string) bool {
	source = strings.TrimSpace(source)
	if strings.Contains(source, "://") || scpCloneOperand.MatchString(source) {
		return true
	}
	if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || strings.HasPrefix(source, "/") || strings.HasPrefix(source, "~") || strings.HasPrefix(source, "$HOME/") {
		return true
	}
	// A bare .git path is a local operand, not a repository shorthand.
	return strings.HasSuffix(source, ".git") && !strings.Contains(source, "/")
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

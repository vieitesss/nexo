// Package sessionname validates and renders configured container names.
package sessionname

import (
	"fmt"
	"strings"
	"unicode"
)

// Values are the available namespace and repository names for a project.
type Values struct {
	Org  string
	Team string
	Repo string
}

type part struct {
	text     string
	variable bool
}

// ValidateTemplate checks the supported placeholders and literal syntax.
func ValidateTemplate(template string) error {
	_, err := parseTemplate(template)
	return err
}

func parseTemplate(template string) ([]part, error) {
	if strings.TrimSpace(template) == "" {
		return nil, fmt.Errorf("empty name template")
	}
	var parts []part
	for len(template) > 0 {
		index := strings.IndexAny(template, "{}")
		if index < 0 {
			parts = append(parts, part{text: template})
			break
		}
		if template[index] == '}' {
			return nil, fmt.Errorf("unmatched closing brace in name template")
		}
		parts = append(parts, part{text: template[:index]})
		template = template[index+1:]
		end := strings.IndexByte(template, '}')
		if end < 0 || strings.Contains(template[:end], "{") {
			return nil, fmt.Errorf("unmatched opening brace in name template")
		}
		name := template[:end]
		switch name {
		case "org", "team", "repo":
		default:
			return nil, fmt.Errorf("unknown name-template placeholder (expected org, team, or repo)")
		}
		parts = append(parts, part{text: name, variable: true})
		template = template[end+1:]
		if len(template) == 0 {
			parts = append(parts, part{})
		}
	}
	return parts, nil
}

// Render substitutes values once and normalizes the generated name.
func Render(template string, values Values) (string, error) {
	parts, err := parseTemplate(template)
	if err != nil {
		return "", err
	}
	for index := range parts {
		if !parts[index].variable {
			continue
		}
		switch parts[index].text {
		case "org":
			if strings.TrimSpace(values.Org) == "" {
				return "", fmt.Errorf("missing org for name template")
			}
			parts[index].text = values.Org
		case "repo":
			if strings.TrimSpace(values.Repo) == "" {
				return "", fmt.Errorf("missing repo for name template")
			}
			parts[index].text = values.Repo
		case "team":
			parts[index].text = values.Team
			if values.Team == "" {
				const separators = "/-_"
				if index > 0 && !parts[index-1].variable && strings.TrimRight(parts[index-1].text, separators) != parts[index-1].text {
					parts[index-1].text = strings.TrimRight(parts[index-1].text, separators)
				} else if index+1 < len(parts) && !parts[index+1].variable {
					parts[index+1].text = strings.TrimLeft(parts[index+1].text, separators)
				}
			}
		}
	}
	var name strings.Builder
	for _, p := range parts {
		name.WriteString(p.text)
	}
	assembled := name.String()
	visible := false
	for _, r := range assembled {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("control character in rendered name")
		}
		if unicode.IsGraphic(r) && !unicode.IsSpace(r) {
			visible = true
		}
	}
	result := strings.TrimSpace(assembled)
	if !visible {
		return "", fmt.Errorf("empty rendered name")
	}
	return strings.NewReplacer(".", "_", ":", "_").Replace(result), nil
}

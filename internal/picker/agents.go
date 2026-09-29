package picker

import (
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
)

const (
	// DecorationRed marks an unseen stopped agent that needs attention.
	DecorationRed DecorationStyle = "red"
	// DecorationYellow marks an agent that is working.
	DecorationYellow DecorationStyle = "yellow"
	// DecorationGreen marks an agent that has stopped and was seen.
	DecorationGreen DecorationStyle = "green"
)

// AgentDot returns the most urgent dot for a group of agents in one container.
// Unseen stopped agents outrank working agents, which outrank seen stopped
// agents. Unknown states do not produce a dot.
func AgentDot(agents []capabilities.Agent) (Decoration, bool) {
	best := -1
	var style DecorationStyle
	for _, agent := range agents {
		var rank int
		var candidate DecorationStyle
		switch strings.ToLower(strings.TrimSpace(agent.State)) {
		case "stopped":
			if agent.Seen {
				rank, candidate = 1, DecorationGreen
			} else {
				rank, candidate = 3, DecorationRed
			}
		case "working":
			rank, candidate = 2, DecorationYellow
		default:
			continue
		}
		if rank > best {
			best = rank
			style = candidate
		}
	}
	if best < 0 {
		return Decoration{}, false
	}
	return Decoration{Text: "●", Style: style}, true
}

// ApplyAgentDots decorates container places with the most urgent matching
// agent. The input is copied so asynchronous updates cannot mutate a caller's
// place slice, and prior agent dots are replaced rather than duplicated.
func ApplyAgentDots(places []Place, agents []capabilities.Agent) []Place {
	byContainer := make(map[string][]capabilities.Agent)
	for _, agent := range agents {
		container := strings.TrimSpace(agent.Container)
		if container != "" {
			byContainer[container] = append(byContainer[container], agent)
		}
	}
	updated := append([]Place(nil), places...)
	for index := range updated {
		place := &updated[index]
		decorations := make([]Decoration, 0, len(place.Decorations)+1)
		for _, decoration := range place.Decorations {
			if isAgentDot(decoration) {
				continue
			}
			decorations = append(decorations, decoration)
		}
		if place.Kind == ContainerKind {
			if dot, ok := AgentDot(byContainer[strings.TrimSpace(place.ContainerName)]); ok {
				decorations = append(decorations, dot)
			}
		}
		place.Decorations = decorations
	}
	return updated
}

func isAgentDot(decoration Decoration) bool {
	if decoration.Text != "●" {
		return false
	}
	switch decoration.Style {
	case DecorationRed, DecorationYellow, DecorationGreen:
		return true
	default:
		return false
	}
}

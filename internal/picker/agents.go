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

// ApplyAgentDots decorates places using the agent's stable container identity
// when available, with unique display labels as a compatibility fallback.
func ApplyAgentDots(places []Place, agents []capabilities.Agent) []Place {
	return applyAgentDots(places, agents, "")
}

// ApplyAgentDotsForMachine applies one source machine's agent snapshot without
// touching places owned by other machines. The source machine is supplied by
// the caller because Agent deliberately does not invent a machine identity.
func ApplyAgentDotsForMachine(places []Place, agents []capabilities.Agent, machine string) []Place {
	return applyAgentDots(places, agents, strings.TrimSpace(machine))
}

func applyAgentDots(places []Place, agents []capabilities.Agent, machine string) []Place {
	byID := make(map[string][]capabilities.Agent)
	byLabel := make(map[string][]capabilities.Agent)
	for _, agent := range agents {
		if id := strings.TrimSpace(agent.ContainerID); id != "" {
			byID[id] = append(byID[id], agent)
			continue
		}
		if label := strings.TrimSpace(agent.Container); label != "" {
			byLabel[label] = append(byLabel[label], agent)
		}
	}
	labelCounts := make(map[string]int)
	for _, place := range places {
		if place.Kind != ContainerKind || !sameMachine(place, machine) {
			continue
		}
		label := strings.TrimSpace(place.ContainerName)
		if label != "" {
			labelCounts[label]++
		}
	}

	updated := append([]Place(nil), places...)
	for index := range updated {
		place := &updated[index]
		if place.Kind != ContainerKind || !sameMachine(*place, machine) {
			continue
		}
		decorations := make([]Decoration, 0, len(place.Decorations)+1)
		for _, decoration := range place.Decorations {
			if isAgentDot(decoration) {
				continue
			}
			decorations = append(decorations, decoration)
		}
		matches := byID[strings.TrimSpace(place.ContainerID)]
		if len(matches) == 0 && labelCounts[strings.TrimSpace(place.ContainerName)] == 1 {
			matches = byLabel[strings.TrimSpace(place.ContainerName)]
		}
		if dot, ok := AgentDot(matches); ok {
			decorations = append(decorations, dot)
		}
		place.Decorations = decorations
	}
	return updated
}

func sameMachine(place Place, machine string) bool {
	if machine == "" {
		return true
	}
	placeMachine := strings.TrimSpace(place.Machine)
	if placeMachine == "" {
		placeMachine = LocalMachine()
	}
	return placeMachine == machine
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

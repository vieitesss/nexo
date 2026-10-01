package picker

import (
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func applyAgentDots(places []Place, agents []capabilities.Agent, machine string) []Place {
	byID := make(map[string][]capabilities.Agent)
	byLabel := make(map[string][]capabilities.Agent)
	for _, agent := range agents {
		if id := strings.TrimSpace(agent.ContainerID); id != "" {
			byID[id] = append(byID[id], agent)
			// An agent that names a container is authoritative for that ID;
			// label fallback must not reassign it to another container.
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
		matches := byID[strings.TrimSpace(place.ContainerID)]
		if len(matches) == 0 && strings.TrimSpace(place.RemoteContainerID) != "" {
			matches = byID[strings.TrimSpace(place.RemoteContainerID)]
		}
		if len(matches) == 0 && labelCounts[strings.TrimSpace(place.ContainerName)] == 1 {
			matches = byLabel[strings.TrimSpace(place.ContainerName)]
		}
		place.Agents = append([]capabilities.Agent(nil), matches...)
	}
	return updated
}

func sameMachine(place Place, machine string) bool {
	if machine == "" {
		return true
	}
	placeMachine := NormalizeMachine(place.Machine)
	return placeMachine == NormalizeMachine(machine)
}

package picker

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
)

// associateAgents attaches one machine's agent snapshot to its containers.
// IDs are authoritative; labels are only a unique fallback for backends that
// cannot expose a stable container identity.
func associateAgents(places []Place, agents []capabilities.Agent, machine string) []Place {
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
		if place.Kind != ContainerKind || !belongsToMachine(place, machine) {
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
		if place.Kind != ContainerKind || !belongsToMachine(*place, machine) {
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

func belongsToMachine(place Place, machine string) bool {
	if machine == "" {
		return true
	}
	placeMachine := NormalizeMachine(place.Machine)
	return placeMachine == NormalizeMachine(machine)
}

// AttentionRank orders agents by urgency: unseen stopped, working, then seen stopped.
func AttentionRank(agent capabilities.Agent) int {
	switch strings.ToLower(strings.TrimSpace(agent.State)) {
	case "stopped":
		if !agent.Seen {
			return 0
		}
		return 2
	case "working":
		return 1
	default:
		return 1
	}
}

// agentLess orders agents for views that do not add their own query or
// location grouping. The pane identity breaks ties when targets are equal so
// script output and status rows are deterministic.
func agentLess(left, right capabilities.Agent) bool {
	leftRank, rightRank := AttentionRank(left), AttentionRank(right)
	if leftRank != rightRank {
		return leftRank < rightRank
	}
	if left.Target != right.Target {
		return left.Target < right.Target
	}
	return left.PaneID < right.PaneID
}

// SortAgents returns agents in the canonical urgency order used by the
// scriptable listing and glance row. The source snapshot is not mutated.
func SortAgents(agents []capabilities.Agent) []capabilities.Agent {
	ordered := make([]capabilities.Agent, len(agents))
	copy(ordered, agents)
	sort.SliceStable(ordered, func(i, j int) bool { return agentLess(ordered[i], ordered[j]) })
	return ordered
}

// AgentIdentity returns the backend-native pane identity.
func AgentIdentity(agent capabilities.Agent) string {
	if agent.PaneID != "" {
		return agent.PaneID
	}
	return agent.Target
}

// StoppedAge formats a stopped duration for compact secondary rows.
func StoppedAge(now, since time.Time) string {
	seconds := int(now.Sub(since) / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dd", hours/24)
}

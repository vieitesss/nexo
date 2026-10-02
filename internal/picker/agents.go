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

type agentClassification struct {
	stopped bool
	seen    bool
}

func classifyAgent(agent capabilities.Agent) agentClassification {
	return agentClassification{
		stopped: strings.EqualFold(strings.TrimSpace(agent.State), "stopped"),
		seen:    agent.Seen,
	}
}

// AttentionRank orders agents by urgency: unseen stopped, working, then seen stopped.
func AttentionRank(agent capabilities.Agent) int {
	classification := classifyAgent(agent)
	if classification.stopped {
		if !classification.seen {
			return 0
		}
		return 2
	}
	return 1
}

func cliAgentRank(agent capabilities.Agent) int {
	classification := classifyAgent(agent)
	if classification.stopped {
		if !classification.seen {
			return 0
		}
		return 1
	}
	return 2
}

// agentTieLess breaks rank ties by target and then pane identity so script
// output and status rows are deterministic.
func agentTieLess(left, right capabilities.Agent) bool {
	if left.Target != right.Target {
		return left.Target < right.Target
	}
	return left.PaneID < right.PaneID
}

func sortAgents(agents []capabilities.Agent, rank func(capabilities.Agent) int) []capabilities.Agent {
	ordered := make([]capabilities.Agent, len(agents))
	copy(ordered, agents)
	sort.SliceStable(ordered, func(i, j int) bool {
		leftRank, rightRank := rank(ordered[i]), rank(ordered[j])
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return agentTieLess(ordered[i], ordered[j])
	})
	return ordered
}

// SortAgents returns agents in the canonical urgency order used by glance.
// The source snapshot is not mutated.
func SortAgents(agents []capabilities.Agent) []capabilities.Agent {
	return sortAgents(agents, AttentionRank)
}

// SortAgentsForCLI preserves the scriptable listing order: all stopped agents
// first (unseen, then seen), followed by working agents.
func SortAgentsForCLI(agents []capabilities.Agent) []capabilities.Agent {
	return sortAgents(agents, cliAgentRank)
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

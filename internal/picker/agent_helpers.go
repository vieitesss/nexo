package picker

import (
	"fmt"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
)

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
	return fmt.Sprintf("%dh", minutes/60)
}

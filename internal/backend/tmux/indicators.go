package tmux

import "github.com/vieitesss/nexo/internal/agentdetect"

// MatchesWorkingIndicator reports whether screen contains the live marker for
// harness. The detection rules are shared with the other adapters.
func MatchesWorkingIndicator(harness, screen string, configured map[string]string) bool {
	return agentdetect.Working(harness, screen, configured)
}

package config

import (
	"fmt"
	"slices"
	"strings"
)

// SetupBackends lists the backends `nexo setup` can integrate with locally, in
// the order the guided form offers them. The form's choices and every check
// that accepts an answer read this one list, so they cannot diverge.
var SetupBackends = []string{"tmux", "herdr", "rex"}

// IsSetupBackend reports whether value names a backend setup can configure.
func IsSetupBackend(value string) bool {
	return slices.Contains(SetupBackends, strings.ToLower(strings.TrimSpace(value)))
}

func validateBackend(value string) error {
	if value != "" && value != "auto" && !slices.Contains(SetupBackends, value) {
		return fmt.Errorf("invalid backend %q", value)
	}
	return nil
}

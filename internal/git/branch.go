// Package git contains small shared adapters for Git display metadata.
package git

import (
	"context"
	"strings"

	"github.com/vieitesss/nexo/internal/runner"
)

// Branch returns the current branch for path, or an empty label when Git is
// unavailable or path is not a named branch.
func Branch(ctx context.Context, commandRunner runner.CommandRunner, path string) string {
	if strings.TrimSpace(path) == "" || commandRunner == nil {
		return ""
	}
	output, err := commandRunner.Run(ctx, "git", "-C", path, "branch", "--show-current")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

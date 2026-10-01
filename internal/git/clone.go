package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/vieitesss/nexo/internal/runner"
)

// Clone runs Git's clone operation through the command-runner seam. SOURCE is
// passed as one complete Git operand; nexo does not reinterpret it.
func Clone(ctx context.Context, commandRunner runner.CommandRunner, source, destination string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if commandRunner == nil {
		return errors.New("git clone requires a command runner")
	}
	if source == "" {
		return errors.New("git clone requires a source")
	}
	args := []string{"clone", "--", source}
	if destination != "" {
		args = append(args, destination)
	}
	output, err := commandRunner.Run(ctx, "git", args...)
	if err != nil {
		message := fmt.Sprintf("git clone %q: %v", source, err)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && strings.TrimSpace(string(exitErr.Stderr)) != "" {
			message += ": " + strings.TrimSpace(string(exitErr.Stderr))
		} else if strings.TrimSpace(string(output)) != "" {
			message += ": " + strings.TrimSpace(string(output))
		}
		return fmt.Errorf("%s: %w", message, err)
	}
	return nil
}

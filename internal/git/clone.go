package git

import (
	"context"
	"errors"
	"fmt"

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
	args := []string{"clone", source}
	if destination != "" {
		args = append(args, destination)
	}
	if _, err := commandRunner.Run(ctx, "git", args...); err != nil {
		return fmt.Errorf("git clone %q: %w", source, err)
	}
	return nil
}

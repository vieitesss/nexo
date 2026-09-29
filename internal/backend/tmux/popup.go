package tmux

import (
	"context"
	"errors"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
)

var _ capabilities.Popup = (*Backend)(nil)

// ShowPopup runs command in a tmux popup and closes it when the command exits.
// The command is deliberately passed as one argument so shell quoting remains
// the caller's responsibility and tests can inspect the exact popup request.
func (b *Backend) ShowPopup(ctx context.Context, command string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux popup requires a command runner")
	}
	if strings.TrimSpace(command) == "" {
		return errors.New("tmux popup requires a command")
	}
	if _, ok := b.Runner.(interface {
		RunInteractive(context.Context, string, ...string) error
	}); ok {
		return b.runInteractive(ctx, "display-popup", "-E", command)
	}
	_, err := b.run(ctx, "display-popup", "-E", command)
	return err
}

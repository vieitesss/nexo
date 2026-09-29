package tmux

import (
	"context"
	"errors"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
)

var _ capabilities.ScreenReader = (*Backend)(nil)

// ReadScreen captures the current visible text of a tmux pane for a place
// preview. tmux performs the terminal decoding; nexo keeps the returned text
// untouched so callers can choose how to render it.
func (b *Backend) ReadScreen(ctx context.Context, paneID string) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("tmux screen reader requires a command runner")
	}
	if strings.TrimSpace(paneID) == "" {
		return "", errors.New("tmux screen reader requires a pane id")
	}
	output, err := b.run(ctx, "capture-pane", "-p", "-t", paneID)
	if err != nil {
		return "", err
	}
	return string(output), nil
}

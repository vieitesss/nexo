package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/proxyidentity"
	"github.com/vieitesss/nexo/internal/remote"
)

func runRemotePaste(ctx context.Context, invocation Invocation) error {
	paster, paneID, err := remotePaster(invocation, "paste")
	if err != nil {
		return err
	}
	keys := append([]string(nil), invocation.Args[1:]...)
	target, err := paster.ProxyTarget(ctx, paneID)
	if err != nil {
		return fmt.Errorf("find current proxy: %w", err)
	}
	passThrough := func() error {
		return paster.SendPaneKeys(ctx, paneID, keys)
	}
	if strings.TrimSpace(target) == "" {
		return passThrough()
	}
	reader := remote.ClipboardReader{Runner: invocation.Runner}
	png, err := reader.Read(ctx)
	if err != nil {
		var missing *remote.MissingToolError
		if errors.As(err, &missing) {
			if invocation.Stderr != nil {
				_, _ = fmt.Fprintf(invocation.Stderr, "nexo: %s; passing the key through\n", missing)
			}
			return passThrough()
		}
		return err
	}
	if len(png) == 0 {
		return passThrough()
	}
	identity, err := proxyidentity.ParseTarget(target)
	if err != nil {
		return fmt.Errorf("find current proxy: %w", err)
	}
	path, err := remote.UploadPNG(ctx, invocation.Runner, invocation.Config.Remote.SSHConfig, identity.Machine, png)
	if err != nil {
		return err
	}
	return paster.SendPaneText(ctx, paneID, remote.ShellQuotePath(path))
}

func currentPaneID(kind backend.Kind) string {
	if kind == backend.Herdr {
		return strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
	}
	return strings.TrimSpace(os.Getenv("TMUX_PANE"))
}

func remotePaster(invocation Invocation, operation string) (capabilities.RemotePaste, string, error) {
	paster, ok := invocation.Adapter.(capabilities.RemotePaste)
	if !ok {
		return nil, "", fmt.Errorf("backend %q does not support remote %s", invocation.Selection.Kind, operation)
	}
	paneID := currentPaneID(invocation.Selection.Kind)
	if paneID == "" {
		return nil, "", fmt.Errorf("remote %s requires the current pane (tmux: bind with TMUX_PANE=#{pane_id})", operation)
	}
	return paster, paneID, nil
}

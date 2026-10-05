package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/proxyidentity"
)

func runRemoteClose(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) > 2 {
		return UsageError{Err: errors.New("remote close accepts at most MACHINE or MACHINE/CONTAINER")}
	}
	if invocation.Runner == nil {
		return errors.New("remote close requires a command runner")
	}
	target := ""
	if len(invocation.Args) == 2 {
		target = invocation.Args[1]
	} else {
		paster, ok := invocation.Adapter.(capabilities.RemotePaste)
		if !ok {
			return errors.New("remote close requires MACHINE or MACHINE/CONTAINER outside a proxy session")
		}
		pane := currentPaneID(invocation.Selection.Kind)
		if pane == "" {
			return errors.New("remote close requires MACHINE or MACHINE/CONTAINER outside a proxy session")
		}
		var err error
		target, err = paster.ProxyTarget(ctx, pane)
		if err != nil {
			return fmt.Errorf("find current proxy: %w", err)
		}
		if strings.TrimSpace(target) == "" {
			if invocation.Selection.Kind == backend.Herdr {
				return errors.New("current workspace is not a nexo proxy")
			}
			return errors.New("current session is not a nexo proxy")
		}
	}
	identity, err := proxyidentity.ParseTarget(target)
	if err != nil {
		return UsageError{Err: fmt.Errorf("remote close requires MACHINE or MACHINE/CONTAINER: %w", err)}
	}
	// Closing is local-only. Do not probe the remote machine: links are often
	// down precisely when a stale local proxy needs to be removed.
	if _, ok := invocation.Adapter.(capabilities.RemoteProxy); !ok {
		return fmt.Errorf("backend %q does not support remote proxies", invocation.Selection.Kind)
	}
	killer, ok := invocation.Adapter.(capabilities.ContainerKiller)
	if !ok {
		return fmt.Errorf("backend %q does not support closing proxy containers", invocation.Selection.Kind)
	}
	resolver, ok := invocation.Adapter.(capabilities.ProxySessionResolver)
	if !ok {
		return fmt.Errorf("backend %q does not support resolving proxy sessions", invocation.Selection.Kind)
	}
	// This is deliberately a local backend operation. It only kills the
	// proxy container and never sends a close command to the remote machine.
	sessionID, err := resolver.ProxySessionID(ctx, identity.Machine, identity.Container)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return fmt.Errorf("proxy %s was not found", identity.String())
	}
	return killer.KillContainer(ctx, sessionID)
}

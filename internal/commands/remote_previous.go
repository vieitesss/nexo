package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/proxy"
	"github.com/vieitesss/nexo/internal/proxyidentity"
)

func runRemotePrevious(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) != 1 {
		return UsageError{Err: errors.New("remote previous accepts no arguments")}
	}
	proxier, ok := invocation.Adapter.(capabilities.RemoteProxy)
	if !ok {
		return fmt.Errorf("backend %q does not support remote proxies", invocation.Selection.Kind)
	}
	entries, err := recentContainerEntries(ctx, invocation)
	if err != nil {
		return fmt.Errorf("read previous remote container: %w", err)
	}
	configured := invocation.Config.Remote
	// The current machine is the proxy's machine inside a proxy and the local
	// machine otherwise. The most recent entry on any other machine wins, so
	// from a proxy the local machine is a candidate like any remote.
	current, err := currentProxyMachine(ctx, invocation)
	if err != nil {
		return fmt.Errorf("identify current proxy: %w", err)
	}
	var live map[string]struct{}
	for _, entry := range entries {
		if !entry.Proxy {
			// Local entries live in this machine's history, so they are only
			// eligible from a proxy, and only while the container exists.
			if current == "" || entry.ID == "" {
				continue
			}
			if live == nil {
				if live, err = liveLocalContainerIDs(ctx, invocation); err != nil {
					return fmt.Errorf("list live containers: %w", err)
				}
			}
			if _, ok := live[entry.ID]; !ok {
				continue
			}
			opener, ok := invocation.Adapter.(capabilities.ContainerOpener)
			if !ok {
				return fmt.Errorf("backend %q does not support opening containers", invocation.Selection.Kind)
			}
			return opener.OpenContainer(ctx, entry.ID)
		}
		if entry.Machine == "" {
			continue
		}
		// Skip the machine we are attached to and any machine that is no longer
		// configured; both are definite reasons not to select the entry.
		if proxyidentity.NormalizeMachineName(entry.Machine) == current || !configured.IsMachineConfigured(entry.Machine) {
			continue
		}
		// M-o never lists remotes, so it recreates the proxy with the kind the
		// history recorded. Entries written before that field existed were all
		// tmux proxies.
		remoteKind := entry.RemoteKind
		if remoteKind == "" {
			remoteKind = proxy.RemoteKindTmux
		}
		return proxier.Proxy(ctx, remoteKind, entry.Machine, "")
	}
	return reportNothingEligible(ctx, invocation, "no previous container on another machine")
}

// reportNothingEligible tells the user why a key-bound command did nothing.
// The multiplexer's status line is preferred because a binding's stderr is
// invisible; without it the message is returned as an ordinary error.
func reportNothingEligible(ctx context.Context, invocation Invocation, message string) error {
	if displayer, ok := invocation.Adapter.(capabilities.MessageDisplayer); ok {
		if err := displayer.DisplayMessage(ctx, "nexo: "+message); err == nil {
			return nil
		}
	}
	return errors.New(message)
}

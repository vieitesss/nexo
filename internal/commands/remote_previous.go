package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/proxyidentity"
	"github.com/vieitesss/nexo/internal/remote"
)

func runRemotePrevious(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) != 1 {
		return UsageError{Err: errors.New("remote previous accepts no arguments")}
	}
	proxy, ok := invocation.Adapter.(capabilities.RemoteProxy)
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
	remotes := remoteContainerNames{orchestrator: newRemoteOrchestrator(invocation, nil)}
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
		if entry.Machine == "" || entry.Name == "" {
			continue
		}
		// Skip the machine we are attached to and any machine that is no longer
		// configured; both are definite reasons not to select the entry.
		if proxyidentity.NormalizeMachineName(entry.Machine) == current || !configured.IsMachineConfigured(entry.Machine) {
			continue
		}
		// A proxy to a missing container ends at once, and tmux then detaches
		// the client from the local server.
		if remotes.gone(ctx, entry.Machine, entry.Name) {
			continue
		}
		return proxy.Proxy(ctx, entry.Machine, entry.Name)
	}
	return reportNothingEligible(ctx, invocation, "no previous container on another machine")
}

// remoteContainerNames lists each machine at most once per invocation.
type remoteContainerNames struct {
	orchestrator remote.Orchestrator
	byMachine    map[string]map[string]struct{}
}

// gone reports whether a reachable machine no longer has the container. An
// unreachable machine keeps its entries: the proxy's reconnect loop rides out
// the outage.
func (r *remoteContainerNames) gone(ctx context.Context, machine, container string) bool {
	if r.byMachine == nil {
		r.byMachine = make(map[string]map[string]struct{})
	}
	names, listed := r.byMachine[machine]
	if !listed {
		result := r.orchestrator.List(ctx, []string{machine})[0]
		if result.Error == "" {
			names = make(map[string]struct{}, len(result.Containers))
			for _, listedContainer := range result.Containers {
				names[tmux.CanonicalContainerName(listedContainer.Name)] = struct{}{}
			}
		}
		r.byMachine[machine] = names
	}
	if names == nil {
		return false
	}
	_, ok := names[tmux.CanonicalContainerName(container)]
	return !ok
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

package commands

import (
	"context"
	"errors"
	"strings"
)

func runRemotePassthrough(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) < 2 {
		return UsageError{Err: errors.New("remote passthrough requires at least one key")}
	}
	paster, paneID, err := remotePaster(invocation, "passthrough")
	if err != nil {
		return err
	}
	target, targetErr := paster.ProxyTarget(ctx, paneID)
	if targetErr != nil || strings.TrimSpace(target) == "" {
		// Outside a proxy the key belongs to the user's local binding. Return
		// a handled status so `nexo ... passthrough KEY || local-action` runs
		// the original action without injecting bytes into the pane.
		return HandledError{Reason: "current pane is not inside a proxy"}
	}
	return paster.SendPaneKeys(ctx, paneID, invocation.Args[1:])
}

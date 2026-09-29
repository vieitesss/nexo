package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func TestConfiguredRemoteContainerResolverReturnsClearUnsupportedErrors(t *testing.T) {
	resolver := containerActionResolver(Invocation{Config: config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}}})
	actions, err := resolver.ResolveContainerActions(context.Background(), "devbox")
	if err != nil {
		t.Fatalf("ResolveContainerActions() error = %v, want resolver", err)
	}
	if err := actions.OpenContainer(context.Background(), "c1"); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("OpenContainer() error = %v, want clear unsupported error", err)
	}
	if err := actions.RenameContainer(context.Background(), "c1", "new"); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("RenameContainer() error = %v, want clear unsupported error", err)
	}
	if err := actions.KillContainer(context.Background(), "c1"); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("KillContainer() error = %v, want clear unsupported error", err)
	}
}

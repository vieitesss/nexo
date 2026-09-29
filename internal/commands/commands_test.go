package commands

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
)

func TestRegistryIncludesFoundationSubcommands(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"backend", "list", "open", "agents", "worktree", "remote"} {
		if _, ok := registry.Lookup(name); !ok {
			t.Fatalf("Lookup(%q) not found", name)
		}
	}
}

func TestBackendCommandReportsSelection(t *testing.T) {
	registry := NewRegistry()
	command, ok := registry.Lookup("backend")
	if !ok {
		t.Fatal("backend command not registered")
	}
	var output strings.Builder
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux, Source: "flag", Reason: "selected by --backend=tmux", Capabilities: []string{}},
		Stdout:    &output,
		JSON:      true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), `"backend":"tmux"`) || !strings.Contains(output.String(), `"reason"`) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestBackendCommandRequiresSelectionOutput(t *testing.T) {
	command, _ := NewRegistry().Lookup("backend")
	if err := command.Run(context.Background(), Invocation{Stdout: io.Discard}); err == nil {
		t.Fatal("Run() error = nil, want missing selection error")
	}
}

package commands

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
)

func TestRegistryLeavesFeatureNamesAvailable(t *testing.T) {
	registry := NewRegistry()
	if _, ok := registry.Lookup("backend"); !ok {
		t.Fatal("backend command not registered")
	}
	if _, ok := registry.Lookup("list"); !ok {
		t.Fatal("Lookup(\"list\") did not find the discovery command")
	}
	if _, ok := registry.Lookup("agents"); !ok {
		t.Fatal("Lookup(\"agents\") did not find the agent command")
	}
	for _, name := range []string{"remote"} {
		if _, ok := registry.Lookup(name); ok {
			t.Fatalf("Lookup(%q) found a placeholder; feature registration must own the name", name)
		}
	}
	for _, name := range registry.Names() {
		if name == PlacePickerCommandName {
			t.Fatal("internal place picker command leaked into Registry.Names")
		}
	}
}

type testCommand struct{ name string }

func (c testCommand) Name() string { return c.name }

func (testCommand) Run(context.Context, Invocation) error { return nil }

func TestRegistryRejectsDuplicateNames(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testCommand{name: "feature"}); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := registry.Register(testCommand{name: "feature"}); err == nil {
		t.Fatal("second Register() error = nil, want duplicate-name error")
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

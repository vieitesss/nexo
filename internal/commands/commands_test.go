// Package commands contains the subcommand registry and command implementations.
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
	if _, ok := registry.Lookup("remote"); !ok {
		t.Fatal("Lookup(\"remote\") did not find the remote listing command")
	}
}

type testCommand struct{ name string }

func (c testCommand) Name() string { return c.name }

func (c testCommand) Help() Help {
	return Help{Summary: "Test command.", Usage: "nexo " + c.name}
}

func (testCommand) Run(context.Context, Invocation) error { return nil }

type aliasTestCommand struct {
	name    string
	aliases []string
}

func (c aliasTestCommand) Name() string { return c.name }
func (c aliasTestCommand) Help() Help {
	return Help{Summary: "Alias test command.", Usage: "nexo " + c.name, Aliases: c.aliases}
}
func (aliasTestCommand) Run(context.Context, Invocation) error { return nil }

func TestRegistryRejectsDuplicateNames(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testCommand{name: "feature"}); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := registry.Register(testCommand{name: "feature"}); err == nil {
		t.Fatal("second Register() error = nil, want duplicate-name error")
	}
}

func TestRegistryAliasesAreCollisionSafeAndAtomic(t *testing.T) {
	registry := &Registry{}
	if err := registry.Register(aliasTestCommand{name: "feature", aliases: []string{"f"}}); err != nil {
		t.Fatal(err)
	}
	if got, ok := registry.Lookup("f"); !ok || got.Name() != "feature" {
		t.Fatalf("alias lookup = %v, %v", got, ok)
	}
	if err := registry.Register(aliasTestCommand{name: "f"}); err == nil {
		t.Fatal("canonical name matching an alias was accepted")
	}
	if err := registry.Register(aliasTestCommand{name: "other", aliases: []string{"feature"}}); err == nil {
		t.Fatal("alias matching a canonical name was accepted")
	}
	if _, ok := registry.Lookup("other"); ok {
		t.Fatal("failed registration mutated canonical commands")
	}
	if err := registry.Register(aliasTestCommand{name: "third", aliases: []string{"f"}}); err == nil {
		t.Fatal("duplicate alias was accepted")
	}
	if _, ok := registry.Lookup("third"); ok {
		t.Fatal("failed registration mutated canonical commands")
	}
	if err := registry.Register(aliasTestCommand{name: "clean", aliases: []string{"c"}}); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	PrintHelp(&output, registry, "", nil)
	if !strings.Contains(output.String(), "clean (aliases: c)") {
		t.Fatalf("top-level help omitted alias: %s", output.String())
	}
}

func TestBackendCommandReportsSelection(t *testing.T) {
	registry := NewRegistry()
	command, ok := registry.Lookup("backend")
	if !ok {
		t.Fatal("backend command is not registered")
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

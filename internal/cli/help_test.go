package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/commands"
)

type helpRunner struct{}

func (helpRunner) Run(context.Context, string, ...string) ([]byte, error) { return nil, nil }

func TestHelpListsCommandsAndGlobalFlags(t *testing.T) {
	var out, errOut strings.Builder
	if status := RunWith([]string{"--help"}, &out, &errOut, helpRunner{}, mapEnvironment{}, ""); status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, errOut.String())
	}
	for _, want := range []string{
		"Commands:\n", "  remote", "  worktree", "Global flags:",
		"--backend auto|tmux|herdr", "-h, --help", "Run 'nexo <command> --help' for more.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help missing %q:\n%s", want, out.String())
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if len(line) > 80 {
			t.Errorf("help line is %d columns: %q", len(line), line)
		}
	}
}

func TestNestedHelpUsesOwnSurface(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{name: "remote", args: []string{"remote", "--help"}, want: []string{"Usage: nexo remote <subcommand>", "Subcommands:", "passthrough"}},
		{name: "tmux", args: []string{"help", "tmux"}, want: []string{"Usage: nexo tmux <subcommand>", "glance", "init"}},
		{name: "worktree", args: []string{"worktree", "create", "-h"}, want: []string{"Usage: nexo worktree create [flags] REPO BRANCH DESTINATION", "--new-branch", "--no-focus"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut strings.Builder
			if status := RunWith(test.args, &out, &errOut, helpRunner{}, mapEnvironment{}, ""); status != 0 {
				t.Fatalf("status = %d, stderr = %q", status, errOut.String())
			}
			for _, want := range test.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("help missing %q:\n%s", want, out.String())
				}
			}
		})
	}
}

func TestUsageErrorPointsAtCommandHelp(t *testing.T) {
	var out, errOut strings.Builder
	status := RunWith([]string{"remote", "unknown"}, &out, &errOut, helpRunner{}, mapEnvironment{}, "")
	if status != 2 || !strings.Contains(errOut.String(), "nexo remote <subcommand>") || !strings.Contains(errOut.String(), "nexo remote --help") {
		t.Fatalf("status = %d, stderr = %q", status, errOut.String())
	}
}

func TestBuiltinCommandsHaveHelpMetadata(t *testing.T) {
	registry := commands.NewRegistry()
	for _, name := range registry.Names() {
		command, _ := registry.Lookup(name)
		provider, ok := command.(commands.HelpProvider)
		if !ok || strings.TrimSpace(provider.Help().Summary) == "" {
			t.Errorf("command %q has no summary", name)
		}
	}
}

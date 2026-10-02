package cli

import (
	"context"
	"os"
	"path/filepath"
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

func TestGlobalFlagsAreDocumentedAndParsed(t *testing.T) {
	var out, errOut strings.Builder
	if status := RunWith([]string{"--help"}, &out, &errOut, helpRunner{}, mapEnvironment{}, ""); status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, errOut.String())
	}
	for _, test := range []struct {
		flag string
		args []string
	}{
		{flag: "--backend", args: []string{"--backend", "tmux", "backend"}},
		{flag: "-c, --config-file PATH", args: []string{"--config-file", "/tmp/nexo.toml", "backend"}},
		{flag: "--json", args: []string{"--json", "backend"}},
		{flag: "-h, --help", args: []string{"--help"}},
		{flag: "--version", args: []string{"--version"}},
	} {
		if _, err := Parse(test.args); err != nil {
			t.Errorf("Parse(%v) error = %v", test.args, err)
		}
		if !strings.Contains(out.String(), test.flag) {
			t.Errorf("help missing parsed global flag %q: %s", test.flag, out.String())
		}
	}
	if _, err := Parse([]string{"--undocumented-global", "backend"}); err == nil {
		t.Fatal("parser accepted an undocumented global flag")
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

func TestEveryNestedHelpPathResolves(t *testing.T) {
	for _, test := range []struct {
		name string
		path []string
	}{
		{name: "agents watch", path: []string{"agents", "watch"}},
		{name: "remote attach", path: []string{"remote", "attach"}},
		{name: "remote close", path: []string{"remote", "close"}},
		{name: "remote list", path: []string{"remote", "list"}},
		{name: "remote new", path: []string{"remote", "new"}},
		{name: "remote open", path: []string{"remote", "open"}},
		{name: "remote passthrough", path: []string{"remote", "passthrough"}},
		{name: "remote paste", path: []string{"remote", "paste"}},
		{name: "remote previous", path: []string{"remote", "previous"}},
		{name: "tmux init", path: []string{"tmux", "init"}},
		{name: "tmux glance on", path: []string{"tmux", "glance", "on"}},
		{name: "tmux glance off", path: []string{"tmux", "glance", "off"}},
		{name: "tmux glance toggle", path: []string{"tmux", "glance", "toggle"}},
		{name: "worktree create", path: []string{"worktree", "create"}},
		{name: "worktree extract", path: []string{"worktree", "extract"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, args := range [][]string{
				append(append([]string(nil), test.path...), "--help"),
				append(append([]string(nil), test.path...), "-h"),
				append([]string{"help"}, test.path...),
			} {
				var out, errOut strings.Builder
				if status := RunWith(args, &out, &errOut, helpRunner{}, mapEnvironment{}, ""); status != 0 || errOut.Len() != 0 {
					t.Fatalf("args=%v status=%d stdout=%q stderr=%q", args, status, out.String(), errOut.String())
				}
				want := "Usage: nexo " + strings.Join(test.path, " ")
				if !strings.Contains(out.String(), want) {
					t.Fatalf("args=%v output=%q, missing %q", args, out.String(), want)
				}
			}
		})
	}
}

func TestUsageErrorsIncludeUsageAndHelpHint(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "nested", args: []string{"remote", "unknown"}, want: "nexo: unknown remote subcommand \"unknown\" (want list, open, new, close, paste, passthrough, or previous)\nUsage: nexo remote <subcommand>\nRun 'nexo remote --help' for more.\n"},
		{name: "top-level", args: []string{"unknown"}, want: "nexo: unknown subcommand \"unknown\"\nUsage: nexo [flags] <command> [args]\nRun 'nexo --help' for more.\n"},
		{name: "target", args: []string{"--backend", "tmux", "remote", "open", "not-a-target"}, want: "nexo: remote open requires MACHINE/CONTAINER: remote target must be MACHINE/CONTAINER\nUsage: nexo remote open MACHINE/CONTAINER\nRun 'nexo remote open --help' for more.\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut strings.Builder
			status := RunWith(test.args, &out, &errOut, helpRunner{}, mapEnvironment{}, "")
			if status != 2 || out.Len() != 0 || errOut.String() != test.want {
				t.Fatalf("status = %d, stdout = %q, stderr = %q", status, out.String(), errOut.String())
			}
		})
	}
}

func TestBuiltinCommandsHaveHelpMetadata(t *testing.T) {
	registry := commands.NewRegistry()
	for _, name := range registry.Names() {
		command, _ := registry.Lookup(name)
		if strings.TrimSpace(command.Help().Summary) == "" || strings.TrimSpace(command.Help().Usage) == "" {
			t.Errorf("command %q has incomplete help metadata", name)
		}
	}
}

func TestHelpGoldens(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		file string
	}{
		{name: "top-level", args: []string{"--help"}, file: "help-top.golden"},
		{name: "remote", args: []string{"remote", "--help"}, file: "help-remote.golden"},
		{name: "tmux", args: []string{"tmux", "--help"}, file: "help-tmux.golden"},
		{name: "worktree", args: []string{"worktree", "--help"}, file: "help-worktree.golden"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut strings.Builder
			if status := RunWith(test.args, &out, &errOut, helpRunner{}, mapEnvironment{}, ""); status != 0 {
				t.Fatalf("status = %d, stderr = %q", status, errOut.String())
			}
			want, err := os.ReadFile(filepath.Join("testdata", test.file))
			if err != nil {
				t.Fatal(err)
			}
			if out.String() != string(want) {
				t.Fatalf("help differs from %s:\n--- got ---\n%s--- want ---\n%s", test.file, out.String(), want)
			}
			if errOut.Len() != 0 {
				t.Fatalf("stderr = %q", errOut.String())
			}
		})
	}
}

func TestNestedHelpResolvesEveryGlanceAction(t *testing.T) {
	for _, action := range []string{"on", "off", "toggle"} {
		t.Run(action, func(t *testing.T) {
			var out, errOut strings.Builder
			if status := RunWith([]string{"tmux", "glance", action, "--help"}, &out, &errOut, helpRunner{}, mapEnvironment{}, ""); status != 0 {
				t.Fatalf("status = %d, stderr = %q", status, errOut.String())
			}
			want := "Usage: nexo tmux glance " + action
			if !strings.Contains(out.String(), want) || errOut.Len() != 0 {
				t.Fatalf("stdout = %q, stderr = %q", out.String(), errOut.String())
			}
		})
	}
	for _, args := range [][]string{
		{"help", "tmux", "glance", "on"},
		{"help", "tmux", "glance", "off"},
		{"help", "tmux", "glance", "toggle"},
	} {
		var out, errOut strings.Builder
		if status := RunWith(args, &out, &errOut, helpRunner{}, mapEnvironment{}, ""); status != 0 || !strings.Contains(out.String(), "Usage: nexo tmux glance "+args[len(args)-1]) {
			t.Fatalf("args=%v status=%d stdout=%q stderr=%q", args, status, out.String(), errOut.String())
		}
	}
}

func TestNestedSyntaxErrorsExitTwoWithUsage(t *testing.T) {
	for _, args := range [][]string{
		{"--backend", "herdr", "tmux", "bogus"},
		{"--backend", "herdr", "tmux", "init", "extra"},
		{"--backend", "tmux", "remote", "open", "not-a-target"},
	} {
		var out, errOut strings.Builder
		if status := RunWith(args, &out, &errOut, helpRunner{}, mapEnvironment{}, ""); status != 2 {
			t.Errorf("args=%v status=%d, stderr=%q", args, status, errOut.String())
		}
		if out.Len() != 0 || !strings.Contains(errOut.String(), "Usage: ") || !strings.Contains(errOut.String(), "Run '") {
			t.Errorf("args=%v stdout=%q stderr=%q", args, out.String(), errOut.String())
		}
	}
}

func TestHelpWidthWrapsAllOutput(t *testing.T) {
	var out strings.Builder
	commands.PrintHelpWithWidth(&out, commands.NewRegistry(), "", nil, 40)
	for _, line := range strings.Split(out.String(), "\n") {
		if len(line) > 40 {
			t.Fatalf("line is %d columns: %q", len(line), line)
		}
	}
}

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/commands"
)

func TestParseGlobalFlagsBeforeAndAfterCommand(t *testing.T) {
	parsed, err := Parse([]string{"--backend=tmux", "backend", "--json", "-c", "/tmp/nexo.toml"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.Command != "backend" || !parsed.JSON || parsed.ConfigPath != "/tmp/nexo.toml" || parsed.Backend == nil || *parsed.Backend != "tmux" {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestParseUpgradeVersionIsCommandFlag(t *testing.T) {
	parsed, err := Parse([]string{"upgrade", "--version", "v1.2.3"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.Version || parsed.Command != "upgrade" || strings.Join(parsed.Args, " ") != "--version v1.2.3" {
		t.Fatalf("parsed = %#v, want command version flag", parsed)
	}
}

func TestParseRequiresCommand(t *testing.T) {
	if _, err := Parse(nil); err == nil {
		t.Fatal("Parse() error = nil, want missing command")
	}
}

func TestRunWithTUIUsageErrorsExitTwo(t *testing.T) {
	for _, args := range [][]string{
		{"--backend=tmux", "tui", "unknown"},
		{"--backend=tmux", "tui", "agents", "projects"},
		{"--backend=tmux", "tui", "--", "agents", "projects"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var output, errors strings.Builder
			status := RunWith(args, &output, &errors, funcRunner(func(context.Context, string, ...string) ([]byte, error) {
				t.Fatal("usage errors should not invoke an external program")
				return nil, nil
			}), mapEnvironment{}, "")
			if status != 2 {
				t.Fatalf("status=%d output=%q errors=%q, want 2", status, output.String(), errors.String())
			}
		})
	}
}

func TestRunWithBareCommandPrintsUsage(t *testing.T) {
	var output, errors strings.Builder
	status := RunWith(nil, &output, &errors, funcRunner(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("bare nexo should not invoke an external program")
		return nil, nil
	}), mapEnvironment{}, "")
	if status != 0 || !strings.Contains(output.String(), "Usage: nexo") || errors.Len() != 0 {
		t.Fatalf("status=%d output=%q errors=%q", status, output.String(), errors.String())
	}
}

func TestRunWithVersionDoesNotRequireCommandOrBackend(t *testing.T) {
	var output, errors strings.Builder
	status := RunWith([]string{"--version"}, &output, &errors, funcRunner(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("--version should not invoke an external program")
		return nil, nil
	}), mapEnvironment{}, "")
	if status != 0 {
		t.Fatalf("status = %d, errors = %q", status, errors.String())
	}
	if got, want := output.String(), "nexo dev\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if errors.Len() != 0 {
		t.Fatalf("errors = %q", errors.String())
	}
}

func TestRunWithReportsBackendOutsideMultiplexerAsJSON(t *testing.T) {
	var output, errors strings.Builder
	status := RunWith([]string{"backend", "--json"}, &output, &errors, funcRunner(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("backend command should not invoke an external program")
		return nil, nil
	}), mapEnvironment{}, "")
	if status != 0 {
		t.Fatalf("status = %d, errors = %q", status, errors.String())
	}
	if !strings.Contains(output.String(), `"backend":"none"`) {
		t.Fatalf("output = %q", output.String())
	}
}

type captureCommand struct {
	invocation commands.Invocation
}

func (captureCommand) Name() string { return "capture" }
func (captureCommand) Help() commands.Help {
	return commands.Help{Summary: "Capture an invocation.", Usage: "nexo capture"}
}

func (c *captureCommand) Run(_ context.Context, invocation commands.Invocation) error {
	c.invocation = invocation
	return nil
}

func TestRunWithPassesConfigRunnerAndAdapter(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("backend = \"tmux\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture := &captureCommand{}
	registry := commands.NewRegistry()
	if err := registry.Register(capture); err != nil {
		t.Fatal(err)
	}
	fakeRunner := funcRunner(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("selected tmux backend should not need an external call")
		return nil, nil
	})

	status := RunWithRegistry(registry, []string{"-c", configPath, "capture"}, &strings.Builder{}, &strings.Builder{}, fakeRunner, mapEnvironment{}, "")
	if status != 0 {
		t.Fatalf("status = %d", status)
	}
	if capture.invocation.Config.Backend != "tmux" {
		t.Fatalf("config = %#v, want typed config", capture.invocation.Config)
	}
	if capture.invocation.Runner == nil {
		t.Fatal("runner = nil, want injected command runner")
	}
	adapter, ok := capture.invocation.Adapter.(*tmux.Backend)
	if !ok || adapter == nil {
		t.Fatalf("adapter = %#v, want *tmux.Backend", capture.invocation.Adapter)
	}
	if capture.invocation.Selection.Adapter != adapter {
		t.Fatal("selection adapter and invocation adapter differ")
	}
}

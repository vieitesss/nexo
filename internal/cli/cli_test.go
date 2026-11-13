package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

func TestRunOpenUsesConfiguredSessionName(t *testing.T) {
	root := filepath.Join(t.TempDir(), "acme")
	project := filepath.Join(root, "api")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.toml")
	contents := fmt.Sprintf("backend = \"tmux\"\npaths = [%q]\n[session]\nname-template = \"{org}/{repo}\"\n", root)
	if err := os.WriteFile(configPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		exists, fail bool
		wantStatus   int
	}{
		{name: "created"}, {name: "reused", exists: true}, {name: "backend error", fail: true, wantStatus: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			r := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "tmux" {
					t.Fatalf("unexpected naming discovery: %s %v", name, args)
				}
				calls = append(calls, append([]string(nil), args...))
				switch strings.Join(args, " ") {
				case "-u has-session -t =acme/api":
					if !tc.exists {
						return []byte("can't find session"), errors.New("can't find session")
					}
					return nil, nil
				case "-u new-session -d -s acme/api -c " + project:
					if tc.fail {
						return nil, errors.New("creation refused")
					}
					return nil, nil
				case "-u display-message -p -t =acme/api: #{session_id}":
					return []byte("$7\n"), nil
				default:
					t.Fatalf("unexpected command: %v", args)
					return nil, nil
				}
			})
			var output, stderr strings.Builder
			status := RunWith([]string{"-c", configPath, "open", "--no-focus", project, "--json"}, &output, &stderr, r, mapEnvironment{}, "")
			if status != tc.wantStatus {
				t.Fatalf("status=%d output=%s stderr=%s", status, &output, &stderr)
			}
			if tc.fail {
				if output.Len() != 0 || !strings.Contains(stderr.String(), "creation refused") {
					t.Fatalf("output=%s stderr=%s", &output, &stderr)
				}
				return
			}
			want := fmt.Sprintf("{\"backend\":\"tmux\",\"id\":\"$7\",\"name\":\"acme/api\",\"path\":%q,\"created\":%t}\n", project, !tc.exists)
			if output.String() != want || stderr.Len() != 0 {
				t.Fatalf("output=%s stderr=%s; want %s", &output, &stderr, want)
			}
			wantCalls := [][]string{{"-u", "has-session", "-t", "=acme/api"}}
			if !tc.exists {
				wantCalls = append(wantCalls, []string{"-u", "new-session", "-d", "-s", "acme/api", "-c", project})
			}
			wantCalls = append(wantCalls, []string{"-u", "display-message", "-p", "-t", "=acme/api:", "#{session_id}"})
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("calls=%#v, want %#v", calls, wantCalls)
			}
		})
	}
}

func TestRunListDoesNotResolveSessionNames(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "api")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("backend = \"tmux\"\npaths = [%q]\n[session]\nname-template = \"{team}\"\n", root)), 0600); err != nil {
		t.Fatal(err)
	}
	var output, stderr strings.Builder
	status := RunWith([]string{"-c", configPath, "list", "--json"}, &output, &stderr, funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		t.Fatalf("list invoked %s %v", name, args)
		return nil, nil
	}), mapEnvironment{}, "")
	if status != 0 || stderr.Len() != 0 || !strings.Contains(output.String(), project) {
		t.Fatalf("status=%d output=%s stderr=%s", status, &output, &stderr)
	}
}

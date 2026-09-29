package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/runner"
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

func TestParseRequiresCommand(t *testing.T) {
	if _, err := Parse(nil); err == nil {
		t.Fatal("Parse() error = nil, want missing command")
	}
}

func TestRunWithReportsBackendOutsideMultiplexerAsJSON(t *testing.T) {
	var output, errors strings.Builder
	status := RunWith([]string{"backend", "--json"}, &output, &errors, runner.Func(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("backend command should not invoke an external program")
		return nil, nil
	}), backend.MapEnvironment{}, "")
	if status != 0 {
		t.Fatalf("status = %d, errors = %q", status, errors.String())
	}
	if !strings.Contains(output.String(), `"backend":"none"`) {
		t.Fatalf("output = %q", output.String())
	}
}

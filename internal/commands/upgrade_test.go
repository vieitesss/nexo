package commands

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
	upgradeversion "github.com/vieitesss/nexo/internal/upgrade"
)

func TestUpgradeAndUpdateAreRegisteredAliases(t *testing.T) {
	registry := NewRegistry()
	upgrade, ok := registry.Lookup("upgrade")
	if !ok {
		t.Fatal("upgrade command is not registered")
	}
	update, ok := registry.Lookup("update")
	if !ok {
		t.Fatal("update command is not registered")
	}
	if upgrade.Name() != "upgrade" || update.Name() != "upgrade" {
		t.Fatalf("names = %q and %q, want the canonical upgrade command", upgrade.Name(), update.Name())
	}
}

func TestParseUpgradeVersionAndMachineFlags(t *testing.T) {
	got, err := parseUpgradeArgs([]string{"--version", "v1.2.3", "--machine=devbox", "--machine", "gpu1", "--dry-run"})
	if err != nil {
		t.Fatalf("parseUpgradeArgs() error = %v", err)
	}
	if got.version != "v1.2.3" || !got.dryRun || len(got.machines) != 2 {
		t.Fatalf("options = %#v", got)
	}
}

func TestUpgradeDryRunPrintsOneLinePerSelectedMachine(t *testing.T) {
	var output strings.Builder
	fake := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch {
		case name == "git":
			return []byte("abc\trefs/tags/v2.0.0\n"), nil
		case name == "uname" && len(args) == 1 && args[0] == "-s":
			return []byte("Linux\n"), nil
		case name == "uname" && len(args) == 1 && args[0] == "-m":
			return []byte("x86_64\n"), nil
		case name == "sh" && len(args) == 2 && args[0] == "-c" && args[1] == "command -v nexo":
			return []byte("/usr/local/bin/nexo\n"), nil
		case name == "brew":
			return nil, errors.New("brew not installed")
		case name == "/usr/local/bin/nexo":
			return []byte("nexo v1.0.0\n"), nil
		case name == "go":
			return nil, errors.New("go not installed")
		case name == "ssh":
			remote := args[len(args)-1]
			switch {
			case strings.Contains(remote, "'uname' '-s'"):
				return []byte("Linux\n"), nil
			case strings.Contains(remote, "'uname' '-m'"):
				return []byte("x86_64\n"), nil
			case strings.Contains(remote, "command -v nexo"):
				return []byte("/usr/local/bin/nexo\n"), nil
			case strings.Contains(remote, "'nexo' '--version'") || strings.Contains(remote, "'/usr/local/bin/nexo' '--version'"):
				return []byte("nexo v1.0.0\n"), nil
			default:
				return nil, errors.New("remote command unavailable")
			}
		default:
			return nil, errors.New("unexpected command")
		}
	})
	if err := (upgradeCommand{}).Run(context.Background(), Invocation{
		Config: config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Runner: fake,
		Stdout: &output,
		Args:   []string{"--version", "v2.0.0", "--dry-run"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "local: v1.0.0 -> v2.0.0 (dry-run)") || !strings.Contains(lines[1], "devbox:") {
		t.Fatalf("output = %q, want one line for local and devbox", output.String())
	}
}

func TestUpgradeTerminalDryRunUsesLiveProgressTable(t *testing.T) {
	previous := upgradeInteractiveTerminal
	upgradeInteractiveTerminal = func() bool { return true }
	defer func() { upgradeInteractiveTerminal = previous }()
	var output strings.Builder
	fake := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch {
		case name == "git":
			return []byte("abc\trefs/tags/v2.0.0\n"), nil
		case name == "uname" && len(args) == 1 && args[0] == "-s":
			return []byte("Linux\n"), nil
		case name == "uname" && len(args) == 1 && args[0] == "-m":
			return []byte("x86_64\n"), nil
		case name == "sh" && len(args) == 2 && args[0] == "-c" && args[1] == "command -v nexo":
			return []byte("/usr/local/bin/nexo\n"), nil
		case name == "brew":
			return nil, errors.New("brew not installed")
		case name == "/usr/local/bin/nexo":
			return []byte("nexo v1.0.0\n"), nil
		default:
			return nil, errors.New("unexpected command")
		}
	})
	if err := (upgradeCommand{}).Run(context.Background(), Invocation{
		Config: config.Config{}, Runner: fake, Stdout: &output, Stdin: strings.NewReader(""),
		Args: []string{"--version", "v2.0.0", "--dry-run", "--local-only"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "MACHINE") || !strings.Contains(output.String(), "STATUS") || !strings.Contains(output.String(), "DETAIL") {
		t.Fatalf("output = %q, want the live progress table", output.String())
	}
	if !strings.Contains(output.String(), "✓ done") {
		t.Fatalf("output = %q, want a final done row", output.String())
	}
	if !strings.Contains(output.String(), "v1.0.0") || !strings.Contains(output.String(), "v2.0.0") {
		t.Fatalf("output = %q, want old and target versions", output.String())
	}
}

func TestUpgradeCancellationIsNotReportedAsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var once sync.Once
	fake := funcRunner(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "ssh" {
			return nil, errors.New("unexpected command")
		}
		once.Do(func() { close(started) })
		<-ctx.Done()
		return nil, errors.New("signal: killed")
	})
	go func() {
		<-started
		cancel()
	}()
	var output strings.Builder
	err := runUpgradeInteractive(ctx, Invocation{
		Runner: fake, Stdout: &output, Stdin: strings.NewReader(""),
	}, []upgradeMachine{{Name: "slowhost", Remote: true}}, upgradeversion.Version{Release: "v2.0.0", Ref: "v2.0.0"}, false)
	if err == nil || err.Error() != "upgrade cancelled" {
		t.Fatalf("runUpgradeInteractive() error = %v, want upgrade cancelled", err)
	}
	if strings.Contains(output.String(), "✗ failed") || strings.Contains(output.String(), "failed on") {
		t.Fatalf("output = %q, cancellation looked like failure", output.String())
	}
	if !strings.Contains(output.String(), "cancelled") {
		t.Fatalf("output = %q, want cancelled row", output.String())
	}
}

func TestUpgradeReportsFailedMachineAndReturnsError(t *testing.T) {
	var output strings.Builder
	fake := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "git" {
			return []byte("abc\trefs/tags/v2.0.0\n"), nil
		}
		return nil, errors.New("offline")
	})
	err := (upgradeCommand{}).Run(context.Background(), Invocation{
		Config: config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Runner: fake,
		Stdout: &output,
		Args:   []string{"--version", "v2.0.0"},
	})
	if err == nil || !strings.Contains(err.Error(), "failed on 2 machine") {
		t.Fatalf("Run() error = %v, want aggregate failure", err)
	}
	if lines := strings.Split(strings.TrimSpace(output.String()), "\n"); len(lines) != 2 {
		t.Fatalf("output = %q, want one line per machine", output.String())
	}
}

func TestUpgradeMachineSelection(t *testing.T) {
	machines, err := upgradeMachines(config.RemoteConfig{Machines: []string{"devbox", "gpu1"}}, upgradeOptions{machines: []string{"GPU1", "local"}})
	if err != nil {
		t.Fatalf("upgradeMachines() error = %v", err)
	}
	if len(machines) != 2 || machines[0].Name != "gpu1" || machines[1].Name != "local" {
		t.Fatalf("machines = %#v", machines)
	}
}

func TestUpgradeResultLines(t *testing.T) {
	cases := []struct {
		result upgradeResult
		want   string
	}{
		{upgradeResult{machine: "local", old: "v1", new: "v2"}, "local: v1 -> v2"},
		{upgradeResult{machine: "dev", old: "v2", skipped: true}, "dev: v2 (skipped; already on target)"},
		{upgradeResult{machine: "dev", old: "v1", new: "v2", dryRun: true}, "dev: v1 -> v2 (dry-run)"},
		{upgradeResult{machine: "dev", old: "unknown", err: errors.New("offline")}, "dev: unknown -> error: offline"},
	}
	for _, testCase := range cases {
		if got := testCase.result.line(); got != testCase.want {
			t.Errorf("line() = %q, want %q", got, testCase.want)
		}
	}
}

func TestAlreadyOnTarget(t *testing.T) {
	if !alreadyOnTarget("v1.2.3", upgradeversion.Version{Release: "v1.2.3"}) || alreadyOnTarget("v1.2.4", upgradeversion.Version{Release: "v1.2.3"}) {
		t.Fatal("release comparison does not match")
	}
	current := upgradeversion.Version{Release: "main", Ref: "abcdef1234567890", Nightly: true}
	if !alreadyOnTarget("v0.0.0-20240101120000-abcdef123456", current) {
		t.Fatal("current nightly should be recognized by its resolved commit")
	}
	if alreadyOnTarget("v0.0.0-20230101000000-ffffffffffff", current) {
		t.Fatal("a different nightly revision must not be skipped")
	}
}

func TestNightlyLabelPinsTheResolvedCommit(t *testing.T) {
	got := upgradeversion.Version{Release: "main", Ref: "abcdef1234567890", Nightly: true}.Label()
	if got != "main@abcdef123456" {
		t.Fatalf("Label() = %q", got)
	}
}

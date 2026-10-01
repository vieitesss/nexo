package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

func TestParseGlanceAction(t *testing.T) {
	for input, want := range map[string]GlanceAction{"on": GlanceOn, "off": GlanceOff, "toggle": GlanceToggle, " on ": GlanceOn} {
		got, err := ParseGlanceAction(input)
		if err != nil {
			t.Fatalf("ParseGlanceAction(%q) error = %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseGlanceAction(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := ParseGlanceAction("maybe"); err == nil {
		t.Fatal("ParseGlanceAction(\"maybe\") error = nil, want error")
	}
}

func TestStatusRows(t *testing.T) {
	for value, want := range map[string]int{"": 0, "off": 0, "on": 1, "1": 1, "2": 2, "5": 5, "junk": 1} {
		if got := statusRows(value); got != want {
			t.Fatalf("statusRows(%q) = %d, want %d", value, got, want)
		}
	}
}

func TestGlanceIndexFor(t *testing.T) {
	for rows, want := range map[int]int{0: 1, 1: 1, 2: 2, 3: 3, 4: 4} {
		got, err := glanceIndexFor(rows)
		if err != nil {
			t.Fatalf("glanceIndexFor(%d) error = %v", rows, err)
		}
		if got != want {
			t.Fatalf("glanceIndexFor(%d) = %d, want %d", rows, got, want)
		}
	}
	if _, err := glanceIndexFor(5); err == nil {
		t.Fatal("glanceIndexFor(5) error = nil, want all-rows-in-use error")
	}
}

// fakeTmuxServer models the tmux global options and clients a glance command
// reads and writes, so the toggle behavior is testable without a live server.
type fakeTmuxServer struct {
	mu        sync.Mutex
	options   map[string]string
	widths    []int
	panes     string
	commands  [][]string
	listError error
}

func newFakeTmuxServer() *fakeTmuxServer {
	return &fakeTmuxServer{
		options: map[string]string{"status": "on", "status-format[0]": "main", "status-format[1]": "paneinfo"},
	}
}

func (f *fakeTmuxServer) runner() runner.CommandRunner {
	return funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "tmux" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		if len(args) > 0 && args[0] == "-u" {
			args = args[1:]
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.commands = append(f.commands, append([]string(nil), args...))
		if f.listError != nil && args[0] == "list-panes" {
			return nil, f.listError
		}
		switch args[0] {
		case "show-options":
			name := args[len(args)-1]
			return []byte(f.options[name]), nil
		case "set-option":
			if len(args) >= 3 && args[1] == "-gu" {
				delete(f.options, args[2])
				return nil, nil
			}
			if len(args) == 4 {
				f.options[args[2]] = args[3]
				return nil, nil
			}
			return nil, fmt.Errorf("unexpected set-option %v", args)
		case "list-panes":
			return []byte(f.panes), nil
		case "list-clients":
			var out strings.Builder
			for _, width := range f.widths {
				out.WriteString(strconv.Itoa(width))
				out.WriteString("\n")
			}
			return []byte(out.String()), nil
		case "display-message":
			return []byte("/tmp/nexo-fake.sock"), nil
		default:
			return nil, fmt.Errorf("unexpected tmux command %v", args)
		}
	})
}

func (f *fakeTmuxServer) option(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.options[name]
}

func (f *fakeTmuxServer) setOption(name, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.options[name] = value
}

func (f *fakeTmuxServer) sawOption(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, command := range f.commands {
		for _, arg := range command {
			if arg == name {
				return true
			}
		}
	}
	return false
}

func glanceSource(t *testing.T, fake *fakeTmuxServer) *Source {
	t.Helper()
	source := NewAgentSource(fake.runner())
	setStateDir(source, t.TempDir())
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}})
	source.ConfigureGlance(config.GlanceConfig{})
	return source
}

func TestGlanceOnAddsExtraRowAndRaisesStatus(t *testing.T) {
	fake := newFakeTmuxServer()
	source := glanceSource(t, fake)

	if err := source.Glance(context.Background(), GlanceOn); err != nil {
		t.Fatalf("Glance(on) error = %v", err)
	}
	if got := fake.option("status"); got != "2" {
		t.Fatalf("status = %q, want 2", got)
	}
	if got := fake.option("status-format[1]"); got != "nexo │" {
		t.Fatalf("status-format[1] = %q, want the glance row", got)
	}
	if got := fake.option(glanceIndexOption); got != "1" {
		t.Fatalf("%s = %q, want 1", glanceIndexOption, got)
	}
	if got := fake.option(glanceStatusOption); got != "on" {
		t.Fatalf("%s = %q, want saved status on", glanceStatusOption, got)
	}
	if got := fake.option(glanceFormatOption); got != "paneinfo" {
		t.Fatalf("%s = %q, want the saved default row", glanceFormatOption, got)
	}
	if fake.sawOption("status-left") {
		t.Fatal("Glance wrote status-left, want it untouched")
	}
}

func TestGlanceOnUsesTheFirstFreeRow(t *testing.T) {
	fake := newFakeTmuxServer()
	fake.setOption("status", "2")
	fake.setOption("status-format[1]", "user-row")
	source := glanceSource(t, fake)

	if err := source.Glance(context.Background(), GlanceOn); err != nil {
		t.Fatalf("Glance(on) error = %v", err)
	}
	if got := fake.option("status"); got != "3" {
		t.Fatalf("status = %q, want 3", got)
	}
	if got := fake.option("status-format[1]"); got != "user-row" {
		t.Fatalf("status-format[1] = %q, want the user's row preserved", got)
	}
	if got := fake.option("status-format[2]"); got != "nexo │" {
		t.Fatalf("status-format[2] = %q, want the glance row", got)
	}
}

func TestGlanceOnAlreadyOnRefreshesOnly(t *testing.T) {
	fake := newFakeTmuxServer()
	source := glanceSource(t, fake)
	if err := source.Glance(context.Background(), GlanceOn); err != nil {
		t.Fatal(err)
	}
	fake.setOption("status", "2")
	fake.setOption(glanceStatusOption, "ignored")

	if err := source.Glance(context.Background(), GlanceOn); err != nil {
		t.Fatalf("second Glance(on) error = %v", err)
	}
	if got := fake.option(glanceStatusOption); got != "ignored" {
		t.Fatalf("second on re-saved status = %q, want the first saved value", got)
	}
	if got := fake.option("status"); got != "2" {
		t.Fatalf("status = %q, want still 2", got)
	}
}

func TestGlanceOffRestoresStatusAndFormat(t *testing.T) {
	fake := newFakeTmuxServer()
	source := glanceSource(t, fake)
	if err := source.Glance(context.Background(), GlanceOn); err != nil {
		t.Fatal(err)
	}
	if err := source.Glance(context.Background(), GlanceOff); err != nil {
		t.Fatalf("Glance(off) error = %v", err)
	}
	if got := fake.option("status"); got != "on" {
		t.Fatalf("status = %q, want restored on", got)
	}
	if got := fake.option("status-format[1]"); got != "paneinfo" {
		t.Fatalf("status-format[1] = %q, want restored default", got)
	}
	for _, option := range []string{glanceIndexOption, glanceStatusOption, glanceFormatOption} {
		if got := fake.option(option); got != "" {
			t.Fatalf("%s = %q, want cleared", option, got)
		}
	}
	if fake.sawOption("status-left") {
		t.Fatal("Glance(off) wrote status-left, want it untouched")
	}
}

func TestGlanceOffRestoresAnEmptyRowByUnsettingIt(t *testing.T) {
	fake := newFakeTmuxServer()
	fake.setOption("status", "2")
	fake.setOption("status-format[1]", "glance-row")
	fake.setOption(glanceIndexOption, "1")
	fake.setOption(glanceStatusOption, "2")
	fake.setOption(glanceFormatOption, "")
	source := glanceSource(t, fake)

	if err := source.Glance(context.Background(), GlanceOff); err != nil {
		t.Fatalf("Glance(off) error = %v", err)
	}
	if _, ok := fake.options["status-format[1]"]; ok {
		t.Fatalf("status-format[1] still set, want unset for an originally empty row")
	}
}

func TestGlanceOffWithoutStateIsNoop(t *testing.T) {
	fake := newFakeTmuxServer()
	source := glanceSource(t, fake)
	if err := source.Glance(context.Background(), GlanceOff); err != nil {
		t.Fatalf("Glance(off) error = %v", err)
	}
	if got := fake.option("status"); got != "on" {
		t.Fatalf("status = %q, want untouched on", got)
	}
}

func TestGlanceToggleFlipsTheRow(t *testing.T) {
	fake := newFakeTmuxServer()
	source := glanceSource(t, fake)
	if err := source.Glance(context.Background(), GlanceToggle); err != nil {
		t.Fatal(err)
	}
	if got := fake.option(glanceIndexOption); got != "1" {
		t.Fatalf("toggle on index = %q, want 1", got)
	}
	if err := source.Glance(context.Background(), GlanceToggle); err != nil {
		t.Fatal(err)
	}
	if got := fake.option(glanceIndexOption); got != "" {
		t.Fatalf("toggle off index = %q, want cleared", got)
	}
}

func TestGlanceOnRefusesWhenAllRowsAreUsed(t *testing.T) {
	fake := newFakeTmuxServer()
	fake.setOption("status", "5")
	source := glanceSource(t, fake)
	if err := source.Glance(context.Background(), GlanceOn); err == nil {
		t.Fatal("Glance(on) error = nil, want all-rows-in-use error")
	}
}

func TestGlanceRendersAgentsAtClientWidth(t *testing.T) {
	fake := newFakeTmuxServer()
	fake.widths = []int{40, 24}
	source := glanceSource(t, fake)
	agents := []capabilities.Agent{{PaneID: "%1", Target: "zeta:1.0", Harness: "pi", State: "stopped", Seen: false}}
	if err := source.setGlanceRow(context.Background(), 1, agents); err != nil {
		t.Fatalf("setGlanceRow() error = %v", err)
	}
	if got := fake.option("status-format[1]"); got != "nexo │ [● zeta:1.0]" {
		t.Fatalf("status-format[1] = %q, want the agent entry", got)
	}

	fake.widths = []int{15}
	if err := source.setGlanceRow(context.Background(), 1, agents); err != nil {
		t.Fatalf("setGlanceRow() narrow error = %v", err)
	}
	if got := fake.option("status-format[1]"); got != "nexo │ …" {
		t.Fatalf("status-format[1] narrow = %q, want the label and an ellipsis", got)
	}
}

package tmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestAllocateGlanceIndex(t *testing.T) {
	defaults := map[int]glanceOption{
		0: {Present: true, Value: "main"},
		1: {Present: true, Value: tmuxStatusFormat1Default},
	}
	tests := []struct {
		name         string
		globalStatus string
		formats      map[int]glanceOption
		localStatus  map[string]glanceOption
		localFormats map[string]map[int]bool
		want         int
		wantErr      bool
	}{
		{
			name:         "uses the built-in default row",
			globalStatus: "on",
			formats:      defaults,
			want:         1,
		},
		{
			name:         "uses tmux 3.7's built-in pane and session rows",
			globalStatus: "on",
			formats: map[int]glanceOption{
				0: {Present: true, Value: "main"},
				1: {Present: true, Value: tmuxStatusFormat1PaneListDefault},
				2: {Present: true, Value: tmuxStatusFormat2SessionListDefault},
			},
			want: 1,
		},
		{
			name:         "skips a hidden custom row",
			globalStatus: "on",
			formats:      map[int]glanceOption{0: {Present: true, Value: "main"}, 1: {Present: true, Value: "USER-ROW"}},
			want:         2,
		},
		{
			name:         "skips an explicitly empty custom row",
			globalStatus: "on",
			formats:      map[int]glanceOption{0: {Present: true, Value: "main"}, 1: {Present: true, Value: ""}},
			want:         2,
		},
		{
			name:         "skips a custom row above the displayed count",
			globalStatus: "2",
			formats:      map[int]glanceOption{0: {Present: true, Value: "main"}, 1: {Present: true, Value: tmuxStatusFormat1Default}, 2: {Present: true, Value: "hidden"}},
			want:         3,
		},
		{
			name:         "starts beyond a session's local rows",
			globalStatus: "on",
			formats:      defaults,
			localStatus:  map[string]glanceOption{"$0": {Present: true, Value: "3"}},
			want:         3,
		},
		{
			name:         "skips a session-local row",
			globalStatus: "on",
			formats:      defaults,
			localFormats: map[string]map[int]bool{"$0": {1: true}},
			want:         2,
		},
		{
			name:         "refuses when every row is custom",
			globalStatus: "on",
			formats:      map[int]glanceOption{0: {Present: true, Value: "main"}, 1: {Present: true, Value: "a"}, 2: {Present: true, Value: "b"}, 3: {Present: true, Value: "c"}, 4: {Present: true, Value: "d"}},
			wantErr:      true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := allocateGlanceIndex(test.globalStatus, test.formats, test.localStatus, test.localFormats)
			if test.wantErr {
				if err == nil {
					t.Fatalf("allocateGlanceIndex() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("allocateGlanceIndex() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("allocateGlanceIndex() = %d, want %d", got, test.want)
			}
		})
	}
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
	state, ok := glanceStateFromFake(t, fake)
	if !ok {
		t.Fatal("glance state missing after on")
	}
	if state.Index != 1 {
		t.Fatalf("state.Index = %d, want 1", state.Index)
	}
	if !state.Status.Present || state.Status.Value != "on" {
		t.Fatalf("saved status = %+v, want present on", state.Status)
	}
	if !state.Format.Present || state.Format.Value != tmuxStatusFormat1Default {
		t.Fatalf("saved format = %+v, want the built-in default", state.Format)
	}
	if fake.sawOption("status-left") {
		t.Fatal("Glance wrote status-left, want it untouched")
	}
}

func TestGlanceOnSkipsAHiddenCustomRow(t *testing.T) {
	fake := newFakeTmuxServer()
	fake.setOption("status-format[1]", "USER-ROW")
	source := glanceSource(t, fake)

	if err := source.Glance(context.Background(), GlanceOn); err != nil {
		t.Fatalf("Glance(on) error = %v", err)
	}
	if got := fake.option("status"); got != "3" {
		t.Fatalf("status = %q, want 3", got)
	}
	if got := fake.option("status-format[1]"); got != "USER-ROW" {
		t.Fatalf("status-format[1] = %q, want the hidden custom row preserved", got)
	}
	if got := fake.option("status-format[2]"); got != "nexo │" {
		t.Fatalf("status-format[2] = %q, want the glance row", got)
	}

	if err := source.Glance(context.Background(), GlanceOff); err != nil {
		t.Fatalf("Glance(off) error = %v", err)
	}
	if got := fake.option("status"); got != "on" {
		t.Fatalf("status after off = %q, want on", got)
	}
	if got := fake.option("status-format[1]"); got != "USER-ROW" {
		t.Fatalf("status-format[1] after off = %q, want the custom row", got)
	}
	if fake.hasOption("status-format[2]") {
		t.Fatalf("status-format[2] after off = %q, want unset", fake.option("status-format[2]"))
	}
}

func TestGlanceOnRefusesWhenEveryRowIsCustom(t *testing.T) {
	fake := newFakeTmuxServer()
	for index := 1; index < maxStatusRows; index++ {
		fake.setOption(fmt.Sprintf("status-format[%d]", index), fmt.Sprintf("row-%d", index))
	}
	source := glanceSource(t, fake)

	if err := source.Glance(context.Background(), GlanceOn); err == nil {
		t.Fatal("Glance(on) error = nil, want all-rows-in-use error")
	}
	if got := fake.option("status"); got != "on" {
		t.Fatalf("status = %q, want untouched on", got)
	}
	if fake.hasOption(glanceStateOption) {
		t.Fatal("glance state written despite the refusal")
	}
}

func TestGlanceOffPreservesPaddedAndExplicitlyEmptyRows(t *testing.T) {
	for name, value := range map[string]string{"padded": "  PAD  ", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeTmuxServer()
			fake.setOption("status-format[1]", value)
			source := glanceSource(t, fake)

			if err := source.Glance(context.Background(), GlanceOn); err != nil {
				t.Fatalf("Glance(on) error = %v", err)
			}
			if got := fake.option("status-format[1]"); got != value {
				t.Fatalf("status-format[1] during on = %q, want %q", got, value)
			}
			if err := source.Glance(context.Background(), GlanceOff); err != nil {
				t.Fatalf("Glance(off) error = %v", err)
			}
			if !fake.hasOption("status-format[1]") {
				t.Fatal("status-format[1] removed, want the original value restored")
			}
			if got := fake.option("status-format[1]"); got != value {
				t.Fatalf("status-format[1] after off = %q, want %q", got, value)
			}
		})
	}
}

func TestGlanceOnRaisesSessionLocalStatus(t *testing.T) {
	fake := newFakeTmuxServer()
	fake.addSession("$1", "other")
	fake.setSessionOption("$1", "status", "3")
	fake.setSessionOption("$1", "status-format[1]", "LOCAL")
	source := glanceSource(t, fake)

	if err := source.Glance(context.Background(), GlanceOn); err != nil {
		t.Fatalf("Glance(on) error = %v", err)
	}
	if got := fake.option("status"); got != "4" {
		t.Fatalf("global status = %q, want 4", got)
	}
	if got := fake.sessionOption("$1", "status"); got != "4" {
		t.Fatalf("session status = %q, want 4", got)
	}
	if got := fake.sessionOption("$1", "status-format[1]"); got != "LOCAL" {
		t.Fatalf("session local row = %q, want LOCAL preserved", got)
	}
	if got := fake.option("status-format[3]"); got != "nexo │" {
		t.Fatalf("global status-format[3] = %q, want the glance row", got)
	}

	if err := source.Glance(context.Background(), GlanceOff); err != nil {
		t.Fatalf("Glance(off) error = %v", err)
	}
	if got := fake.option("status"); got != "on" {
		t.Fatalf("global status after off = %q, want on", got)
	}
	if got := fake.sessionOption("$1", "status"); got != "3" {
		t.Fatalf("session status after off = %q, want 3", got)
	}
	if got := fake.sessionOption("$1", "status-format[1]"); got != "LOCAL" {
		t.Fatalf("session local row after off = %q, want LOCAL", got)
	}
}

func TestGlanceReconcileHandlesANewSession(t *testing.T) {
	fake := newFakeTmuxServer()
	source := glanceSource(t, fake)
	ctx := context.Background()
	if err := source.Glance(ctx, GlanceOn); err != nil {
		t.Fatal(err)
	}
	fake.addSession("$1", "late")
	fake.setSessionOption("$1", "status", "1")

	if err := source.reconcileGlanceLocked(ctx, nil); err != nil {
		t.Fatalf("reconcile error = %v", err)
	}
	if got := fake.sessionOption("$1", "status"); got != "2" {
		t.Fatalf("new session status = %q, want 2", got)
	}
	state, _ := glanceStateFromFake(t, fake)
	if _, ok := state.Sessions["$1"]; !ok {
		t.Fatal("new session missing from the saved state")
	}

	if err := source.Glance(ctx, GlanceOff); err != nil {
		t.Fatal(err)
	}
	if got := fake.sessionOption("$1", "status"); got != "1" {
		t.Fatalf("new session status after off = %q, want 1", got)
	}
}

func TestGlanceReconcileOverridesASessionLocalRow(t *testing.T) {
	fake := newFakeTmuxServer()
	source := glanceSource(t, fake)
	ctx := context.Background()
	if err := source.Glance(ctx, GlanceOn); err != nil {
		t.Fatal(err)
	}
	fake.addSession("$1", "late")
	fake.setSessionOption("$1", "status", "2")
	fake.setSessionOption("$1", "status-format[1]", "SHADOW")

	if err := source.reconcileGlanceLocked(ctx, nil); err != nil {
		t.Fatalf("reconcile error = %v", err)
	}
	if got := fake.sessionOption("$1", "status-format[1]"); got != "nexo │" {
		t.Fatalf("session local row = %q, want the glance row", got)
	}
	state, _ := glanceStateFromFake(t, fake)
	if saved := state.Sessions["$1"].Format; !saved.Present || saved.Value != "SHADOW" {
		t.Fatalf("saved session format = %+v, want SHADOW", saved)
	}

	if err := source.Glance(ctx, GlanceOff); err != nil {
		t.Fatal(err)
	}
	if got := fake.sessionOption("$1", "status-format[1]"); got != "SHADOW" {
		t.Fatalf("session local row after off = %q, want SHADOW", got)
	}
}

func TestGlanceReconcileSkipsUnchangedWrites(t *testing.T) {
	fake := newFakeTmuxServer()
	dir := t.TempDir()
	source := glanceSourceIn(t, fake, dir)
	ctx := context.Background()
	if err := source.Glance(ctx, GlanceOn); err != nil {
		t.Fatal(err)
	}
	agents := []capabilities.Agent{{PaneID: "%1", Target: "zeta:1.0", Harness: "pi", State: "stopped"}}

	if err := source.reconcileGlanceLocked(ctx, agents); err != nil {
		t.Fatal(err)
	}
	fake.resetCommands()
	if err := source.reconcileGlanceLocked(ctx, agents); err != nil {
		t.Fatal(err)
	}
	if got := fake.writes("status-format[1]"); got != 0 {
		t.Fatalf("identical poll wrote status-format[1] %d times, want 0", got)
	}
	if got := fake.writes("status"); got != 0 {
		t.Fatalf("identical poll wrote status %d times, want 0", got)
	}

	changed := append([]capabilities.Agent(nil), agents...)
	changed[0].Seen = true
	if err := source.reconcileGlanceLocked(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if got := fake.writes("status-format[1]"); got != 1 {
		t.Fatalf("changed row wrote status-format[1] %d times, want 1", got)
	}

	fake.resetCommands()
	fake.widths = []int{8}
	if err := source.reconcileGlanceLocked(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if got := fake.writes("status-format[1]"); got != 1 {
		t.Fatalf("width change wrote status-format[1] %d times, want 1", got)
	}

	// A restarted watcher re-applies the row once even when it is unchanged.
	fake.resetCommands()
	restarted := glanceSourceIn(t, fake, dir)
	if err := restarted.reconcileGlanceLocked(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if got := fake.writes("status-format[1]"); got != 1 {
		t.Fatalf("restarted watcher wrote status-format[1] %d times, want 1", got)
	}
}

func TestGlanceOnRetriesAfterAnInterruptedActivation(t *testing.T) {
	fake := newFakeTmuxServer()
	source := glanceSource(t, fake)
	ctx := context.Background()
	fake.listError = errors.New("boom")

	if err := source.Glance(ctx, GlanceOn); err == nil {
		t.Fatal("Glance(on) error = nil, want the listing error")
	}
	if fake.hasOption(glanceStateOption) {
		t.Fatal("interrupted activation left the enabled marker behind")
	}
	if got := fake.option("status"); got != "on" {
		t.Fatalf("status after failure = %q, want on", got)
	}

	fake.listError = nil
	if err := source.Glance(ctx, GlanceOn); err != nil {
		t.Fatalf("retry Glance(on) error = %v", err)
	}
	if got := fake.option("status"); got != "2" {
		t.Fatalf("status after retry = %q, want 2", got)
	}
	if got := fake.option("status-format[1]"); got != "nexo │" {
		t.Fatalf("status-format[1] after retry = %q, want the glance row", got)
	}

	// A retry after a later interruption (status lowered externally) also
	// finishes activation instead of only refreshing the row.
	fake.setOption("status", "on")
	if err := source.Glance(ctx, GlanceOn); err != nil {
		t.Fatalf("Glance(on) refresh error = %v", err)
	}
	if got := fake.option("status"); got != "2" {
		t.Fatalf("status after refresh = %q, want 2", got)
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
	if !fake.hasOption(glanceStateOption) {
		t.Fatal("toggle on left no state")
	}
	if err := source.Glance(context.Background(), GlanceToggle); err != nil {
		t.Fatal(err)
	}
	if fake.hasOption(glanceStateOption) {
		t.Fatal("toggle off left state behind")
	}
	if got := fake.option("status-format[1]"); got != tmuxStatusFormat1Default {
		t.Fatalf("status-format[1] = %q, want the default restored", got)
	}
}

func TestGlanceIndependentTogglesRestoreTheOriginal(t *testing.T) {
	fake := newFakeTmuxServer()
	dir := t.TempDir()
	first := glanceSourceIn(t, fake, dir)
	second := glanceSourceIn(t, fake, dir)
	ctx := context.Background()

	if err := first.Glance(ctx, GlanceToggle); err != nil {
		t.Fatalf("first toggle error = %v", err)
	}
	if err := second.Glance(ctx, GlanceToggle); err != nil {
		t.Fatalf("second toggle error = %v", err)
	}
	if fake.hasOption(glanceStateOption) {
		t.Fatal("two toggles left the glance enabled")
	}
	if got := fake.option("status"); got != "on" {
		t.Fatalf("status = %q, want on", got)
	}
	if got := fake.option("status-format[1]"); got != tmuxStatusFormat1Default {
		t.Fatalf("status-format[1] = %q, want the default restored", got)
	}
}

func TestGlanceOffWaitsForAConcurrentWatcherPoll(t *testing.T) {
	fake := newFakeTmuxServer()
	dir := t.TempDir()
	watcher := glanceSourceIn(t, fake, dir)
	cli := glanceSourceIn(t, fake, dir)
	ctx := context.Background()
	if err := cli.Glance(ctx, GlanceOn); err != nil {
		t.Fatal(err)
	}

	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fake.before = func(args []string) {
		if len(args) >= 3 && args[0] == "show-options" && args[1] == "-gqv" && args[2] == "status" {
			once.Do(func() {
				close(blocked)
				<-release
			})
		}
	}

	watcherErr := make(chan error, 1)
	go func() { watcherErr <- watcher.reconcileGlanceLocked(ctx, nil) }()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not reach its state read")
	}

	offErr := make(chan error, 1)
	go func() { offErr <- cli.Glance(ctx, GlanceOff) }()
	time.Sleep(50 * time.Millisecond)
	close(release)

	if err := <-watcherErr; err != nil {
		t.Fatalf("watcher reconcile error = %v", err)
	}
	if err := <-offErr; err != nil {
		t.Fatalf("off error = %v", err)
	}
	if fake.hasOption(glanceStateOption) {
		t.Fatal("glance re-enabled itself after off")
	}
	if got := fake.option("status"); got != "on" {
		t.Fatalf("status = %q, want on", got)
	}
	if got := fake.option("status-format[1]"); got != tmuxStatusFormat1Default {
		t.Fatalf("status-format[1] = %q, want the default restored", got)
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
	if got := fake.option("status-format[1]"); got != "nexo │ [#[fg=red]●#[fg=default] zeta:1.0]" {
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

// fakeTmuxServer models the global and per-session options, sessions and
// clients a glance command reads and writes, so the behavior is testable
// without a live server.
type fakeTmuxServer struct {
	mu        sync.Mutex
	global    map[string]string
	sessions  map[string]*fakeSession
	widths    []int
	panes     string
	commands  [][]string
	listError error
	before    func(args []string)
}

type fakeSession struct {
	name  string
	local map[string]string
}

func newFakeTmuxServer() *fakeTmuxServer {
	return &fakeTmuxServer{
		global: map[string]string{
			"status":           "on",
			"status-format[0]": "main",
			"status-format[1]": tmuxStatusFormat1Default,
		},
		sessions: map[string]*fakeSession{
			"$0": {name: "work", local: map[string]string{}},
		},
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
		if f.before != nil {
			f.before(append([]string(nil), args...))
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.commands = append(f.commands, append([]string(nil), args...))
		if f.listError != nil && args[0] == "list-panes" {
			return nil, f.listError
		}
		switch args[0] {
		case "show-options":
			return f.showOptions(args)
		case "set-option":
			return f.handleSetOption(args)
		case "list-sessions":
			return f.listSessions(args)
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

func (f *fakeTmuxServer) showOptions(args []string) ([]byte, error) {
	global, valueOnly, session, positional := parseOptionArgs(args)
	name := ""
	if len(positional) > 0 {
		name = positional[0]
	}
	if name == "status-format" {
		source := f.global
		if session != "" {
			source = f.session(session).local
		}
		var out strings.Builder
		for _, key := range sortedFormatKeys(source) {
			fmt.Fprintf(&out, "%s %q\n", key, source[key])
		}
		return []byte(out.String()), nil
	}
	source := f.global
	if session != "" {
		source = f.session(session).local
	}
	_ = global
	value, ok := source[name]
	if !ok {
		if valueOnly {
			return nil, nil
		}
		return []byte(fmt.Sprintf("%s ''\n", name)), nil
	}
	if valueOnly {
		return []byte(value + "\n"), nil
	}
	return []byte(fmt.Sprintf("%s %q\n", name, value)), nil
}

func (f *fakeTmuxServer) handleSetOption(args []string) ([]byte, error) {
	session := ""
	unset := false
	var positional []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "-t" && i+1 < len(args) {
			i++
			session = args[i]
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			for _, c := range arg[1:] {
				if c == 'u' {
					unset = true
				}
			}
			continue
		}
		positional = append(positional, arg)
	}
	if len(positional) == 0 {
		return nil, fmt.Errorf("set-option missing option name: %v", args)
	}
	target := f.global
	if session != "" {
		target = f.session(session).local
	}
	if unset {
		delete(target, positional[0])
		return nil, nil
	}
	if len(positional) < 2 {
		return nil, fmt.Errorf("set-option missing value: %v", args)
	}
	target[positional[0]] = positional[1]
	return nil, nil
}

var statusFormatRef = regexp.MustCompile(`#\{status-format\[(\d+)\]\}`)

func (f *fakeTmuxServer) listSessions(args []string) ([]byte, error) {
	format := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "-F" && i+1 < len(args) {
			format = args[i+1]
		}
	}
	ids := make([]string, 0, len(f.sessions))
	for id := range f.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out strings.Builder
	for _, id := range ids {
		session := f.sessions[id]
		line := format
		line = strings.ReplaceAll(line, "#{session_id}", id)
		line = strings.ReplaceAll(line, "#{session_name}", session.name)
		line = strings.ReplaceAll(line, "#{status}", f.effective(session, "status"))
		line = statusFormatRef.ReplaceAllStringFunc(line, func(ref string) string {
			match := statusFormatRef.FindStringSubmatch(ref)
			return f.effective(session, "status-format["+match[1]+"]")
		})
		out.WriteString(line)
		out.WriteString("\n")
	}
	return []byte(out.String()), nil
}

func (f *fakeTmuxServer) effective(session *fakeSession, name string) string {
	if value, ok := session.local[name]; ok {
		return value
	}
	return f.global[name]
}

func (f *fakeTmuxServer) session(id string) *fakeSession {
	session, ok := f.sessions[id]
	if !ok {
		session = &fakeSession{name: id, local: map[string]string{}}
		f.sessions[id] = session
	}
	return session
}

func parseOptionArgs(args []string) (global, valueOnly bool, session string, positional []string) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "-t" && i+1 < len(args) {
			i++
			session = args[i]
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			for _, c := range arg[1:] {
				switch c {
				case 'g':
					global = true
				case 'v':
					valueOnly = true
				case 't':
					if i+1 < len(args) {
						i++
						session = args[i]
					}
				}
			}
			continue
		}
		positional = append(positional, arg)
	}
	return global, valueOnly, session, positional
}

func sortedFormatKeys(options map[string]string) []string {
	keys := make([]string, 0, len(options))
	for key := range options {
		if strings.HasPrefix(key, "status-format[") {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		left, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(keys[i], "status-format["), "]"))
		right, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(keys[j], "status-format["), "]"))
		return left < right
	})
	return keys
}

func (f *fakeTmuxServer) option(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.global[name]
}

func (f *fakeTmuxServer) hasOption(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.global[name]
	return ok
}

func (f *fakeTmuxServer) setOption(name, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.global[name] = value
}

func (f *fakeTmuxServer) addSession(id, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[id] = &fakeSession{name: name, local: map[string]string{}}
}

func (f *fakeTmuxServer) sessionOption(id, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok {
		return ""
	}
	return session.local[name]
}

func (f *fakeTmuxServer) setSessionOption(id, name, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.session(id).local[name] = value
}

func (f *fakeTmuxServer) resetCommands() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = nil
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

// writes counts set-option calls that target name.
func (f *fakeTmuxServer) writes(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, command := range f.commands {
		if len(command) == 0 || command[0] != "set-option" {
			continue
		}
		for _, arg := range command {
			if arg == name {
				count++
				break
			}
		}
	}
	return count
}

func glanceStateFromFake(t *testing.T, fake *fakeTmuxServer) (glanceState, bool) {
	t.Helper()
	raw := fake.option(glanceStateOption)
	if raw == "" {
		return glanceState{}, false
	}
	var state glanceState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatalf("decode glance state %q: %v", raw, err)
	}
	return state, true
}

func glanceSource(t *testing.T, fake *fakeTmuxServer) *Source {
	t.Helper()
	return glanceSourceIn(t, fake, t.TempDir())
}

func glanceSourceIn(t *testing.T, fake *fakeTmuxServer, dir string) *Source {
	t.Helper()
	source := NewAgentSource(fake.runner())
	setStateDir(source, dir)
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}})
	source.ConfigureGlance(config.GlanceConfig{})
	return source
}

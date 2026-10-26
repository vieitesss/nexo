package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func TestRexBlockBindsOnlyChosenKeys(t *testing.T) {
	none := RexBlock("/opt/nexo", "", "")
	if strings.Contains(none, "rex.bind(") {
		t.Fatalf("block without keys contains a binding:\n%s", none)
	}
	for _, action := range []string{`name = "nexo_tui"`, `name = "nexo_agents"`, `title = "nexo TUI"`} {
		if !strings.Contains(none, action) {
			t.Fatalf("block without keys lacks %s:\n%s", action, none)
		}
	}

	one := RexBlock("/opt/nexo", "ctrl+space>o", "")
	if got := strings.Count(one, "rex.bind("); got != 1 {
		t.Fatalf("one-key block has %d bindings, want 1:\n%s", got, one)
	}
	if !strings.Contains(one, `rex.bind("ctrl+space>o", "nexo_tui")`) {
		t.Fatalf("one-key block lacks the TUI binding:\n%s", one)
	}

	both := RexBlock("/opt/nexo", "ctrl+space>o", "ctrl+space>a")
	if got := strings.Count(both, "rex.bind("); got != 2 {
		t.Fatalf("two-key block has %d bindings, want 2:\n%s", got, both)
	}
	for _, binding := range []string{
		`rex.bind("ctrl+space>o", "nexo_tui")`,
		`rex.bind("ctrl+space>a", "nexo_agents")`,
	} {
		if !strings.Contains(both, binding) {
			t.Fatalf("two-key block lacks %s:\n%s", binding, both)
		}
	}
}

func TestRexBlockOpensNexoInANewWindowForTheOriginatingClient(t *testing.T) {
	body := RexBlock("/opt/nexo", "ctrl+space>o", "")
	for _, want := range []string{
		`local nexo_bin = "/opt/nexo"`,
		`"REX_CLIENT=" .. tostring(ctx.client_id or "")`,
		`"--backend=rex", "tui"`,
		`rex.session.new_window{`,
		`session_id = ctx.session_id`,
		`window_label = label`,
		`flavor = nexo_terminal .. ".shell"`,
		`command = argv`,
		`login = false`,
		`shell = "none"`,
		`focus = true`,
		`on_completion = true`,
		`quick_exit_threshold_ms = 0`,
		`return window`,
		`nexo_launch(ctx, {}, "nexo")`,
		`nexo_launch(ctx, { "agents" }, "nexo agents")`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Rex block lacks %q:\n%s", want, body)
		}
	}
	// The window is tiled, so nothing the floating layer needed belongs here.
	for _, unwanted := range []string{
		"new_layer",
		"bounds",
		"set_theme",
		"describe_block",
		"rex.on(",
	} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("Rex block still contains %q:\n%s", unwanted, body)
		}
	}
}

func TestReplaceManagedBlockBetweenUsesLuaCommentsAndIsIdempotent(t *testing.T) {
	body := RexBlock("/opt/nexo", "ctrl+space>o", "")
	before := "-- user setting\nlocal keep = true\n"
	got, changed, err := ReplaceManagedBlockBetween(before, body, LuaManagedStart, LuaManagedEnd)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if !changed {
		t.Fatal("first pass reported no change")
	}
	if !strings.Contains(got, "-- >>> nexo >>>\n"+body+"\n-- <<< nexo <<<") {
		t.Fatalf("Lua markers do not wrap the block:\n%s", got)
	}
	if strings.Contains(got, ManagedStart) {
		t.Fatalf("Rex block used a non-Lua marker:\n%s", got)
	}
	again, changed, err := ReplaceManagedBlockBetween(got, body, LuaManagedStart, LuaManagedEnd)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if changed || again != got {
		t.Fatalf("second pass was not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

func TestRexBindingsRoundTripByAction(t *testing.T) {
	body := RexBlock("/opt/nexo", "ctrl+space>o", "ctrl+space>a")
	contents := "-- keep\n-- >>> nexo >>>\n" + body + "\n-- <<< nexo <<<\n"
	tuiKey, agentsKey := bindingsFromContents(contents, "rex")
	if tuiKey != "ctrl+space>o" || agentsKey != "ctrl+space>a" {
		t.Fatalf("bindings = %q, %q; want ctrl+space>o, ctrl+space>a", tuiKey, agentsKey)
	}
}

func TestRexBindingsDecodeAnEscapedKey(t *testing.T) {
	// One trailing backslash is a valid Rex chord, but Lua and Go write it
	// escaped, so reading the block back must decode it again.
	key := `ctrl+space>\`
	if err := config.ValidateBindingKeyForBackend("rex", key); err != nil {
		t.Fatalf("Rex rejects the chosen key %q: %v", key, err)
	}
	contents := "-- keep\n" + LuaManagedStart + "\n" + RexBlock("/opt/nexo", key, "") + "\n" + LuaManagedEnd + "\n"
	if !strings.Contains(contents, `rex.bind("ctrl+space>\\", "nexo_tui")`) {
		t.Fatalf("generated block did not escape the key:\n%s", contents)
	}
	tuiKey, agentsKey := bindingsFromContents(contents, "rex")
	if tuiKey != key || agentsKey != "" {
		t.Fatalf("read-back keys = %q, %q; want %q and no agents key", tuiKey, agentsKey, key)
	}
	if err := validateBindings("rex", tuiKey, agentsKey); err != nil {
		t.Fatalf("read-back key does not validate: %v", err)
	}
}

func TestSetupRexDryRunKeepsAnEscapedBinding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	initPath := rexConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(initPath), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := LuaManagedStart + "\n" + RexBlock(nexoPath(), `ctrl+space>\`, "") + "\n" + LuaManagedEnd + "\n"
	if err := os.WriteFile(initPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	notify := true
	out := &bytes.Buffer{}
	in := Input{Runner: &rexSetupRunner{}, Home: home, Stdout: out, TTY: false}
	err := Run(context.Background(), in, Options{Backend: "rex", ConfigPath: filepath.Join(home, ".config", "nexo", "config.toml"), LocalOnly: true, DryRun: true, Yes: true, Notify: &notify})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "--- "+initPath) {
		t.Fatalf("rerun planned a change to the escaped binding:\n%s", out.String())
	}
}

// rexSetupRunner records every command and lets a test make Rex's config check
// fail with diagnostics, as the real checker does for a bad Lune/Lua file.
type rexSetupRunner struct {
	calls    []string
	checkErr error
	checkOut []byte
}

func (r *rexSetupRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	switch {
	case name == "sh" && len(args) >= 2 && strings.Contains(args[len(args)-1], "command -v nexo"):
		return []byte("/usr/local/bin/nexo\n"), nil
	case name == "sh" && len(args) >= 2 && strings.Contains(args[len(args)-1], "locale charmap"):
		return []byte("UTF-8\n"), nil
	case name == "/usr/local/bin/nexo" && len(args) == 1 && args[0] == "--version":
		return []byte("nexo test\n"), nil
	case name == "rex" && slices.Contains(args, "config") && slices.Contains(args, "check"):
		return r.checkOut, r.checkErr
	}
	return nil, nil
}

func (r *rexSetupRunner) called(substr string) bool {
	for _, call := range r.calls {
		if strings.Contains(call, substr) {
			return true
		}
	}
	return false
}

func TestSetupRexWritesManagedBlockValidatesAndBacksUp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	initPath := rexConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(initPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("-- my Rex config\nlocal keep = true\n")
	if err := os.WriteFile(initPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".config", "nexo", "config.toml")
	notify := true
	commandRunner := &rexSetupRunner{}
	out := &bytes.Buffer{}
	in := Input{Runner: commandRunner, Home: home, Stdout: out, TTY: false}
	options := Options{Backend: "rex", ConfigPath: configPath, LocalOnly: true, Yes: true, Notify: &notify}
	if err := Run(context.Background(), in, options); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(initPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"-- my Rex config\nlocal keep = true\n",
		LuaManagedStart,
		LuaManagedEnd,
		`name = "nexo_tui"`,
		`name = "nexo_agents"`,
		`local nexo_bin = ` + strconv.Quote(nexoPath()),
		`"--backend=rex", "tui"`,
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("Rex init.lua lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "rex.bind(") {
		t.Fatalf("setup bound a key the user did not choose:\n%s", got)
	}
	backup, err := os.ReadFile(initPath + ".nexo.bak")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !bytes.Equal(backup, original) {
		t.Fatalf("backup = %q, want %q", backup, original)
	}
	wantCheck := "rex --autostart=false config check " + initPath
	if !slices.Contains(commandRunner.calls, wantCheck) {
		t.Fatalf("calls = %q, want %q", commandRunner.calls, wantCheck)
	}
	if commandRunner.called("herdr") || commandRunner.called("tmux") {
		t.Fatalf("Rex setup called another backend: %q", commandRunner.calls)
	}
	if !strings.Contains(out.String(), "rex config reload") {
		t.Fatalf("output does not tell the user to reload Rex:\n%s", out.String())
	}

	// A second run plans no change, so it must neither rewrite the file nor
	// validate it again.
	commandRunner.calls = nil
	out.Reset()
	if err := Run(context.Background(), in, options); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(initPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, got) {
		t.Fatalf("rerun rewrote init.lua:\nbefore:\n%s\nafter:\n%s", got, again)
	}
	if commandRunner.called("config check") {
		t.Fatalf("rerun validated an unchanged config: %q", commandRunner.calls)
	}
	if !strings.Contains(out.String(), "local: ok (no changes)") {
		t.Fatalf("rerun output = %q", out.String())
	}
}

func TestSetupRexKeepsOneChosenKeyOnRerun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	initPath := rexConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(initPath), 0o755); err != nil {
		t.Fatal(err)
	}
	partial := RexBlock("/opt/nexo", "ctrl+space>o", "")
	contents := "-- keep\n" + LuaManagedStart + "\n" + partial + "\n" + LuaManagedEnd + "\n"
	if err := os.WriteFile(initPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	notify := true
	in := Input{Runner: &rexSetupRunner{}, Home: home, Stdout: &bytes.Buffer{}, TTY: false}
	if err := Run(context.Background(), in, Options{Backend: "rex", ConfigPath: filepath.Join(home, ".config", "nexo", "config.toml"), LocalOnly: true, Yes: true, Notify: &notify}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(initPath)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(got), "rex.bind("); count != 1 {
		t.Fatalf("rerun wrote %d bindings, want 1:\n%s", count, got)
	}
	if !strings.Contains(string(got), `rex.bind("ctrl+space>o", "nexo_tui")`) {
		t.Fatalf("rerun lost the chosen key:\n%s", got)
	}
}

func TestSetupRexRejectsInvalidBindingBeforeWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	initPath := rexConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(initPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(LuaManagedStart + "\n" + RexBlock("/opt/nexo", "prefix+a", "") + "\n" + LuaManagedEnd + "\n")
	if err := os.WriteFile(initPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	notify := true
	commandRunner := &rexSetupRunner{}
	err := Run(context.Background(), Input{Runner: commandRunner, Home: home, Stdout: &bytes.Buffer{}, TTY: false}, Options{Backend: "rex", ConfigPath: filepath.Join(home, ".config", "nexo", "config.toml"), LocalOnly: true, Yes: true, Notify: &notify})
	if err == nil || !strings.Contains(err.Error(), "prefix+") {
		t.Fatalf("setup error = %v, want a Rex key error", err)
	}
	got, readErr := os.ReadFile(initPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("invalid key rewrote init.lua:\n%s", got)
	}
	if commandRunner.called("config check") {
		t.Fatalf("invalid key reached config check: %q", commandRunner.calls)
	}
	if _, statErr := os.Stat(initPath + ".nexo.bak"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid key created a backup: %v", statErr)
	}
}

func TestSetupRexReportsConfigCheckFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	initPath := rexConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(initPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(initPath, []byte("-- keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	notify := true
	commandRunner := &rexSetupRunner{checkErr: errors.New("exit status 1"), checkOut: []byte("unknown key \"prefix\"\n")}
	err := Run(context.Background(), Input{Runner: commandRunner, Home: home, Stdout: &bytes.Buffer{}, TTY: false}, Options{Backend: "rex", ConfigPath: filepath.Join(home, ".config", "nexo", "config.toml"), LocalOnly: true, Yes: true, Notify: &notify})
	if err == nil || !strings.Contains(err.Error(), "validate Rex config") || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("setup error = %v, want the Rex check diagnostic", err)
	}
	if _, statErr := os.Stat(initPath + ".nexo.bak"); statErr != nil {
		t.Fatalf("failed validation did not leave the backup: %v", statErr)
	}
}

func TestSetupRexCheckReportsTheBackendWithoutCallingHerdr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	initPath := rexConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(initPath), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := LuaManagedStart + "\n" + RexBlock("/opt/nexo", "", "") + "\n" + LuaManagedEnd + "\n"
	if err := os.WriteFile(initPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".config", "nexo", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("paths = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commandRunner := &rexSetupRunner{}
	out := &bytes.Buffer{}
	if err := Run(context.Background(), Input{Runner: commandRunner, Home: home, Stdout: out, TTY: false}, Options{Backend: "rex", ConfigPath: configPath, Check: true}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, want := range []string{"backend=rex", "focus=ok", "init/plugin=ok", "watcher=ok"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("check output lacks %q:\n%s", want, out.String())
		}
	}
	if !commandRunner.called("config check") {
		t.Fatalf("check did not validate init.lua: %q", commandRunner.calls)
	}
	if commandRunner.called("herdr") {
		t.Fatalf("Rex check called Herdr: %q", commandRunner.calls)
	}
}

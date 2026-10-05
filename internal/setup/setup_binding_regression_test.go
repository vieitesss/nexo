package setup

import (
	"context"
	"strings"
	"testing"
)

func TestBindingsRoundTripByActionAndPreserveAgentsOnly(t *testing.T) {
	both := ManagedStart + "\n" + TmuxBlock(false, "C-s", "prefix+a") + "\n" + ManagedEnd + "\n"
	tuiKey, agentsKey := bindingsFromContents(both, "tmux")
	if tuiKey != "C-s" || agentsKey != "prefix+a" {
		t.Fatalf("tmux bindings = %q, %q", tuiKey, agentsKey)
	}
	agentsOnly := ManagedStart + "\n" + TmuxBlock(false, "", "prefix+a") + "\n" + ManagedEnd + "\n"
	tuiKey, agentsKey = bindingsFromContents(agentsOnly, "tmux")
	if tuiKey != "" || agentsKey != "prefix+a" {
		t.Fatalf("agents-only bindings = %q, %q", tuiKey, agentsKey)
	}
	swapped := ManagedStart + "\n" + TmuxBlock(false, "prefix+t", "M-a") + "\n" + ManagedEnd + "\n"
	for _, want := range []string{`bind t display-popup -E -B -w 100% -h 100% "nexo tui"`, `bind -n M-a display-popup -E -B -w 100% -h 100% "nexo tui agents"`} {
		if !strings.Contains(swapped, want) {
			t.Fatalf("tmux block lacks %q:\n%s", want, swapped)
		}
	}
	tuiKey, agentsKey = bindingsFromContents(swapped, "tmux")
	if tuiKey != "prefix+t" || agentsKey != "M-a" {
		t.Fatalf("swapped bindings = %q, %q", tuiKey, agentsKey)
	}
	// Blocks written before prefix+ existed bound the agents key with a bare
	// bind. They must read back as the same prefix-table key.
	legacy := ManagedStart + "\nbind -n C-s display-popup -E \"nexo tui\"\nbind a display-popup -E \"nexo tui agents\"\n" + ManagedEnd + "\n"
	tuiKey, agentsKey = bindingsFromContents(legacy, "tmux")
	if tuiKey != "C-s" || agentsKey != "prefix+a" {
		t.Fatalf("legacy bindings = %q, %q", tuiKey, agentsKey)
	}
	herdr := ManagedStart + "\n" + HerdrBlock("ctrl+s", "prefix+a") + "\n" + ManagedEnd + "\n"
	tuiKey, agentsKey = bindingsFromContents(herdr, "herdr")
	if tuiKey != "ctrl+s" || agentsKey != "prefix+a" {
		t.Fatalf("Herdr bindings = %q, %q", tuiKey, agentsKey)
	}
}

type remoteFileRunner struct{ output []byte }

func (r remoteFileRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return r.output, nil
}

func TestReadinessIgnoresCommentedBackendLines(t *testing.T) {
	contents := "# set -g focus-events on\n# if-shell nexo tmux init\nset -g @plugin 'vieitesss/nexo'\n"
	if activeTmuxConfigLine(contents, "focus-events on") || activeTmuxConfigLine(contents, "nexo tmux init") {
		t.Fatal("commented tmux settings were treated as active")
	}
	if !activeTmuxConfigLine(contents, "@plugin 'vieitesss/nexo'") {
		t.Fatal("active TPM plugin line was not recognized")
	}
}

func TestTmuxReadinessRequiresHooksAndFocusEvents(t *testing.T) {
	ready, err := probeTmuxReadiness(context.Background(), remoteFileRunner{output: []byte("watcher=yes\ntmux_running=yes\ntmux_focus=on\ntmux_hooks=yes\n")}, "", "")
	if err != nil || !ready.TmuxFocusOK || !ready.TmuxHooksOK || !ready.WatcherRunning {
		t.Fatalf("ready probe = %#v, err=%v", ready, err)
	}
	missingHooks, err := probeTmuxReadiness(context.Background(), remoteFileRunner{output: []byte("watcher=yes\ntmux_running=yes\ntmux_focus=on\ntmux_hooks=no\n")}, "", "")
	if err != nil || !missingHooks.TmuxFocusOK || missingHooks.TmuxHooksOK {
		t.Fatalf("missing-hooks probe = %#v, err=%v", missingHooks, err)
	}
}

func TestRemoteShellChangePreservesLocaleRepair(t *testing.T) {
	before := ManagedStart + "\nexport PATH=\"$HOME/.local/bin:$PATH\"\nexport LC_ALL='C.UTF-8'\n" + ManagedEnd + "\n"
	response := "__NEXO_FILE_PRESENT__\n" + before
	change, err := remoteShellChange(context.Background(), remoteFileRunner{output: []byte(response)}, "dev", "", "/home/u/.zshenv", "/bin/zsh", `export PATH="$HOME/.local/bin:$PATH"`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(change.After), "LC_ALL='C.UTF-8'") {
		t.Fatalf("locale repair was removed: %s", change.After)
	}
}

func TestTmuxBindingConflictsMatchTableAndKey(t *testing.T) {
	listKeys := strings.Join([]string{
		`bind-key    -T copy-mode-vi C-s                  send-keys -X search-forward`,
		`bind-key    -T prefix       a                    send-prefix`,
		`bind-key -r -T prefix       Up                   select-pane -U`,
		`bind-key    -T root         M-g                  display-popup "lazygit"`,
		`bind-key    -T root         C-s                  display-popup -E -B -w 100% -h 100% "nexo tui"`,
	}, "\n")
	for keys, want := range map[string]string{
		"C-s":          "",
		"^s,M-a":       "",
		"prefix+a,M-g": "prefix+a,M-g",
		"a,prefix+g":   "",
		"prefix+up":    "prefix+up",
		"m-g":          "m-g",
	} {
		got := strings.Join(tmuxBindingConflicts(listKeys, strings.Split(keys, ",")), ",")
		if got != want {
			t.Errorf("conflicts for %s = %q, want %q", keys, got, want)
		}
	}
}

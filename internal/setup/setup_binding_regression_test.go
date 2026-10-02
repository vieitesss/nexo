package setup

import (
	"context"
	"strings"
	"testing"
)

func TestBindingsRoundTripByActionAndPreserveAgentsOnly(t *testing.T) {
	both := ManagedStart + "\n" + TmuxBlock(false, "C-s", "a") + "\n" + ManagedEnd + "\n"
	tuiKey, agentsKey := bindingsFromContents(both, "tmux")
	if tuiKey != "C-s" || agentsKey != "a" {
		t.Fatalf("tmux bindings = %q, %q", tuiKey, agentsKey)
	}
	agentsOnly := ManagedStart + "\n" + TmuxBlock(false, "", "a") + "\n" + ManagedEnd + "\n"
	tuiKey, agentsKey = bindingsFromContents(agentsOnly, "tmux")
	if tuiKey != "" || agentsKey != "a" {
		t.Fatalf("agents-only bindings = %q, %q", tuiKey, agentsKey)
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

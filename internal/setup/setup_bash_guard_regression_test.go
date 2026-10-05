package setup

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// A stock Debian/Ubuntu ~/.bashrc starts with a guard that returns before the
// rest of the file when the shell is not interactive.
const (
	bashHeader      = "# ~/.bashrc: executed by bash(1) for non-login shells.\n# If not running interactively, don't do anything\n"
	bashGuard       = "case $- in\n    *i*) ;;\n      *) return;;\nesac\n"
	bashGuardPrompt = "[[ $- != *i* ]] && return\n"
	bashGuardPS1    = "[ -z \"$PS1\" ] && return\n"
)

const bashPathBody = `export PATH="$HOME/.local/bin:$PATH"`

func bashBlock(body string) string {
	return ManagedStart + "\n" + body + "\n" + ManagedEnd + "\n"
}

func bashRemoteShellChange(t *testing.T, contents, body string) Change {
	t.Helper()
	response := "__NEXO_FILE_PRESENT__\n" + contents
	change, err := remoteShellChange(context.Background(), remoteFileRunner{output: []byte(response)}, "dev", "", "/home/u/.bashrc", "/bin/bash", body)
	if err != nil {
		t.Fatal(err)
	}
	return change
}

func TestRemoteShellChangeMovesBashBlockBeforeInteractiveGuard(t *testing.T) {
	before := bashHeader + bashGuard + bashBlock(bashPathBody) + "# user line\n"
	change := bashRemoteShellChange(t, before, bashPathBody)
	if !change.Changed() {
		t.Fatal("block behind the interactive guard was left in place")
	}
	// The block must move in front of the guard; everything else keeps its
	// order and bytes.
	want := bashHeader + bashBlock(bashPathBody) + bashGuard + "# user line\n"
	if string(change.After) != want {
		t.Fatalf("after = %q, want %q", change.After, want)
	}
	if !strings.HasPrefix(string(change.After), "# ~/.bashrc") {
		t.Fatalf("user header was dropped: %q", change.After)
	}
}

func TestRemoteShellChangeGuardRepairIsIdempotent(t *testing.T) {
	first := bashRemoteShellChange(t, bashHeader+bashGuard+bashBlock(bashPathBody)+"# user line\n", bashPathBody)
	if !first.Changed() {
		t.Fatal("first run did not repair the block")
	}
	second := bashRemoteShellChange(t, string(first.After), bashPathBody)
	if second.Changed() {
		t.Fatalf("second run changed the repaired file:\n%s", second.After)
	}
	if string(second.After) != string(first.After) {
		t.Fatalf("second run rewrote the file:\n%q", second.After)
	}
}

func TestRemoteShellChangeInsertsBashBlockBeforeInteractiveGuard(t *testing.T) {
	change := bashRemoteShellChange(t, bashHeader+bashGuard, bashPathBody)
	if !change.Changed() {
		t.Fatal("new block was not planned")
	}
	want := bashHeader + bashBlock(bashPathBody) + bashGuard
	if string(change.After) != want {
		t.Fatalf("after = %q, want %q", change.After, want)
	}
}

func TestRemoteShellChangeLeavesBlockInFrontOfGuardAlone(t *testing.T) {
	before := bashHeader + bashBlock(bashPathBody) + bashGuard
	change := bashRemoteShellChange(t, before, bashPathBody)
	if change.Changed() {
		t.Fatalf("block already in front of the guard was rewritten:\n%s", change.After)
	}
}

func TestRemoteShellChangeMovesBashBlockBeforeKnownGuards(t *testing.T) {
	for name, guard := range map[string]string{
		"case":     bashGuard,
		"brackets": bashGuardPrompt,
		"ps1":      bashGuardPS1,
	} {
		before := bashHeader + guard + bashBlock(bashPathBody) + "# trailing user line\n"
		change := bashRemoteShellChange(t, before, bashPathBody)
		want := bashHeader + bashBlock(bashPathBody) + guard + "# trailing user line\n"
		if string(change.After) != want {
			t.Errorf("%s guard: after = %q, want %q", name, change.After, want)
		}
	}
}

func TestRemoteShellChangeMovesBashBlockBeforeEarliestGuard(t *testing.T) {
	before := bashHeader + bashGuardPS1 + bashGuard + bashBlock(bashPathBody) + "# user line\n"
	change := bashRemoteShellChange(t, before, bashPathBody)
	want := bashHeader + bashBlock(bashPathBody) + bashGuardPS1 + bashGuard + "# user line\n"
	if string(change.After) != want {
		t.Fatalf("after = %q, want %q", change.After, want)
	}
}

func TestRemoteShellChangeInsertsBlockAtGuardLineStart(t *testing.T) {
	// A guard wrapped in a one-line if, indented in the user's file: the block
	// goes in front of the whole line, so its markers start at column zero.
	guard := "  if [ -z \"$PS1\" ]; then return; fi\n"
	before := bashHeader + guard + bashBlock(bashPathBody)
	change := bashRemoteShellChange(t, before, bashPathBody)
	want := bashHeader + bashBlock(bashPathBody) + guard
	if string(change.After) != want {
		t.Fatalf("after = %q, want %q", change.After, want)
	}
	second := bashRemoteShellChange(t, string(change.After), bashPathBody)
	if second.Changed() {
		t.Fatalf("re-run after a mid-line guard moved the block again:\n%s", second.After)
	}
}

type bashRemotePlanRunner struct{ files map[string][]byte }

func (r *bashRemotePlanRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "ssh" {
		return nil, nil
	}
	command := strings.Join(args, " ")
	if strings.Contains(command, "__NEXO_FILE_PRESENT__") {
		for path, contents := range r.files {
			if strings.Contains(command, path) {
				return append([]byte("__NEXO_FILE_PRESENT__\n"), contents...), nil
			}
		}
		return []byte("__NEXO_FILE_MISSING__\n"), nil
	}
	return nil, nil
}

func TestPlanRemoteRepairsBashBlockBehindGuardAndRerunsClean(t *testing.T) {
	const bashrc = "/home/u/.bashrc"
	runner := &bashRemotePlanRunner{files: map[string][]byte{bashrc: []byte(bashHeader + bashGuard + bashBlock(bashPathBody) + "# user line\n")}}
	facts := RemoteMachine{Machine: "dev", Home: "/home/u", Shell: "/bin/bash", OS: runtime.GOOS, Arch: runtime.GOARCH, HasNexo: true, HasTmux: true, TmuxRunning: true, WatcherRunning: true, Locale: "ANSI_X3.4-1968", UTF8Locale: "C.UTF-8"}
	options := Options{FixLocale: true}
	plan := planRemoteWithFacts(context.Background(), Input{Runner: runner}, options, "dev", "", "tmux", nil, &facts)
	if plan.Error != nil {
		t.Fatal(plan.Error)
	}
	var shell Change
	for _, change := range plan.Changes {
		if change.Path == bashrc {
			shell = change
		}
	}
	if !shell.Changed() {
		t.Fatalf("plan did not include the .bashrc repair: %#v", plan.Changes)
	}
	// The plan must show the repair: the block appears in front of the guard
	// and disappears from behind it.
	diff := FormatDiff(shell)
	if !strings.Contains(diff, "+# >>> nexo >>>") || !strings.Contains(diff, "-# >>> nexo >>>") {
		t.Fatalf("diff does not show the block moving:\n%s", diff)
	}
	want := bashHeader + bashBlock(bashPathBody+"\nexport LC_ALL='C.UTF-8'") + bashGuard + "# user line\n"
	if string(shell.After) != want {
		t.Fatalf("after = %q, want %q", shell.After, want)
	}
	// Apply the planned bytes and plan again: setup must print only
	// "ok (no changes)" and write nothing.
	for _, change := range plan.Changes {
		runner.files[change.Path] = change.After
	}
	second := planRemoteWithFacts(context.Background(), Input{Runner: runner}, options, "dev", "", "tmux", nil, &facts)
	if second.Error != nil {
		t.Fatal(second.Error)
	}
	if len(second.Changes) != 0 {
		t.Fatalf("second plan is not a no-op: %#v", second.Changes)
	}
}

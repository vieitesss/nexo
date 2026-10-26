package proxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

// fakeHerdrEnv is one fake `herdr` on PATH with the remote answers a test
// configures.
type fakeHerdrEnv struct {
	log        string
	path       string
	status     string
	workspaces string
	focusExit  string
	listExit   string
}

func newFakeHerdr(t *testing.T, status, workspaces, focusExit string) fakeHerdrEnv {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
if [ $# -eq 0 ]; then printf 'ATTACH\n' >> "$HERDR_TEST_LOG"; exit 0; fi
printf '%s\n' "$*" >> "$HERDR_TEST_LOG"
case "$1 $2" in
  "status server")
    printf '%s' "$HERDR_TEST_STATUS"
    exit 0
    ;;
  "workspace list")
    printf '%s' "$HERDR_TEST_WORKSPACES"
    exit "${HERDR_TEST_LIST_EXIT:-0}"
    ;;
  "workspace focus")
    exit "$HERDR_TEST_FOCUS_EXIT"
    ;;
esac
exit 1
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return fakeHerdrEnv{
		log:        logPath,
		path:       dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		status:     status,
		workspaces: workspaces,
		focusExit:  focusExit,
	}
}

func (e fakeHerdrEnv) run(t *testing.T, shell, input string) (string, error) {
	t.Helper()
	command := exec.Command("sh", "-c", shell)
	command.Env = append(os.Environ(),
		"PATH="+e.path,
		"HERDR_TEST_LOG="+e.log,
		"HERDR_TEST_STATUS="+e.status,
		"HERDR_TEST_WORKSPACES="+e.workspaces,
		"HERDR_TEST_FOCUS_EXIT="+e.focusExit,
		"HERDR_TEST_LIST_EXIT="+e.listExit,
	)
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	return string(output), err
}

func (e fakeHerdrEnv) calls(t *testing.T) []string {
	t.Helper()
	contents, err := os.ReadFile(e.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(contents), "\n"), "\n")
}

const stoppedServerJSON = `{"status":"not_running","running":false}`

const runningServerJSON = `{"status":"running","running":true}`

const oneWorkspaceJSON = `{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"alpha","focused":true}]}}`

const noWorkspacesJSON = `{"result":{"type":"workspace_list","workspaces":[]}}`

func TestHerdrAttachShellRefusesToStartAServerAndQuitsOnQ(t *testing.T) {
	env := newFakeHerdr(t, stoppedServerJSON, noWorkspacesJSON, "0")
	output, err := env.run(t, HerdrAttachShell("rpi", ""), "q")
	if err != nil {
		t.Fatalf("attach shell error = %v (%s)", err, output)
	}
	if !strings.Contains(output, "no Herdr server on rpi — press r to retry, q to close") {
		t.Fatalf("attach shell output = %q, want the missing-server guard", output)
	}
	for _, call := range env.calls(t) {
		if call != "status server --json" {
			t.Fatalf("attach shell calls = %v, want only the read-only status probe", env.calls(t))
		}
	}
}

func TestHerdrAttachShellFocusesThePinnedWorkspaceThenAttaches(t *testing.T) {
	env := newFakeHerdr(t, runningServerJSON, oneWorkspaceJSON, "0")
	output, err := env.run(t, HerdrAttachShell("rpi", "w2"), "")
	if err != nil {
		t.Fatalf("attach shell error = %v (%s)", err, output)
	}
	want := []string{"status server --json", "workspace focus w2", "workspace list", "ATTACH"}
	if got := env.calls(t); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("attach shell calls = %v, want %v", got, want)
	}
	for _, call := range env.calls(t) {
		if strings.HasPrefix(call, "workspace create") || strings.HasPrefix(call, "server start") {
			t.Fatalf("attach shell created remote state: %v", env.calls(t))
		}
	}
}

func TestHerdrAttachShellNeverAttachesWithoutAConfirmedWorkspaceList(t *testing.T) {
	// Herdr starts a server on attach and an empty server creates a
	// workspace, so an attach is only safe once a nonempty listing proves
	// the remote already holds one (D-4). A listing that fails, answers with
	// nothing, or answers with something unreadable must wait for retry.
	calls := []struct {
		name       string
		workspaces string
		listExit   string
	}{
		{name: "failed listing", workspaces: oneWorkspaceJSON, listExit: "1"},
		{name: "empty answer", workspaces: "", listExit: "0"},
		{name: "unreadable answer", workspaces: `{"error":{"code":"server_not_running"}}`, listExit: "0"},
	}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			env := newFakeHerdr(t, runningServerJSON, call.workspaces, "0")
			env.listExit = call.listExit
			output, err := env.run(t, HerdrAttachShell("rpi", ""), "q")
			if err != nil {
				t.Fatalf("attach shell error = %v (%s)", err, output)
			}
			if !strings.Contains(output, "no workspaces on rpi — press r to retry, q to close") {
				t.Fatalf("attach shell output = %q, want the workspace guard", output)
			}
			for _, made := range env.calls(t) {
				if made == "ATTACH" {
					t.Fatalf("attach shell attached without a confirmed workspace list: %v", env.calls(t))
				}
			}
		})
	}
}

func TestHerdrAttachShellRefusesAnEmptyServerInsteadOfCreatingAWorkspace(t *testing.T) {
	env := newFakeHerdr(t, runningServerJSON, noWorkspacesJSON, "0")
	output, err := env.run(t, HerdrAttachShell("rpi", ""), "q")
	if err != nil {
		t.Fatalf("attach shell error = %v (%s)", err, output)
	}
	if !strings.Contains(output, "no workspaces on rpi — press r to retry, q to close") {
		t.Fatalf("attach shell output = %q, want the empty-server guard", output)
	}
	for _, call := range env.calls(t) {
		if call == "ATTACH" {
			t.Fatalf("attach shell attached to an empty server: %v", env.calls(t))
		}
	}
}

func TestHerdrAttachShellReportsAMissingPinnedWorkspaceAndRetriesUnpinned(t *testing.T) {
	// The first attempt pins w99, which the fake refuses; r then retries
	// unpinned against the server's own active workspace.
	env := newFakeHerdr(t, runningServerJSON, oneWorkspaceJSON, "1")
	output, err := env.run(t, HerdrAttachShell("rpi", "w99"), "r")
	if err != nil {
		t.Fatalf("attach shell error = %v (%s)", err, output)
	}
	if !strings.Contains(output, "no workspace w99 on rpi — press r to retry, q to close") {
		t.Fatalf("attach shell output = %q, want the missing-workspace guard", output)
	}
	calls := env.calls(t)
	want := []string{"status server --json", "workspace focus w99", "status server --json", "workspace list", "ATTACH"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("attach shell calls = %v, want %v", calls, want)
	}
}

func TestHerdrSwitchShellFocusesTheWorkspaceByID(t *testing.T) {
	env := newFakeHerdr(t, runningServerJSON, oneWorkspaceJSON, "0")
	if output, err := env.run(t, HerdrSwitchShell("w3"), ""); err != nil {
		t.Fatalf("switch shell error = %v (%s)", err, output)
	}
	if got := strings.Join(env.calls(t), "|"); got != "status server --json|workspace focus w3" {
		t.Fatalf("switch shell calls = %q, want status then focus", got)
	}
}

func TestHerdrSwitchShellSeparatesAMissingWorkspaceFromAStoppedServer(t *testing.T) {
	missing := newFakeHerdr(t, runningServerJSON, oneWorkspaceJSON, "1")
	if output, err := missing.run(t, HerdrSwitchShell("w3"), ""); err == nil || !IsMissingRemoteTarget(err) {
		t.Fatalf("missing workspace error = %v (%s), want the missing-target sentinel", err, output)
	}
	stopped := newFakeHerdr(t, stoppedServerJSON, noWorkspacesJSON, "0")
	err := func() error {
		_, runErr := stopped.run(t, HerdrSwitchShell("w3"), "")
		return runErr
	}()
	if err == nil || IsMissingRemoteTarget(err) {
		t.Fatalf("stopped server error = %v, want an exit that leaves the pane to the guard", err)
	}
}

func TestTransportArgsSelectTheHerdrShellWithoutARendezvousFile(t *testing.T) {
	args := TransportArgs(config.RemoteConfig{Transport: "ssh"}, RemoteKindTmux, RemoteKindHerdr, "rpi", "w2")
	command := args[len(args)-1]
	for _, want := range []string{"no Herdr server on", "workspace focus \"$target\"", "exec herdr"} {
		if !strings.Contains(command, want) {
			t.Fatalf("herdr transport args = %q, want %q", command, want)
		}
	}
	for _, unwanted := range []string{"nexo-proxy-", "tmux "} {
		if strings.Contains(command, unwanted) {
			t.Fatalf("herdr transport args = %q, want no tmux attachment", command)
		}
	}
	if got := TransportArgs(config.RemoteConfig{Transport: "mosh"}, RemoteKindTmux, RemoteKindHerdr, "rpi", "")[0]; got != "mosh" {
		t.Fatalf("herdr transport program = %q, want mosh to wrap the same shell", got)
	}
}

func TestAttachCommandNamesTheHerdrRemoteKindOnlyForHerdr(t *testing.T) {
	bridged, err := AttachCommand("/usr/bin/nexo", "tmux", "", RemoteKindHerdr, "rpi", "w2", false)
	if err != nil {
		t.Fatalf("AttachCommand() error = %v", err)
	}
	want := "exec '/usr/bin/nexo' --backend tmux remote attach --remote-kind herdr --initial 'w2' -- 'rpi'"
	if bridged != want {
		t.Fatalf("AttachCommand() = %q, want %q", bridged, want)
	}
	tmuxRemote, err := AttachCommand("/usr/bin/nexo", "tmux", "", RemoteKindTmux, "rpi", "", false)
	if err != nil {
		t.Fatalf("AttachCommand() error = %v", err)
	}
	if strings.Contains(tmuxRemote, "--remote-kind") {
		t.Fatalf("AttachCommand() = %q, want no remote kind flag for the tmux default", tmuxRemote)
	}
	if _, err := AttachCommand("/usr/bin/nexo", "tmux", "", "zellij", "rpi", "", false); err == nil {
		t.Fatal("AttachCommand() accepted an unknown remote kind")
	}
}

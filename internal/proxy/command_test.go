package proxy

import (
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func TestAttachCommandUsesUnpinnedMachineTarget(t *testing.T) {
	got, err := AttachCommand("/usr/bin/nexo", "tmux", "", RemoteKindTmux, "devbox", "", false)
	if err != nil {
		t.Fatalf("AttachCommand() error = %v", err)
	}
	want := "exec '/usr/bin/nexo' --backend tmux remote attach -- 'devbox'"
	if got != want {
		t.Fatalf("AttachCommand() = %q, want %q", got, want)
	}
}

func TestAttachCommandUsesInitialTargetOnlyOnFirstAttach(t *testing.T) {
	got, err := AttachCommand("/usr/bin/nexo", "tmux", "", RemoteKindTmux, "devbox", "api", false)
	if err != nil {
		t.Fatalf("AttachCommand() error = %v", err)
	}
	want := "exec '/usr/bin/nexo' --backend tmux remote attach --initial 'api' -- 'devbox'"
	if got != want {
		t.Fatalf("AttachCommand() = %q, want %q", got, want)
	}
}

func TestAttachCommandUsesEnvironmentForHerdrProxy(t *testing.T) {
	got, err := AttachCommand("/usr/bin/nexo", "herdr", "/tmp/nexo config.toml", RemoteKindTmux, "", "", true)
	if err != nil {
		t.Fatalf("AttachCommand() error = %v", err)
	}
	want := "exec '/usr/bin/nexo' --backend herdr --config-file '/tmp/nexo config.toml' remote attach --from-env"
	if got != want {
		t.Fatalf("AttachCommand() = %q, want %q", got, want)
	}
}

func TestRemoteAttachShellUsesSingleKeyPromptAndInitialTarget(t *testing.T) {
	unpinned := RemoteAttachShell("key", "devbox", "")
	for _, want := range []string{"session_last_attached", "stty -icanon -echo min 1 time 0", "stty \"$tty_state\"", "dd bs=1 count=1", "no sessions on devbox", "client_session_id", "state_file"} {
		if !strings.Contains(unpinned, want) {
			t.Fatalf("RemoteAttachShell() = %q, want %q", unpinned, want)
		}
	}
	initial := RemoteAttachShell("key", "devbox", "api.v1:blue")
	if !strings.Contains(initial, "initial='=api_v1_blue'") {
		t.Fatalf("initial attach shell = %q, want canonical exact target", initial)
	}
	if !strings.Contains(initial, "session_last_attached") {
		t.Fatalf("initial attach shell = %q, want fallback unpinned resolver", initial)
	}
}

func TestProxyRendezvousKeyIncludesBackend(t *testing.T) {
	tmuxKey := ProxyRendezvousKey("tmux", "host-machine")
	herdrKey := ProxyRendezvousKey("herdr", "host-machine")
	if tmuxKey == herdrKey {
		t.Fatalf("ProxyRendezvousKey() = %q for both backends", tmuxKey)
	}
}

func TestProxyRendezvousKeyDoesNotCollapseDistinctMachines(t *testing.T) {
	dot := ProxyRendezvousKey("tmux", "dev.box")
	underscore := ProxyRendezvousKey("tmux", "dev_box")
	if dot == underscore {
		t.Fatalf("ProxyRendezvousKey collapsed dev.box and dev_box to %q", dot)
	}
	for _, key := range []string{dot, underscore} {
		for _, r := range key {
			allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
			if !allowed {
				t.Fatalf("rendezvous key %q is not filename-safe", key)
			}
		}
	}
}

// TestBackendRendezvousIsSharedByAttachAndSwitch pins that one backend's
// attachment and explicit switch meet on the same remote rendezvous file.
func TestBackendRendezvousIsSharedByAttachAndSwitch(t *testing.T) {
	for _, backend := range []string{"tmux", "herdr"} {
		key := ProxyRendezvousKey(backend, "reviewbox")
		attach := strings.Join(TransportArgs(config.RemoteConfig{Transport: "ssh"}, backend, RemoteKindTmux, "reviewbox", ""), " ")
		if !strings.Contains(attach, "nexo-proxy-"+key+".tty") {
			t.Fatalf("%s attach = %q, want rendezvous %q", backend, attach, key)
		}
		switchArgs := RemoteSwitchSSHArgs(config.RemoteConfig{}, backend, "reviewbox", "second")
		if command := switchArgs[len(switchArgs)-1]; !strings.Contains(command, "nexo-proxy-"+key+".tty") {
			t.Fatalf("%s switch = %q, want rendezvous %q", backend, command, key)
		}
	}
}

func TestRemoteSwitchClientShellUsesCanonicalTarget(t *testing.T) {
	id := ProxyRendezvousKey("tmux", "host-machine")
	command := RemoteSwitchClientShell(id, "host-machine", "api.v1:blue")
	if !strings.Contains(command, "nexo-proxy-"+id+".tty") || !strings.Contains(command, "nexo-proxy-"+id+".target") || !strings.Contains(command, "-t \"$target\"") || !strings.Contains(command, "client_tty") {
		t.Fatalf("RemoteSwitchClientShell() = %q, want tty file and canonical target", command)
	}
}

func TestAttachCommandRejectsControlAndFormatCharacters(t *testing.T) {
	for _, value := range []string{"/tmp/nexo\u0015", "/tmp/nexo\u200e"} {
		if _, err := AttachCommand(value, "herdr", "", RemoteKindTmux, "", "", true); err == nil {
			t.Fatalf("AttachCommand(%q) accepted a control/format character", value)
		}
	}
}

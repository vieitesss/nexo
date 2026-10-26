package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/vieitesss/nexo/internal/config"
)

// Remote multiplexer kinds an attachment can target. Tmux is the historical
// default: a proxy pane attaches to a remote tmux unless it names another
// kind explicitly.
const (
	RemoteKindTmux  = "tmux"
	RemoteKindHerdr = "herdr"
)

// AttachCommand builds the shell command typed into a proxy pane. A non-empty
// initial target is used only for the first attachment; reconnects resolve an
// unpinned target on the remote machine. remoteKind names the remote
// multiplexer; it is written only when the remote is not tmux.
func AttachCommand(binary, backend, configPath, remoteKind, machine, initial string, fromEnv bool) (string, error) {
	values := []string{binary, configPath, machine, initial}
	for _, value := range values {
		if err := rejectControl(value); err != nil {
			return "", err
		}
	}
	if remoteKind != "" && remoteKind != RemoteKindTmux && remoteKind != RemoteKindHerdr {
		return "", errors.New("unknown remote multiplexer kind " + remoteKind)
	}
	parts := []string{"exec", ShellQuote(binary), "--backend", backend}
	if configPath != "" {
		parts = append(parts, "--config-file", ShellQuote(configPath))
	}
	parts = append(parts, "remote", "attach")
	if remoteKind != "" && remoteKind != RemoteKindTmux {
		parts = append(parts, "--remote-kind", remoteKind)
	}
	if initial != "" {
		parts = append(parts, "--initial", ShellQuote(initial))
	}
	if fromEnv {
		parts = append(parts, "--from-env")
	} else {
		parts = append(parts, "--", ShellQuote(machine))
	}
	return strings.Join(parts, " "), nil
}

// ShellQuote returns a POSIX single-quoted shell word.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// CanonicalContainerName applies tmux's session-name canonicalization for
// punctuation that tmux rewrites when creating a session.
func CanonicalContainerName(name string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(name)
}

// ProxyDisplayName supplies the machine when a machine-only proxy has no
// remote container name to display.
func ProxyDisplayName(name, machine string) string {
	if name == "" {
		return machine
	}
	return name
}

// ProxyRendezvousKey uniquely identifies one attachment backend and machine.
// The backend is part of the key because tmux and Herdr proxies for the same
// machine can coexist on the local machine. The key digests the delimited
// tuple so distinct machines whose names differ only in punctuation never
// collapse onto one rendezvous file, and it stays filename-safe.
func ProxyRendezvousKey(backend, machine string) string {
	host, err := os.Hostname()
	if err != nil {
		host = "local"
	}
	parts := []string{host, backend, machine}
	for index, part := range parts {
		parts[index] = strings.ToLower(strings.TrimSpace(part))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

// rendezvousFile is the sole builder for the remote attachment rendezvous
// path. The shell expands XDG_RUNTIME_DIR on the remote machine.
func rendezvousFile(key, suffix string) string {
	return `"${XDG_RUNTIME_DIR:-/tmp}/nexo-proxy-` + key + `.` + suffix + `"`
}

// RemoteAttachShell resolves a remote tmux attachment target. An initial
// target is attempted once; if it no longer exists, the unpinned resolver is
// used immediately. When the attached session disappears, the shell loops
// instead of treating tmux's successful exit as a deliberate detach.
func RemoteAttachShell(key, machine, initial string) string {
	ttyFile := rendezvousFile(key, "tty")
	stateFile := rendezvousFile(key, "target")
	lines := []string{
		"tty_file=" + ttyFile,
		"state_file=" + stateFile,
	}
	if initial != "" {
		lines = append(lines, "initial="+ShellQuote("="+CanonicalContainerName(initial)))
	} else {
		lines = append(lines, "initial=")
	}
	lines = append(lines,
		"while :; do",
		"  target=",
		"  if [ -n \"$initial\" ]; then",
		"    target=\"$initial\"",
		"    initial=",
		"  fi",
		"  if [ -z \"$target\" ]; then",
		"    target=$(tmux list-sessions -F '#{session_last_attached} #{session_id}' 2>/dev/null | sort -rn | head -n1)",
		"    target=${target#* }",
		"  fi",
		"  if [ -n \"$target\" ] && ! tmux has-session -t \"$target\" 2>/dev/null; then",
		"    target=",
		"  fi",
		"  if [ -z \"$target\" ]; then",
		"    printf '%s\\n' "+ShellQuote("no sessions on "+machine+" — press r to retry, q to close")+" >&2",
		"    tty_state=$(stty -g 2>/dev/null) || tty_state=",
		"    if [ -n \"$tty_state\" ]; then stty -icanon -echo min 1 time 0 2>/dev/null || tty_state=; fi",
		"    key=$(dd bs=1 count=1 2>/dev/null)",
		"    if [ -n \"$tty_state\" ]; then stty \"$tty_state\" 2>/dev/null || :; fi",
		"    case \"$key\" in r|R) ;; q|Q|'') exit 0 ;; esac",
		"    continue",
		"  fi",
		"  tty=$(tty 2>/dev/null) || tty=",
		"  printf '%s\\n' \"$tty\" > \"$tty_file\"",
		"  printf '%s\\n' \"$target\" > \"$state_file\"",
		"  track_client() { current=$(tmux list-clients -F '#{client_tty} #{client_session_id}' 2>/dev/null | awk -v tty=\"$tty\" '$1 == tty {print $2; exit}'); [ -n \"$current\" ] && printf '%s\\n' \"$current\" > \"$state_file\"; }",
		"  ( while kill -0 $$ 2>/dev/null; do track_client; sleep 1; done ) & watcher=$!",
		"  tmux attach-session -t \"$target\"",
		"  kill \"$watcher\" 2>/dev/null || :; wait \"$watcher\" 2>/dev/null || :",
		"  target=$(cat \"$state_file\" 2>/dev/null) || target=",
		"  if tmux has-session -t \"$target\" 2>/dev/null; then exit 0; fi",
		"done",
	)
	return strings.Join(lines, "\n")
}

// herdrServerCase is the one statement of whether the remote Herdr server is
// running. It records the status JSON in $status and defines herdr_compact to
// strip its whitespace, so no shell restates the probe or matches the JSON a
// different way. unmatched supplies the case branches for a server that is not
// running; the probe itself never starts one.
func herdrServerCase(unmatched ...string) []string {
	lines := []string{
		"status=$(herdr status server --json 2>/dev/null) || status=",
		"herdr_compact() { printf '%s' \"$1\" | tr -d '[:space:]'; }",
		"case \"$(herdr_compact \"$status\")\" in",
		"  *'\"running\":true'*) ;;",
	}
	lines = append(lines, unmatched...)
	return append(lines, "esac")
}

// HerdrAttachShell attaches a proxy pane to the default Herdr session on a
// remote machine. Herdr starts a server (and an empty server creates a
// workspace) when a client attaches, so the shell guards both cases and waits
// on the same r/q contract as the tmux resolver. It never edits the remote
// Herdr configuration and never starts anything itself.
func HerdrAttachShell(machine, initial string) string {
	lines := []string{
		"machine=" + ShellQuote(machine),
		"ask_retry() {",
		"  printf '%s\\n' \"$1\" >&2",
		"  tty_state=$(stty -g 2>/dev/null) || tty_state=",
		"  if [ -n \"$tty_state\" ]; then stty -icanon -echo min 1 time 0 2>/dev/null || tty_state=; fi",
		"  answer=$(dd bs=1 count=1 2>/dev/null)",
		"  if [ -n \"$tty_state\" ]; then stty \"$tty_state\" 2>/dev/null || :; fi",
		"  case \"$answer\" in r|R) return 0 ;; *) return 1 ;; esac",
		"}",
	}
	if initial != "" {
		lines = append(lines, "initial="+ShellQuote(initial))
	} else {
		lines = append(lines, "initial=")
	}
	lines = append(lines, "while :; do")
	lines = append(lines, herdrServerCase(
		`  *) ask_retry "no Herdr server on $machine — press r to retry, q to close" || exit 0; continue ;;`,
	)...)
	lines = append(lines,
		"  if [ -n \"$initial\" ]; then",
		"    target=$initial",
		"    initial=",
		"    if ! herdr workspace focus \"$target\" >/dev/null 2>&1; then",
		"      ask_retry \"no workspace $target on $machine — press r to retry, q to close\" || exit 0",
		"      continue",
		"    fi",
		"  fi",
		"  workspaces=$(herdr workspace list 2>/dev/null) || workspaces=",
		"  case \"$(herdr_compact \"$workspaces\")\" in",
		"    *'\"workspaces\":[]'*) ask_retry \"no workspaces on $machine — press r to retry, q to close\" || exit 0; continue ;;",
		"  esac",
		"  exec herdr",
		"done",
	)
	return strings.Join(lines, "\n")
}

const MissingRemoteTargetExitCode = 2

// RemoteSwitchClientShell switches the existing remote proxy tmux client to
// an explicit container identified by its attach-shell tty rendezvous file.
// Exit code 2 means the requested remote target is missing; exit code 1 means
// the proxy client could not be found and callers may restart the pane.
func RemoteSwitchClientShell(key, machine, container string) string {
	ttyFile := rendezvousFile(key, "tty")
	stateFile := rendezvousFile(key, "target")
	target := "=" + CanonicalContainerName(strings.TrimSpace(container))
	lines := []string{
		"target=" + ShellQuote(target),
		"if ! tmux has-session -t \"$target\" 2>/dev/null; then",
		"  printf '%s\\n' " + ShellQuote("no session "+container+" on "+machine) + " >&2",
		"  exit 2",
		"fi",
		"tty=$(cat " + ttyFile + " 2>/dev/null) || exit 1",
		"client=$(tmux list-clients -F '#{client_tty} #{client_name}' 2>/dev/null | awk -v tty=\"$tty\" '$1 == tty {print $2; exit}')",
		"[ -n \"$client\" ] || exit 1",
		"tmux switch-client -c \"$client\" -t \"$target\" && printf '%s\\n' \"$target\" > " + stateFile,
	}
	return strings.Join(lines, "\n")
}

// HerdrSwitchShell focuses one remote workspace by ID. A focused workspace is
// server-wide, so this moves every client attached to that remote server. Exit
// code 2 means the workspace is missing; any other exit leaves the pane to the
// attach guard, which reports a stopped server and keeps its retry loop.
func HerdrSwitchShell(workspaceID string) string {
	lines := herdrServerCase("  *) exit 1 ;;")
	return strings.Join(append(lines,
		"herdr workspace focus "+ShellQuote(workspaceID)+" >/dev/null 2>&1 || exit 2",
	), "\n")
}

// IsMissingRemoteTarget reports the explicit-open sentinel returned by the
// remote client-switch shell.
func IsMissingRemoteTarget(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == MissingRemoteTargetExitCode
}

// SwitchSSHArgs builds the plain SSH invocation that retargets the remote
// client of an existing proxy. It mirrors TransportArgs: the remote kind picks
// the switch shell, and the backend only keys the tmux rendezvous file.
func SwitchSSHArgs(remote config.RemoteConfig, backend, remoteKind, machine, container string) []string {
	command := RemoteSwitchClientShell(ProxyRendezvousKey(backend, machine), machine, container)
	if remoteKind == RemoteKindHerdr {
		command = HerdrSwitchShell(container)
	}
	args := []string{"-o", "BatchMode=yes"}
	if strings.TrimSpace(remote.SSHConfig) != "" {
		args = append(args, "-F", remote.SSHConfig)
	}
	return append(args, machine, "sh -c "+ShellQuote(command))
}

func rejectControl(value string) error {
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errors.New("proxy command values cannot contain control or format characters")
		}
	}
	return nil
}

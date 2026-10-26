// Package rex contains the Rex backend adapter. A Rex session is a nexo
// container and a Rex terminal block is a nexo pane.
package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

// noAutostart keeps every adapter call from starting a Rex server as a side
// effect of detecting, listing, or opening.
const noAutostart = "--autostart=false"

// terminalFlavor is the prefix of block flavors that own a program, a working
// directory, and therefore project usage. Other flavors have nothing to
// inspect.
const terminalFlavor = "com.superlogical.terminal"

// labelLimit is Rex's maximum session label length.
const labelLimit = 128

// appBinary is the macOS application bundle location of the Rex CLI. It is the
// last resort: PATH and REX_BIN_DIR always win.
const appBinary = "Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex"

// Backend is the Rex backend. Capabilities are added independently as each
// feature is implemented.
type Backend struct {
	Runner runner.CommandRunner

	binary   string
	messages io.Writer
	stateDir string
	// now is the ledger clock. A nil clock uses time.Now.
	now func() time.Time

	// agents is the shared agents policy; agentsMu guards it because the TUI
	// configures agents once and then polls from another goroutine.
	agentsMu sync.Mutex
	agents   config.AgentsConfig

	identity    identity
	identified  bool
	identityErr error

	// proxyConfig and proxyConfigPath apply to machine proxies: the attachment
	// transport, SSH settings, and the optional config file the child nexo
	// process inherits.
	proxyConfig     config.RemoteConfig
	proxyConfigPath string
}

// New constructs a Rex backend over the supplied command runner. Informational
// notices such as a focus that no connected app can perform are written to
// messages, which may be nil.
func New(r runner.CommandRunner, messages io.Writer) *Backend {
	return &Backend{Runner: r, binary: locateBinary(osLookup()), messages: messages, now: time.Now, agents: (config.AgentsConfig{}).WithDefaults()}
}

// Error reports a failed Rex operation and preserves Rex's stderr.
type Error struct {
	Operation string
	Stderr    string
	Err       error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("rex %s: %s", e.Operation, strings.TrimSpace(e.Stderr))
	}
	return fmt.Sprintf("rex %s: %v", e.Operation, e.Err)
}

// Unwrap preserves the command runner error for errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// binaryLookup is the process surface used to find the Rex CLI.
type binaryLookup struct {
	LookPath func(string) (string, error)
	Getenv   func(string) string
	HomeDir  func() (string, error)
}

func osLookup() binaryLookup {
	return binaryLookup{LookPath: exec.LookPath, Getenv: os.Getenv, HomeDir: os.UserHomeDir}
}

// FindBinary resolves the Rex CLI the way every adapter call will: PATH first,
// then the server's REX_BIN_DIR, then the macOS application bundle.
func FindBinary() string {
	return locateBinary(osLookup())
}

// locateBinary resolves the Rex executable: PATH first, then the server's
// REX_BIN_DIR, then the macOS application bundle. The fallback name keeps the
// runner's own "not found" diagnostic in front of the user.
func locateBinary(lookup binaryLookup) string {
	if lookup.LookPath != nil {
		if path, err := lookup.LookPath("rex"); err == nil && strings.TrimSpace(path) != "" {
			return path
		}
	}
	if lookup.Getenv != nil {
		if dir := strings.TrimSpace(lookup.Getenv("REX_BIN_DIR")); dir != "" {
			if path := filepath.Join(dir, "rex"); isExecutable(path) {
				return path
			}
		}
	}
	if lookup.HomeDir != nil {
		if home, err := lookup.HomeDir(); err == nil && strings.TrimSpace(home) != "" {
			if path := filepath.Join(home, appBinary); isExecutable(path) {
				return path
			}
		}
	}
	return "rex"
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func (b *Backend) executable() string {
	if b == nil || strings.TrimSpace(b.binary) == "" {
		return "rex"
	}
	return b.binary
}

// exec runs one Rex CLI request. The operation label names the request in the
// diagnostic instead of repeating its JSON payload.
func (b *Backend) exec(ctx context.Context, operation string, args ...string) ([]byte, error) {
	if b == nil || b.Runner == nil {
		return nil, errors.New("rex backend requires a command runner")
	}
	output, err := b.Runner.Run(ctx, b.executable(), append([]string{noAutostart}, args...)...)
	if err == nil {
		return output, nil
	}
	stderr := string(output)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		stderr = string(exitErr.Stderr)
	}
	if stderr == "" {
		stderr = err.Error()
	}
	return nil, &Error{Operation: operation, Stderr: stderr, Err: err}
}

// api calls one server API method. Rex prints the method's JSON result
// directly, without Herdr's result envelope.
func (b *Backend) api(ctx context.Context, method string, input any) ([]byte, error) {
	args := []string{"api", "call", method}
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("encode rex %s input: %w", method, err)
		}
		args = append(args, string(payload))
	}
	return b.exec(ctx, method, args...)
}

// identity is the durable namespace of one Rex server run, plus the caller's
// native location when nexo runs inside a Rex block. Native ids are only
// unique within one server, so persisted state is scoped to the server run.
type identity struct {
	HostID     string `json:"host_id"`
	InstanceID string `json:"instance_id"`
	Endpoint   string `json:"endpoint"`
	SessionID  string `json:"session_id"`
	WindowID   string `json:"window_id"`
	BlockID    string `json:"block_id"`
}

// namespace scopes persisted state to the server files identity and process
// instance, falling back to the endpoint when the server has no host id.
func (i identity) namespace() string {
	host := strings.TrimSpace(i.HostID)
	if host == "" {
		host = strings.TrimSpace(i.Endpoint)
	}
	instance := strings.TrimSpace(i.InstanceID)
	switch {
	case host == "" && instance == "":
		return "default"
	case instance == "":
		return host
	case host == "":
		return instance
	default:
		return host + "-" + instance
	}
}

// server resolves and caches this invocation's server identity. Rex only
// reports location fields for ids that resolve on the live server, so a stale
// REX_SESSION cannot claim a session it does not name.
func (b *Backend) server(ctx context.Context) (identity, error) {
	if b == nil || b.Runner == nil {
		return identity{}, errors.New("rex backend requires a command runner")
	}
	if b.identified {
		return b.identity, b.identityErr
	}
	b.identified = true
	output, err := b.exec(ctx, "whoami", "whoami", "--json")
	if err != nil {
		b.identityErr = err
		return identity{}, err
	}
	var resolved identity
	if err := json.Unmarshal(output, &resolved); err != nil {
		b.identityErr = fmt.Errorf("decode rex whoami: %w", err)
		return identity{}, b.identityErr
	}
	b.identity = resolved
	return resolved, nil
}

// CurrentContainerID reports the session that owns the calling block.
func (b *Backend) CurrentContainerID(ctx context.Context) (string, error) {
	resolved, err := b.server(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resolved.SessionID), nil
}

// notice writes one informational message when a focus cannot move the user's
// view. Warnings go to the caller's diagnostic stream, never to stdout.
func (b *Backend) notice(message string) {
	if b == nil || b.messages == nil || message == "" {
		return
	}
	_, _ = fmt.Fprintln(b.messages, message)
}

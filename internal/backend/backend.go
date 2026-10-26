// Package backend selects the one multiplexer driven by an invocation.
package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/backend/herdr"
	"github.com/vieitesss/nexo/internal/backend/rex"
	"github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

// Kind identifies a backend selection.
type Kind string

const (
	Auto  Kind = "auto"
	Tmux  Kind = "tmux"
	Herdr Kind = "herdr"
	Rex   Kind = "rex"
	None  Kind = "none"
)

// ParseKind validates a user-provided backend name.
func ParseKind(value string) (Kind, error) {
	kind := Kind(strings.ToLower(strings.TrimSpace(value)))
	switch kind {
	case Auto, Tmux, Herdr, Rex:
		return kind, nil
	default:
		return "", fmt.Errorf("invalid backend %q (want auto, tmux, herdr, or rex)", value)
	}
}

// Environment reads process environment variables.
type Environment interface {
	LookupEnv(string) (string, bool)
}

// OSEnvironment reads the current process environment.
type OSEnvironment struct{}

// LookupEnv implements Environment.
func (OSEnvironment) LookupEnv(key string) (string, bool) { return os.LookupEnv(key) }

// Selection is the result of backend selection and its initialized adapter.
type Selection struct {
	Kind         Kind     `json:"backend"`
	Source       string   `json:"source"`
	Reason       string   `json:"reason"`
	Capabilities []string `json:"capabilities"`
	Adapter      any      `json:"-"`
}

// Require returns an error when a command needs a live multiplexer but none
// was detected.
func (s Selection) Require() error {
	if s.Kind == None {
		return errors.New("no live multiplexer detected; run nexo inside tmux, Herdr, or Rex, or pass --backend")
	}
	return nil
}

// ResolveInput controls backend selection. A non-nil Flag always wins,
// including the explicit value "auto".
type ResolveInput struct {
	Flag   *string
	Config config.Config
	Env    Environment
	TTY    string
	Runner runner.CommandRunner
	// Messages receives informational notices from the selected adapter, such
	// as a focus no app can perform. A nil writer discards them.
	Messages io.Writer
}

// Resolve applies flag > config > auto-detection and initializes the selected backend.
func Resolve(ctx context.Context, input ResolveInput) (Selection, error) {
	if input.Env == nil {
		input.Env = OSEnvironment{}
	}

	if input.Flag != nil {
		kind, err := ParseKind(*input.Flag)
		if err != nil {
			return Selection{}, err
		}
		if kind != Auto {
			return newSelection(kind, "flag", fmt.Sprintf("selected by --backend=%s", kind), input), nil
		}
		selected, reason, err := detect(ctx, input.Env, input.TTY, input.Runner)
		if err != nil {
			return Selection{}, err
		}
		return newSelection(selected, "auto-detect", reason, input), nil
	}

	if input.Config.Backend != "" {
		kind, err := ParseKind(input.Config.Backend)
		if err != nil {
			return Selection{}, fmt.Errorf("config backend: %w", err)
		}
		if kind != Auto {
			return newSelection(kind, "config", fmt.Sprintf("selected by config backend=%s", kind), input), nil
		}
	}

	selected, reason, err := detect(ctx, input.Env, input.TTY, input.Runner)
	if err != nil {
		return Selection{}, err
	}
	return newSelection(selected, "auto-detect", reason, input), nil
}

func detect(ctx context.Context, env Environment, tty string, commandRunner runner.CommandRunner) (Kind, string, error) {
	herdrMarker, herdrReason := herdrEnvironment(env)
	tmuxMarker, tmuxReason := tmuxEnvironment(env)
	rexMarker, rexReason := rexEnvironment(env)

	// A lone tmux or Herdr marker is uncontradicted, so it keeps the fast path
	// and no process probe runs.
	if !rexMarker {
		switch {
		case !herdrMarker && !tmuxMarker:
			return None, "no supported multiplexer detected", nil
		case !herdrMarker:
			return Tmux, tmuxReason, nil
		case !tmuxMarker:
			return Herdr, herdrReason, nil
		default:
			return innerMultiplexer(ctx, tty, commandRunner, true, true, herdrReason, tmuxReason)
		}
	}

	rexLive, rexErr := rexSessionLive(ctx, env, commandRunner)
	if !rexLive {
		// A stale or unreadable marker is not a claim to Rex, so the tmux and
		// Herdr precedence stands.
		kind, reason, err := innerMultiplexer(ctx, tty, commandRunner, herdrMarker, tmuxMarker, herdrReason, tmuxReason)
		if err != nil || kind != None {
			return kind, reason, err
		}
		if rexErr != nil {
			return None, fmt.Sprintf("REX_SESSION is set but could not be verified against the Rex server: %v", rexErr), nil
		}
		return None, "REX_SESSION is stale: the Rex server does not own that session", nil
	}

	// The marker is live, so the multiplexer that owns the invocation's
	// controlling TTY is the inner one. A marker can be inherited from an
	// outer terminal, which is exactly why ownership is compared at all.
	control := controlTTY(ctx, tty, commandRunner)
	if !tmuxMarker && !herdrMarker {
		return rexByTTY(ctx, env, control, rexReason, commandRunner)
	}
	if control == "" {
		// Cron jobs and pipes own no terminal: keep today's precedence and say
		// why nothing could be compared.
		kind, reason, err := innerMultiplexer(ctx, control, commandRunner, herdrMarker, tmuxMarker, herdrReason, tmuxReason)
		if err != nil {
			return "", "", err
		}
		return kind, reason + " inside a Rex block (no controlling TTY to compare ownership)", nil
	}
	owns, err := rexOwnsTTY(ctx, env, control, commandRunner)
	if err != nil {
		return None, fmt.Sprintf("cannot tell whether Rex owns the controlling TTY: %v; pass --backend rex or --backend tmux", err), nil
	}
	if owns {
		return Rex, fmt.Sprintf("%s; the block owns the controlling TTY, so the %s marker is inherited", rexReason, outerMarkers(herdrMarker, tmuxMarker)), nil
	}
	if tmuxMarker {
		owned, err := tmuxOwnsTTY(ctx, control, commandRunner)
		if err != nil {
			return None, fmt.Sprintf("cannot tell whether tmux owns the controlling TTY: %v; pass --backend tmux or --backend rex", err), nil
		}
		if owned {
			return Tmux, tmuxReason + " inside a Rex block (the tmux pane owns the controlling TTY)", nil
		}
	}
	if herdrMarker {
		return Herdr, herdrReason + " inside a Rex block (the Rex block does not own the controlling TTY)", nil
	}
	return None, "TMUX and REX_SESSION disagree and neither owns the controlling TTY; pass --backend tmux or --backend rex", nil
}

// rexByTTY confirms that a lone Rex marker really is the inner multiplexer. A
// live REX_SESSION can be inherited outside Rex, where whoami echoes the id
// without Rex owning the caller's terminal, so the marker alone is not proof.
func rexByTTY(ctx context.Context, env Environment, tty, rexReason string, commandRunner runner.CommandRunner) (Kind, string, error) {
	if tty == "" {
		return Rex, rexReason + "; no controlling TTY to compare, so the marker stands", nil
	}
	owns, err := rexOwnsTTY(ctx, env, tty, commandRunner)
	switch {
	case err != nil:
		return None, fmt.Sprintf("REX_SESSION is live but its block's terminal cannot be compared with the controlling TTY: %v; pass --backend rex to force Rex", err), nil
	case owns:
		return Rex, rexReason + "; the block owns the controlling TTY", nil
	default:
		return None, "REX_SESSION is live but no Rex block process owns the controlling TTY: the id is inherited from outside Rex; pass --backend rex to force Rex", nil
	}
}

// outerMarkers names the non-Rex markers that lost the ownership comparison.
func outerMarkers(herdrMarker, tmuxMarker bool) string {
	switch {
	case herdrMarker && tmuxMarker:
		return "Herdr and tmux"
	case herdrMarker:
		return "Herdr"
	default:
		return "tmux"
	}
}

// innerMultiplexer resolves the tmux/Herdr pair, including when both markers
// are inherited from an outer multiplexer.
func innerMultiplexer(ctx context.Context, tty string, commandRunner runner.CommandRunner, herdrMarker, tmuxMarker bool, herdrReason, tmuxReason string) (Kind, string, error) {
	switch {
	case herdrMarker && tmuxMarker:
		owns, err := tmuxOwnsTTY(ctx, tty, commandRunner)
		if err != nil {
			return "", "", err
		}
		if owns {
			return Tmux, "detected tmux as the inner multiplexer (controlling TTY)", nil
		}
		return Herdr, "detected Herdr as the inner multiplexer (controlling TTY)", nil
	case herdrMarker:
		return Herdr, herdrReason, nil
	case tmuxMarker:
		return Tmux, tmuxReason, nil
	default:
		return None, "no supported multiplexer detected", nil
	}
}

func herdrEnvironment(env Environment) (bool, string) {
	if value, ok := env.LookupEnv("HERDR_ENV"); ok && value == "1" {
		return true, "detected Herdr via HERDR_ENV=1"
	}
	if value, ok := env.LookupEnv("HERDR_SOCKET_PATH"); ok && value != "" {
		return true, "detected Herdr via HERDR_SOCKET_PATH"
	}
	if value, ok := env.LookupEnv("HERDR_PANE_ID"); ok && value != "" {
		return true, "detected Herdr via HERDR_PANE_ID"
	}
	return false, ""
}

// rexEnvironment reports whether the invocation carries a Rex block context.
// The marker alone is not evidence: it can be stale, inherited, or forged, so
// detection verifies it against the live server before trusting it.
func rexEnvironment(env Environment) (bool, string) {
	if value, ok := env.LookupEnv("REX_SESSION"); ok && strings.TrimSpace(value) != "" {
		return true, "detected Rex via REX_SESSION (confirmed by the server)"
	}
	return false, ""
}

// rexSessionLive asks the live server whether it owns the marker's session.
// Rex reports caller location only for ids that resolve, so a stale marker can
// never name a session it does not own.
func rexSessionLive(ctx context.Context, env Environment, commandRunner runner.CommandRunner) (bool, error) {
	session, _ := env.LookupEnv("REX_SESSION")
	session = strings.TrimSpace(session)
	if session == "" {
		return false, nil
	}
	if commandRunner == nil {
		return false, errors.New("no command runner")
	}
	output, err := commandRunner.Run(ctx, rex.FindBinary(), "--autostart=false", "whoami", "--json")
	if err != nil {
		return false, fmt.Errorf("rex whoami: %w", err)
	}
	var whoami struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(output, &whoami); err != nil {
		return false, fmt.Errorf("decode rex whoami: %w", err)
	}
	return strings.TrimSpace(whoami.SessionID) == session, nil
}

// rexOwnsTTY reports whether the block named by the Rex markers owns tty. Rex
// exposes no terminal path, so the block's own processes are resolved through
// ps. Inconclusive evidence (no block id, no running process, an unreadable
// ps) is an error, never permission to trust the environment marker.
func rexOwnsTTY(ctx context.Context, env Environment, tty string, commandRunner runner.CommandRunner) (bool, error) {
	session, block := rexBlockContext(env)
	if session == "" || block == "" {
		return false, errors.New("REX_BLOCK is not set")
	}
	want, ok := normalizeTTY(tty)
	if !ok {
		return false, fmt.Errorf("controlling TTY %q is not a terminal path", tty)
	}
	pids, err := rex.BlockProcessPIDs(ctx, commandRunner, session, block)
	if err != nil {
		return false, err
	}
	inspected := false
	for _, pid := range pids {
		got, ok := processTTY(ctx, commandRunner, pid)
		if !ok {
			continue
		}
		inspected = true
		if got == want {
			return true, nil
		}
	}
	if !inspected {
		return false, errors.New("no block process has a readable controlling terminal")
	}
	return false, nil
}

// rexBlockContext returns the native location the Rex environment names.
func rexBlockContext(env Environment) (session, block string) {
	session, _ = env.LookupEnv("REX_SESSION")
	block, _ = env.LookupEnv("REX_BLOCK")
	return strings.TrimSpace(session), strings.TrimSpace(block)
}

func tmuxEnvironment(env Environment) (bool, string) {
	if value, ok := env.LookupEnv("TMUX"); ok && value != "" {
		return true, "detected tmux via TMUX"
	}
	if value, ok := env.LookupEnv("TMUX_PANE"); ok && value != "" {
		return true, "detected tmux via TMUX_PANE"
	}
	return false, ""
}

func tmuxOwnsTTY(ctx context.Context, tty string, commandRunner runner.CommandRunner) (bool, error) {
	if tty == "" {
		return false, nil
	}
	if commandRunner == nil {
		return false, errors.New("cannot determine inner multiplexer: no command runner")
	}

	commands := [][]string{
		{"list-clients", "-F", "#{client_tty}"},
		{"list-panes", "-a", "-F", "#{pane_tty}"},
	}
	var failures []error
	for _, args := range commands {
		output, err := commandRunner.Run(ctx, "tmux", tmux.UTF8Args(args...)...)
		if err != nil {
			failures = append(failures, fmt.Errorf("tmux %s: %w", args[0], err))
			continue
		}
		for _, line := range strings.Split(string(output), "\n") {
			if filepath.Clean(strings.TrimSpace(line)) == filepath.Clean(tty) {
				return true, nil
			}
		}
	}
	if len(failures) == len(commands) {
		return false, fmt.Errorf("cannot determine inner multiplexer: %w", errors.Join(failures...))
	}
	return false, nil
}

func newSelection(kind Kind, source, reason string, input ResolveInput) Selection {
	selection := Selection{
		Kind:   kind,
		Source: source,
		Reason: reason,
	}
	return selection.withCapabilities(input)
}

// withCapabilities attaches the selected backend adapter and reports its
// capabilities. Resolution owns this step so every invocation receives one
// fully initialized selection rather than relying on the caller to complete it.
func (s Selection) withCapabilities(input ResolveInput) Selection {
	environment := input.Env
	if environment == nil {
		environment = OSEnvironment{}
	}
	s.Capabilities = []string{}
	s.Adapter = nil
	switch s.Kind {
	case Tmux:
		inside, _ := tmuxEnvironment(environment)
		s.Adapter = tmux.New(input.Runner, inside)
	case Herdr:
		s.Adapter = herdr.New(input.Runner)
	case Rex:
		s.Adapter = rex.New(input.Runner, input.Messages)
	}
	s.Capabilities = capabilities.Names(s.Adapter)
	if s.Capabilities == nil {
		s.Capabilities = []string{}
	}
	return s
}

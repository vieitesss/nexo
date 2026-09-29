package tmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
	"github.com/vieitesss/nexo/internal/statefile"
)

type pane struct {
	id      string
	target  string
	pid     string
	command string
	path    string
	harness string
	branch  string
}

type agentState struct {
	LastIndicator time.Time  `json:"last_indicator"`
	StoppedSince  *time.Time `json:"stopped_since,omitempty"`
	State         string     `json:"state"`
	Seen          bool       `json:"seen"`
}

type pendingNotification struct {
	ID      uint64 `json:"id"`
	PaneID  string `json:"pane_id"`
	Harness string `json:"harness"`
	Target  string `json:"target"`
}

type persistedStates struct {
	Agents             map[string]agentState `json:"agents"`
	Pending            []pendingNotification `json:"pending_notifications,omitempty"`
	NextNotificationID uint64                `json:"next_notification_id,omitempty"`
	LastNotification   time.Time             `json:"last_notification,omitempty"`
}

var errWatcherAlreadyRunning = errors.New("tmux agent watcher already running")

// Source discovers agent panes in a tmux server and polls their visible
// screens for harness working indicators.
type Source struct {
	runner   runner.CommandRunner
	config   config.AgentsConfig
	now      func() time.Time
	stateDir string
	server   string
	notifier capabilities.Notifier
	mu       sync.Mutex
}

// NewAgentSource creates a tmux AgentSource with the default configuration.
func NewAgentSource(r runner.CommandRunner) *Source {
	return &Source{
		runner: r,
		config: (config.AgentsConfig{}).WithDefaults(),
		now:    time.Now,
	}
}

// ConfigureAgents applies the shared agents configuration to this source.
func (s *Source) ConfigureAgents(cfg config.AgentsConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = cfg.WithDefaults()
}

// SetClock replaces the clock used for state transitions. It is intended for
// deterministic tests and is harmless for production callers.
func (s *Source) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now == nil {
		s.now = time.Now
		return
	}
	s.now = now
}

// SetStateDir selects where this source stores per-server state and locks.
func (s *Source) SetStateDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateDir = dir
}

// SetNotifier attaches the optional backend notifier used by the watcher.
func (s *Source) SetNotifier(notifier capabilities.Notifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifier = notifier
}

// ListAgents implements capabilities.AgentSource.
func (s *Source) ListAgents(ctx context.Context) ([]capabilities.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listAgents(ctx)
}

func (s *Source) listAgents(ctx context.Context) ([]capabilities.Agent, error) {
	if s.runner == nil {
		return nil, errors.New("tmux agents requires a command runner")
	}
	cfg := s.config.WithDefaults()
	panes, err := s.agentPanes(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if s.server == "" {
		s.server = s.serverName(ctx)
	}
	type observedPane struct {
		pane
		working bool
	}
	observed := make([]observedPane, 0, len(panes))
	for _, p := range panes {
		screen, err := s.runner.Run(ctx, "tmux", "capture-pane", "-p", "-J", "-t", p.id)
		if err != nil {
			// A pane can disappear between list-panes and capture-pane. It is
			// not an error for the navigator to omit that pane.
			continue
		}
		observed = append(observed, observedPane{
			pane:    p,
			working: MatchesWorkingIndicator(p.harness, string(screen), cfg.WorkingPattern),
		})
	}

	now := s.now()
	present := make(map[string]struct{}, len(panes))
	for _, current := range panes {
		present[current.id] = struct{}{}
	}
	working := make(map[string]struct{}, len(observed))
	for _, current := range observed {
		if current.working {
			working[current.id] = struct{}{}
		}
	}
	var agents []capabilities.Agent
	err = s.updatePersisted(func(saved *persistedStates) error {
		if !cfg.NotificationsEnabled() {
			saved.Pending = nil
		}
		// A pending transition is obsolete once its pane is working again or
		// disappears from tmux. Keep entries for panes whose screen capture
		// failed; the pane may still exist and can be delivered on the next poll.
		filtered := saved.Pending[:0]
		for _, pending := range saved.Pending {
			if _, ok := present[pending.PaneID]; !ok {
				continue
			}
			if _, ok := working[pending.PaneID]; ok {
				continue
			}
			filtered = append(filtered, pending)
		}
		saved.Pending = filtered

		agents = make([]capabilities.Agent, 0, len(observed))
		for _, current := range observed {
			state := saved.Agents[current.id]
			if state.LastIndicator.IsZero() {
				state.LastIndicator = now
				state.State = "working"
				state.Seen = false
			}
			if current.working {
				state.LastIndicator = now
				state.State = "working"
				state.StoppedSince = nil
				state.Seen = false
			} else if now.Sub(state.LastIndicator) >= time.Duration(cfg.IdleSeconds)*time.Second {
				if state.State != "stopped" && cfg.NotificationsEnabled() {
					saved.NextNotificationID++
					saved.Pending = append(saved.Pending, pendingNotification{
						ID:      saved.NextNotificationID,
						PaneID:  current.id,
						Harness: current.harness,
						Target:  current.target,
					})
				}
				state.State = "stopped"
				if state.StoppedSince == nil {
					stoppedAt := state.LastIndicator.Add(time.Duration(cfg.IdleSeconds) * time.Second)
					state.StoppedSince = &stoppedAt
					state.Seen = false
				}
			}
			saved.Agents[current.id] = state
			agents = append(agents, capabilities.Agent{
				PaneID:       current.id,
				Target:       current.target,
				Harness:      current.harness,
				State:        state.State,
				Seen:         state.Seen,
				StoppedSince: state.StoppedSince,
				Branch:       current.branch,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return agents, nil
}

func (s *Source) agentPanes(ctx context.Context, cfg config.AgentsConfig) ([]pane, error) {
	format := "#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{session_name}\t#{window_index}\t#{pane_index}\t#{pane_current_path}"
	output, err := s.runner.Run(ctx, "tmux", "list-panes", "-a", "-F", format)
	if err != nil {
		return nil, fmt.Errorf("list tmux panes: %w", err)
	}
	allow := make(map[string]struct{}, len(cfg.Processes))
	for _, process := range cfg.Processes {
		allow[strings.ToLower(filepath.Base(strings.TrimSpace(process)))] = struct{}{}
	}
	var result []pane
	for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		current := strings.TrimSpace(fields[2])
		args := current
		if !isHarness(current, allow) {
			if fields[1] == "" {
				continue
			}
			ps, psErr := s.runner.Run(ctx, "ps", "-o", "args=", "-p", fields[1])
			if psErr != nil {
				continue
			}
			args = string(ps)
		}
		harness := findHarness(args, allow)
		if harness == "" {
			continue
		}
		p := pane{
			id:      strings.TrimSpace(fields[0]),
			pid:     strings.TrimSpace(fields[1]),
			command: current,
			path:    strings.TrimSpace(fields[6]),
			target:  fmt.Sprintf("%s:%s.%s", fields[3], fields[4], fields[5]),
			harness: harness,
		}
		p.branch = s.branch(ctx, p.path)
		result = append(result, p)
	}
	return result, nil
}

func isHarness(command string, allow map[string]struct{}) bool {
	_, ok := allow[strings.ToLower(filepath.Base(strings.TrimSpace(command)))]
	return ok
}

func findHarness(args string, allow map[string]struct{}) string {
	for _, field := range strings.Fields(args) {
		field = strings.Trim(field, "\"'(),[]{}")
		base := strings.ToLower(filepath.Base(field))
		if _, ok := allow[base]; ok {
			return base
		}
	}
	return ""
}

func (s *Source) branch(ctx context.Context, path string) string {
	if path == "" {
		return ""
	}
	output, err := s.runner.Run(ctx, "git", "-C", path, "branch", "--show-current")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func (s *Source) serverName(ctx context.Context) string {
	output, err := s.runner.Run(ctx, "tmux", "display-message", "-p", "#{socket_path}")
	if err != nil {
		return "default"
	}
	name := strings.TrimSpace(string(output))
	if name == "" {
		return "default"
	}
	return name
}

func (s *Source) readPersistedStates() (persistedStates, error) {
	contents, err := os.ReadFile(s.statePath("json"))
	if errors.Is(err, os.ErrNotExist) {
		return persistedStates{Agents: make(map[string]agentState)}, nil
	}
	if err != nil {
		return persistedStates{}, err
	}
	var saved persistedStates
	if err := json.Unmarshal(contents, &saved); err != nil {
		return persistedStates{}, err
	}
	if saved.Agents == nil {
		saved.Agents = make(map[string]agentState)
	}
	return saved, nil
}

func (s *Source) updatePersisted(update func(*persistedStates) error) error {
	if s.server == "" {
		return errors.New("tmux agent state has no server")
	}
	stateDir := s.stateDir
	if stateDir == "" {
		stateDir = statefile.DefaultDir()
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create agent state directory: %w", err)
	}
	release, err := acquireFileLock(s.statePath("state.lock"), false)
	if err != nil {
		return fmt.Errorf("lock agent state: %w", err)
	}
	defer release()
	saved, err := s.readPersistedStates()
	if err != nil {
		return fmt.Errorf("read agent state: %w", err)
	}
	if err := update(&saved); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent state: %w", err)
	}
	if err := statefile.WriteAtomic(s.statePath("json"), contents); err != nil {
		return fmt.Errorf("write agent state: %w", err)
	}
	return nil
}

func (s *Source) updateStates(update func(map[string]agentState) error) error {
	return s.updatePersisted(func(saved *persistedStates) error {
		return update(saved.Agents)
	})
}

func (s *Source) statePath(suffix string) string {
	return statefile.Path(s.stateDir, s.server, suffix)
}

// deliverPending sends one trailing, coalesced notification for all pending
// transitions once the durable rate-limit window has elapsed. It is called
// only by Watch; listing agents and focus hooks never send notifications.
func (s *Source) deliverPending(ctx context.Context) error {
	s.mu.Lock()
	notifier := s.notifier
	cfg := s.config.WithDefaults()
	now := s.now()
	server := s.server
	stateDir := s.stateDir
	s.mu.Unlock()
	if notifier == nil || !cfg.NotificationsEnabled() || server == "" {
		return nil
	}
	if stateDir == "" {
		stateDir = statefile.DefaultDir()
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create agent state directory: %w", err)
	}
	release, err := acquireFileLock(statefile.Path(stateDir, server, "state.lock"), false)
	if err != nil {
		return fmt.Errorf("lock agent state: %w", err)
	}
	saved, readErr := s.readPersistedStates()
	release()
	if readErr != nil {
		return fmt.Errorf("read agent state: %w", readErr)
	}
	if len(saved.Pending) == 0 || (!saved.LastNotification.IsZero() && now.Sub(saved.LastNotification) < notificationInterval) {
		return nil
	}
	message := formatPendingNotification(saved.Pending)
	ids := make(map[uint64]struct{}, len(saved.Pending))
	for _, pending := range saved.Pending {
		ids[pending.ID] = struct{}{}
	}
	// A failed notification remains durable and will be retried on the next
	// poll. State is acknowledged only after the runner reports success.
	if err := notifier.Notify(ctx, message); err != nil {
		return nil
	}
	return s.updatePersisted(func(current *persistedStates) error {
		remaining := current.Pending[:0]
		for _, pending := range current.Pending {
			if _, sent := ids[pending.ID]; !sent {
				remaining = append(remaining, pending)
			}
		}
		current.Pending = remaining
		if len(ids) > 0 {
			current.LastNotification = now
		}
		return nil
	})
}

func formatPendingNotification(pending []pendingNotification) string {
	if len(pending) == 1 {
		return fmt.Sprintf("%s · %s stopped", pending[0].Harness, pending[0].Target)
	}
	return fmt.Sprintf("%s · %s and %d more agents stopped", pending[0].Harness, pending[0].Target, len(pending)-1)
}

// MarkSeen marks a stopped pane seen after focus enters that exact pane.
func (s *Source) MarkSeen(ctx context.Context, paneID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server == "" {
		s.server = s.serverName(ctx)
	}
	return s.updateStates(func(states map[string]agentState) error {
		state, ok := states[paneID]
		if !ok {
			return fmt.Errorf("unknown agent pane %q", paneID)
		}
		if state.State == "stopped" {
			state.Seen = true
			states[paneID] = state
		}
		return nil
	})
}

// Watch polls until ctx is canceled. A kernel-held file lock makes concurrent
// watchers for one server idempotent and releases automatically on crashes.
func (s *Source) Watch(ctx context.Context) error {
	s.mu.Lock()
	if s.server == "" {
		s.server = s.serverName(ctx)
	}
	s.mu.Unlock()
	lockPath := s.statePath("lock")
	watchersMu.Lock()
	if _, ok := watchers[lockPath]; ok {
		watchersMu.Unlock()
		return nil
	}
	watchersMu.Unlock()
	release, err := acquireWatcherLock(lockPath)
	if errors.Is(err, errWatcherAlreadyRunning) {
		return nil
	}
	if err != nil {
		return err
	}
	watchersMu.Lock()
	watchers[lockPath] = struct{}{}
	watchersMu.Unlock()
	defer func() {
		watchersMu.Lock()
		delete(watchers, lockPath)
		watchersMu.Unlock()
		release()
	}()
	if err := s.installHooks(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	interval := s.config.WithDefaults().PollInterval
	s.mu.Unlock()
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	if _, err := s.ListAgents(ctx); err != nil {
		return err
	}
	if err := s.deliverPending(ctx); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := s.ListAgents(ctx); err != nil {
				return err
			}
			if err := s.deliverPending(ctx); err != nil {
				return err
			}
		}
	}
}

func (s *Source) installHooks(ctx context.Context) error {
	if _, err := s.runner.Run(ctx, "tmux", "set-option", "-g", "focus-events", "on"); err != nil {
		return fmt.Errorf("enable tmux focus events: %w", err)
	}
	command := "run-shell 'nexo --backend tmux agents seen #{pane_id}'"
	if _, err := s.runner.Run(ctx, "tmux", "set-hook", "-g", "pane-focus-in", command); err != nil {
		return err
	}
	_, err := s.runner.Run(ctx, "tmux", "set-hook", "-g", "client-session-changed", command)
	return err
}

func acquireWatcherLock(path string) (func(), error) {
	return acquireFileLock(path, true)
}

func acquireFileLock(path string, nonBlocking bool) (func(), error) {
	release, err := statefile.Lock(path, nonBlocking)
	if err != nil {
		if nonBlocking && (errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)) {
			return nil, errWatcherAlreadyRunning
		}
		return nil, err
	}
	return release, nil
}

var (
	watchersMu sync.Mutex
	watchers   = make(map[string]struct{})
)

var _ capabilities.AgentSource = (*Source)(nil)

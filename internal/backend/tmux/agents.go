package tmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	id          string
	target      string
	containerID string
	container   string
	window      string
	pid         string
	command     string
	path        string
	harness     string
	branch      string
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
	runner     runner.CommandRunner
	config     config.AgentsConfig
	now        func() time.Time
	stateDir   string
	server     string
	notifier   capabilities.Notifier
	hookBinary string
	hookConfig string
	mu         sync.Mutex
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

// ConfigureHookCommand selects the executable and config file used by focus
// hooks installed for this tmux server.
func (s *Source) ConfigureHookCommand(binary, configPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hookBinary = binary
	s.hookConfig = configPath
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
		screen, err := s.runTmux(ctx, "capture-pane", "-p", "-J", "-t", p.id)
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
				ContainerID:  current.containerID,
				Container:    current.container,
				Window:       current.window,
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

func (s *Source) runTmux(ctx context.Context, args ...string) ([]byte, error) {
	return s.runner.Run(ctx, "tmux", UTF8Args(args...)...)
}

func (s *Source) agentPanes(ctx context.Context, cfg config.AgentsConfig) ([]pane, error) {
	format := "#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{session_id}\t#{session_name}\t#{window_name}\t#{window_index}\t#{pane_index}\t#{pane_current_path}"
	output, err := s.runTmux(ctx, "list-panes", "-a", "-F", format)
	if err != nil {
		return nil, fmt.Errorf("list tmux panes: %w", err)
	}
	allow := make(map[string]struct{}, len(cfg.Processes))
	for _, process := range cfg.Processes {
		allow[strings.ToLower(filepath.Base(strings.TrimSpace(process)))] = struct{}{}
	}
	var result []pane
	var tree processTree
	haveTree := false
	for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 9)
		if len(fields) != 9 {
			continue
		}
		containerField := 4
		windowField := 5
		windowIndexField := 6
		paneIndexField := 7
		pathField := 8
		current := strings.TrimSpace(fields[2])
		harness := findHarness(current, allow)
		if harness == "" {
			// macOS reports a foreground comm such as "node" or a version
			// string, and pane_pid is the pane's shell. The agent is usually a
			// descendant of that shell, so inspect one shared process snapshot.
			if !haveTree {
				tree = s.processTree(ctx)
				haveTree = true
			}
			harness = findPaneHarness(fields[1], tree, allow)
		}
		if harness == "" {
			continue
		}
		containerID := strings.TrimSpace(fields[3])
		p := pane{
			id:          strings.TrimSpace(fields[0]),
			pid:         strings.TrimSpace(fields[1]),
			command:     current,
			path:        strings.TrimSpace(fields[pathField]),
			containerID: containerID,
			container:   strings.TrimSpace(fields[containerField]),
			window:      strings.TrimSpace(fields[windowField]),
			target:      fmt.Sprintf("%s:%s.%s", fields[containerField], fields[windowIndexField], fields[paneIndexField]),
			harness:     harness,
		}
		p.branch = s.branch(ctx, p.path)
		result = append(result, p)
	}
	return result, nil
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

// processTree is one point-in-time view of the process table. Both BSD/macOS
// ps and procps accept the "ps -A -o pid=,ppid=,args=" snapshot used to build
// it.
type processTree struct {
	args     map[int]string
	children map[int][]int
}

func (s *Source) processTree(ctx context.Context) processTree {
	output, err := s.runner.Run(ctx, "ps", "-A", "-o", "pid=,ppid=,args=")
	if err != nil {
		return processTree{}
	}
	return parseProcessTree(output)
}

func parseProcessTree(output []byte) processTree {
	tree := processTree{args: make(map[int]string), children: make(map[int][]int)}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimLeft(line, " \t")
		if line == "" {
			continue
		}
		pidEnd := strings.IndexAny(line, " \t")
		if pidEnd < 0 {
			continue
		}
		pid, err := strconv.Atoi(line[:pidEnd])
		if err != nil {
			continue
		}
		line = strings.TrimLeft(line[pidEnd:], " \t")
		ppidEnd := strings.IndexAny(line, " \t")
		if ppidEnd < 0 {
			continue
		}
		ppid, err := strconv.Atoi(line[:ppidEnd])
		if err != nil {
			continue
		}
		tree.args[pid] = strings.TrimSpace(line[ppidEnd:])
		tree.children[ppid] = append(tree.children[ppid], pid)
	}
	return tree
}

// findPaneHarness walks a pane's process tree breadth-first and returns the
// first harness named by a process command line. The pane process itself is
// included because a pane can run the agent as its main process while tmux
// still reports a non-harness current command.
func findPaneHarness(panePID string, tree processTree, allow map[string]struct{}) string {
	start, err := strconv.Atoi(strings.TrimSpace(panePID))
	if err != nil {
		return ""
	}
	queue := []int{start}
	visited := map[int]struct{}{start: {}}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if harness := findHarness(tree.args[pid], allow); harness != "" {
			return harness
		}
		for _, child := range tree.children[pid] {
			if _, ok := visited[child]; ok {
				continue
			}
			visited[child] = struct{}{}
			queue = append(queue, child)
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
	output, err := s.runTmux(ctx, "display-message", "-p", "#{socket_path}")
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
	// The tmux focus hook fires for every pane, including panes that have no
	// tracked agent state. Read first so an unknown or already-visible pane is
	// a silent no-op that creates no state artifacts.
	saved, err := s.readPersistedStates()
	if err != nil {
		return fmt.Errorf("read agent state: %w", err)
	}
	state, ok := saved.Agents[paneID]
	if !ok || state.State != "stopped" {
		return nil
	}
	return s.updateStates(func(states map[string]agentState) error {
		current, ok := states[paneID]
		if !ok || current.State != "stopped" {
			return nil
		}
		current.Seen = true
		states[paneID] = current
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
	if err := s.installHooksQuiet(ctx); err != nil {
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
	return s.installHooksWithWarning(ctx, true)
}

func (s *Source) installHooksQuiet(ctx context.Context) error {
	return s.installHooksWithWarning(ctx, false)
}

func (s *Source) installHooksWithWarning(ctx context.Context, warnFocusEvents bool) error {
	focusEvents, err := s.runTmux(ctx, "show-option", "-gqv", "focus-events")
	if err != nil {
		return fmt.Errorf("read tmux focus events: %w", err)
	}
	focusEventsEnabled := strings.TrimSpace(string(focusEvents)) == "on"
	s.mu.Lock()
	binary, configPath := s.hookBinary, s.hookConfig
	s.mu.Unlock()
	if binary == "" {
		binary = os.Args[0]
	}
	command := focusHookCommand(binary, configPath)
	for _, hook := range []string{"pane-focus-in", "client-session-changed"} {
		output, err := s.runTmux(ctx, "show-hooks", "-g", hook)
		if err != nil {
			return fmt.Errorf("read tmux %s hooks: %w", hook, err)
		}
		index := nexoHookIndex(string(output), hook)
		if index >= 0 {
			if _, err := s.runTmux(ctx, "set-hook", "-g", fmt.Sprintf("%s[%d]", hook, index), command); err != nil {
				return err
			}
			continue
		}
		if _, err := s.runTmux(ctx, "set-hook", "-ag", hook, command); err != nil {
			return err
		}
	}
	if !focusEventsEnabled && warnFocusEvents {
		const warning = "nexo: agent seen-marking needs 'set -g focus-events on'"
		if _, err := s.runTmux(ctx, "display-message", warning); err != nil {
			return fmt.Errorf("report tmux focus events requirement: %w", err)
		}
	}
	return nil
}

const focusHookMarker = "NEXO_AGENT_FOCUS_HOOK=1"

func focusHookCommand(binary, configPath string) string {
	command := focusHookMarker + " " + shellQuote(binary) + " --backend=" + shellQuote("tmux")
	if configPath != "" {
		command += " --config-file=" + shellQuote(configPath)
	}
	command += " agents seen #{pane_id}"
	return "run-shell " + shellQuote(command)
}

func nexoHookIndex(output, hook string) int {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		prefix := hook + "["
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		end := strings.IndexByte(line[len(prefix):], ']')
		if end < 0 {
			continue
		}
		index, err := strconv.Atoi(line[len(prefix) : len(prefix)+end])
		if err != nil || !strings.HasPrefix(strings.TrimSpace(line[len(prefix)+end+1:]), `run-shell "`+focusHookMarker+" ") {
			continue
		}
		return index
	}
	return -1
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

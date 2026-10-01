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

	"github.com/vieitesss/nexo/internal/agentstate"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/git"
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

type observedPane struct {
	pane
	working bool
}

type pendingNotification struct {
	ID      uint64 `json:"id"`
	PaneID  string `json:"pane_id"`
	Harness string `json:"harness"`
	Target  string `json:"target"`
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

	present := make([]string, 0, len(panes))
	for _, current := range panes {
		present = append(present, current.id)
	}
	working := make(map[string]struct{}, len(observed))
	observations := make([]agentstate.Observation, 0, len(observed))
	for _, current := range observed {
		if current.working {
			working[current.id] = struct{}{}
		}
		observations = append(observations, agentstate.Observation{
			ID:      current.id,
			Working: current.working,
		})
	}
	results, err := s.ledger().Observe(
		agentstate.Batch{Present: present, Observations: observations},
		agentstate.Policy{IdleThreshold: time.Duration(cfg.IdleSeconds) * time.Second},
		func(document *agentstate.Document, transitions []agentstate.Transition) error {
			return updatePending(document, cfg, present, working, transitions, observed)
		},
	)
	if err != nil {
		return nil, err
	}
	agents := make([]capabilities.Agent, 0, len(results))
	for index, result := range results {
		current := observed[index]
		state := result.State
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
		p.branch = git.Branch(ctx, s.runner, p.path)
		result = append(result, p)
	}
	return result, nil
}

// interpreters are runtimes whose own name never identifies an agent; the
// harness is the script they execute instead.
var interpreters = map[string]struct{}{
	"node":    {},
	"bun":     {},
	"deno":    {},
	"python":  {},
	"python3": {},
}

// findHarness returns the allowed harness named by a process's command line.
// Only the process identity counts: the basename of argv[0], or, when argv[0]
// is an interpreter, the basename of the script it runs.
func findHarness(args string, allow map[string]struct{}) string {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return ""
	}
	name := commandBase(fields[0])
	if _, ok := interpreters[name]; ok {
		name = scriptBase(fields[1:])
	}
	if name == "" {
		return ""
	}
	if _, ok := allow[name]; ok {
		return name
	}
	return ""
}

func commandBase(field string) string {
	field = strings.Trim(field, "\"'(),[]{}")
	if field == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(field))
}

// scriptBase returns the basename of the first non-flag argument, the script
// passed to an interpreter.
func scriptBase(args []string) string {
	for _, field := range args {
		field = strings.Trim(field, "\"'(),[]{}")
		if field == "" || strings.HasPrefix(field, "-") {
			continue
		}
		return strings.ToLower(filepath.Base(field))
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
// shallowest harness named by a process command line. Ties at the same depth
// resolve to the lowest pid so discovery is deterministic. The pane process
// itself is included because a pane can run the agent as its main process
// while tmux still reports a non-harness current command.
func findPaneHarness(panePID string, tree processTree, allow map[string]struct{}) string {
	start, err := strconv.Atoi(strings.TrimSpace(panePID))
	if err != nil {
		return ""
	}
	frontier := []int{start}
	visited := map[int]struct{}{start: {}}
	for len(frontier) > 0 {
		bestPID := -1
		bestHarness := ""
		for _, pid := range frontier {
			harness := findHarness(tree.args[pid], allow)
			if harness == "" {
				continue
			}
			if bestPID == -1 || pid < bestPID {
				bestPID = pid
				bestHarness = harness
			}
		}
		if bestHarness != "" {
			return bestHarness
		}
		var next []int
		for _, pid := range frontier {
			for _, child := range tree.children[pid] {
				if _, ok := visited[child]; ok {
					continue
				}
				visited[child] = struct{}{}
				next = append(next, child)
			}
		}
		frontier = next
	}
	return ""
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

func (s *Source) ledger() *agentstate.Ledger {
	return agentstate.New(s.stateDir, s.server, s.now)
}

const (
	pendingNotificationsKey = "pending_notifications"
	nextNotificationIDKey   = "next_notification_id"
	lastNotificationKey     = "last_notification"
)

type notificationState struct {
	pending []pendingNotification
	nextID  uint64
	last    time.Time
}

func readNotificationState(document *agentstate.Document) (notificationState, error) {
	var state notificationState
	if raw := document.Extra[pendingNotificationsKey]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &state.pending); err != nil {
			return notificationState{}, err
		}
	}
	if raw := document.Extra[nextNotificationIDKey]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &state.nextID); err != nil {
			return notificationState{}, err
		}
	}
	if raw := document.Extra[lastNotificationKey]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &state.last); err != nil {
			return notificationState{}, err
		}
	}
	return state, nil
}

func writeNotificationState(document *agentstate.Document, state notificationState) error {
	if document.Extra == nil {
		document.Extra = make(map[string]json.RawMessage)
	}
	write := func(key string, value any, present bool) error {
		if !present {
			delete(document.Extra, key)
			return nil
		}
		contents, err := json.Marshal(value)
		if err != nil {
			return err
		}
		document.Extra[key] = contents
		return nil
	}
	if err := write(pendingNotificationsKey, state.pending, len(state.pending) != 0); err != nil {
		return err
	}
	if err := write(nextNotificationIDKey, state.nextID, state.nextID != 0); err != nil {
		return err
	}
	return write(lastNotificationKey, state.last, !state.last.IsZero())
}

func updatePending(document *agentstate.Document, cfg config.AgentsConfig, present []string, working map[string]struct{}, transitions []agentstate.Transition, observed []observedPane) error {
	state, err := readNotificationState(document)
	if err != nil {
		return err
	}
	if !cfg.NotificationsEnabled() {
		state.pending = nil
		return writeNotificationState(document, state)
	}
	presentSet := make(map[string]struct{}, len(present))
	for _, paneID := range present {
		presentSet[paneID] = struct{}{}
	}
	filtered := state.pending[:0]
	for _, pending := range state.pending {
		if _, ok := presentSet[pending.PaneID]; !ok {
			continue
		}
		if _, ok := working[pending.PaneID]; ok {
			continue
		}
		filtered = append(filtered, pending)
	}
	state.pending = filtered
	byPane := make(map[string]observedPane, len(observed))
	for _, current := range observed {
		byPane[current.id] = current
	}
	for _, transition := range transitions {
		current, ok := byPane[transition.Observation.ID]
		if !ok {
			continue
		}
		state.nextID++
		state.pending = append(state.pending, pendingNotification{
			ID:      state.nextID,
			PaneID:  current.id,
			Harness: current.harness,
			Target:  current.target,
		})
	}
	return writeNotificationState(document, state)
}

func (s *Source) statePath(suffix string) string {
	return s.ledger().Path(suffix)
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
	s.mu.Unlock()
	if notifier == nil || !cfg.NotificationsEnabled() || server == "" {
		return nil
	}
	document, err := s.ledger().ReadLocked()
	if err != nil {
		return err
	}
	saved, err := readNotificationState(&document)
	if err != nil {
		return fmt.Errorf("read agent state: %w", err)
	}
	if len(saved.pending) == 0 || (!saved.last.IsZero() && now.Sub(saved.last) < notificationInterval) {
		return nil
	}
	message := formatPendingNotification(saved.pending)
	ids := make(map[uint64]struct{}, len(saved.pending))
	for _, pending := range saved.pending {
		ids[pending.ID] = struct{}{}
	}
	// A failed notification remains durable and will be retried on the next
	// poll. State is acknowledged only after the runner reports success.
	if err := notifier.Notify(ctx, message); err != nil {
		return nil
	}
	return s.ledger().Update(func(document *agentstate.Document) error {
		saved, err := readNotificationState(document)
		if err != nil {
			return err
		}
		remaining := saved.pending[:0]
		for _, pending := range saved.pending {
			if _, sent := ids[pending.ID]; !sent {
				remaining = append(remaining, pending)
			}
		}
		saved.pending = remaining
		if len(ids) > 0 {
			saved.last = now
		}
		return writeNotificationState(document, saved)
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
	// tracked agent state. The ledger makes unknown or already-visible panes a
	// silent no-op without creating state artifacts.
	if err := s.ledger().MarkSeen(paneID); err != nil {
		return err
	}
	return nil
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

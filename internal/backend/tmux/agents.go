package tmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
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
	windowID    string
	pid         string
	command     string
	path        string
	harness     string
	branch      string
	// focused reports that the pane is the active pane of the active window
	// in a session some client is attached to.
	focused bool
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

const watcherShutdownTimeout = 5 * time.Second

// Source discovers agent panes in a tmux server and polls their visible
// screens for harness working indicators.
type Source struct {
	runner       runner.CommandRunner
	config       config.AgentsConfig
	glance       config.GlanceConfig
	glanceSynced bool
	now          func() time.Time
	stateDir     string
	server       string
	notifier     capabilities.Notifier
	hookBinary   string
	hookConfig   string
	binary       *binaryWatch
	mu           sync.Mutex
}

// NewAgentSource creates a tmux AgentSource with the default configuration.
func NewAgentSource(r runner.CommandRunner) *Source {
	return &Source{
		runner: r,
		config: (config.AgentsConfig{}).WithDefaults(),
		now:    time.Now,
		binary: newBinaryWatch(),
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
	return s.listAgents(ctx, false)
}

// poll lists agents and reconciles the tmux window-list highlight. Only the
// watcher applies highlights; plain listing stays read-only.
func (s *Source) poll(ctx context.Context) ([]capabilities.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listAgents(ctx, true)
}

func (s *Source) listAgents(ctx context.Context, highlight bool) ([]capabilities.Agent, error) {
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
	screens := make([][]byte, len(panes))
	captured := make([]bool, len(panes))
	forEachPane(len(panes), func(index int) {
		screen, err := s.runTmux(ctx, "capture-pane", "-p", "-J", "-t", panes[index].id)
		screens[index], captured[index] = screen, err == nil
	})
	observed := make([]observedPane, 0, len(panes))
	for index, p := range panes {
		if !captured[index] {
			// A pane can disappear between list-panes and capture-pane. It is
			// not an error for the navigator to omit that pane.
			continue
		}
		observed = append(observed, observedPane{
			pane:    p,
			working: MatchesWorkingIndicator(p.harness, string(screens[index]), cfg.WorkingPattern),
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
			Focused: current.focused,
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
	if highlight {
		if err := s.reconcileHighlights(ctx, unseenWindows(observed, results)); err != nil {
			return nil, err
		}
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
	if highlight {
		if err := s.reconcileGlanceLocked(ctx, agents); err != nil {
			return nil, err
		}
	}
	return agents, nil
}

func (s *Source) runTmux(ctx context.Context, args ...string) ([]byte, error) {
	return s.runner.Run(ctx, "tmux", UTF8Args(args...)...)
}

func (s *Source) agentPanes(ctx context.Context, cfg config.AgentsConfig) ([]pane, error) {
	format := "#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{session_id}\t#{session_name}\t#{window_name}\t#{window_index}\t#{pane_index}\t#{window_id}\t#{&&:#{session_attached},#{&&:#{window_active},#{pane_active}}}\t#{pane_current_path}"
	output, err := s.runTmux(ctx, "list-panes", "-a", "-F", format)
	if err != nil {
		// tmux sources its config on a fresh server before the first session
		// exists, and `list-panes -a` then fails instead of listing nothing.
		if strings.Contains(interactiveStderr(err), "no current target") {
			return nil, nil
		}
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
		fields := strings.SplitN(line, "\t", 11)
		if len(fields) != 11 {
			continue
		}
		containerField := 4
		windowField := 5
		windowIndexField := 6
		paneIndexField := 7
		windowIDField := 8
		focusedField := 9
		pathField := 10
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
			windowID:    strings.TrimSpace(fields[windowIDField]),
			target:      fmt.Sprintf("%s:%s.%s", fields[containerField], fields[windowIndexField], fields[paneIndexField]),
			harness:     harness,
			focused:     strings.TrimSpace(fields[focusedField]) == "1",
		}
		result = append(result, p)
	}
	forEachPane(len(result), func(index int) {
		result[index].branch = git.Branch(ctx, s.runner, result[index].path)
	})
	return result, nil
}

// paneConcurrency bounds the per-pane subprocesses (git branch, capture-pane)
// one listing runs at once.
const paneConcurrency = 8

// forEachPane calls fn for every index in [0, count) with at most
// paneConcurrency calls in flight, and returns once all of them have.
func forEachPane(count int, fn func(index int)) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, paneConcurrency)
	for index := range count {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			fn(index)
		}()
	}
	wg.Wait()
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

func (s *Source) watcherPIDPath() string {
	return s.statePath("watcher")
}

func (s *Source) writeWatcherPID() error {
	return statefile.WriteAtomic(s.watcherPIDPath(), []byte(strconv.Itoa(os.Getpid())+"\n"))
}

func (s *Source) clearWatcherPID(pid int) {
	contents, err := os.ReadFile(s.watcherPIDPath())
	if err != nil || strings.TrimSpace(string(contents)) != strconv.Itoa(pid) {
		return
	}
	_ = os.Remove(s.watcherPIDPath())
}

func (s *Source) removeStaleWatcherPID() {
	_ = os.Remove(s.watcherPIDPath())
}

func (s *Source) watcherPID() (int, error) {
	contents, err := os.ReadFile(s.watcherPIDPath())
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid watcher pid %q", strings.TrimSpace(string(contents)))
	}
	return pid, nil
}

// watcherLockHeld checks the lock without changing its ownership. A free lock
// means the old watcher has already gone, so its owner record is stale.
func watcherLockHeld(path string) (bool, error) {
	release, err := acquireWatcherLock(path)
	if err == nil {
		release()
		return false, nil
	}
	if errors.Is(err, errWatcherAlreadyRunning) {
		return true, nil
	}
	return false, err
}

func isNexoAgentWatcher(commandLine string) bool {
	fields := strings.Fields(commandLine)
	hasNexo := false
	for _, field := range fields {
		field = strings.Trim(field, "'\"")
		if filepath.Base(field) == "nexo" {
			hasNexo = true
			break
		}
	}
	if !hasNexo {
		return false
	}
	for index := 0; index+1 < len(fields); index++ {
		if strings.Trim(fields[index], "'\"") == "agents" && strings.Trim(fields[index+1], "'\"") == "watch" {
			return true
		}
	}
	return false
}

// verifyWatcher confirms the owner record still names the same process, that
// the watcher lock remains held, and that the PID is running nexo agents watch.
// It returns false when the lock is free, treating the old watcher as gone.
func (s *Source) verifyWatcher(ctx context.Context, lockPath string, pid int) (bool, error) {
	held, err := watcherLockHeld(lockPath)
	if err != nil {
		return false, fmt.Errorf("check tmux agent watcher lock: %w", err)
	}
	if !held {
		s.removeStaleWatcherPID()
		return false, nil
	}
	recorded, err := s.watcherPID()
	if err != nil {
		return false, fmt.Errorf("verify tmux agent watcher owner: %w", err)
	}
	if recorded != pid {
		return false, fmt.Errorf("verify tmux agent watcher owner: owner record changed from %d to %d", pid, recorded)
	}
	if s.runner == nil {
		return false, errors.New("verify tmux agent watcher owner: command runner is unavailable")
	}
	output, err := s.runner.Run(ctx, "ps", "-o", "args=", "-p", strconv.Itoa(pid))
	if err != nil {
		return false, fmt.Errorf("verify tmux agent watcher owner %d: %w", pid, err)
	}
	if !isNexoAgentWatcher(string(output)) {
		return false, fmt.Errorf("verify tmux agent watcher owner %d: process is not nexo agents watch", pid)
	}
	return true, nil
}

// ReplaceWatcher stops the existing watcher for this tmux server, if any.
// Init calls this before launching the current binary so upgrading nexo never
// leaves an old watcher holding the server lock and silently losing the new
// configuration.
func (s *Source) ReplaceWatcher(ctx context.Context) error {
	s.mu.Lock()
	if s.server == "" {
		s.server = s.serverName(ctx)
	}
	lockPath := s.statePath("lock")
	s.mu.Unlock()

	release, err := acquireWatcherLock(lockPath)
	if err == nil {
		s.removeStaleWatcherPID()
		release()
		return nil
	}
	if !errors.Is(err, errWatcherAlreadyRunning) {
		return err
	}
	pid, err := s.watcherPID()
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace tmux agent watcher: running watcher has no owner record")
	}
	if err != nil {
		return fmt.Errorf("replace tmux agent watcher: read owner: %w", err)
	}
	if pid == os.Getpid() {
		return errors.New("replace tmux agent watcher: refusing to stop this process")
	}
	verified, err := s.verifyWatcher(ctx, lockPath, pid)
	if err != nil {
		return fmt.Errorf("replace tmux agent watcher: %w", err)
	}
	if !verified {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("replace tmux agent watcher: find owner %d: %w", pid, err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("replace tmux agent watcher: stop owner %d: %w", pid, err)
	}
	if err := waitForWatcherLock(lockPath, watcherShutdownTimeout); err == nil {
		return nil
	}
	// A watcher from an older binary may not handle SIGTERM. Verify both the
	// process identity and lock ownership again before escalating.
	verified, err = s.verifyWatcher(ctx, lockPath, pid)
	if err != nil {
		return fmt.Errorf("replace tmux agent watcher: %w", err)
	}
	if !verified {
		return nil
	}
	if err := process.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("replace tmux agent watcher: force-stop owner %d: %w", pid, err)
	}
	if err := waitForWatcherLock(lockPath, watcherShutdownTimeout); err != nil {
		return fmt.Errorf("replace tmux agent watcher: wait for owner %d: %w", pid, err)
	}
	return nil
}

func waitForWatcherLock(path string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		release, err := acquireWatcherLock(path)
		if err == nil {
			release()
			return nil
		}
		if !errors.Is(err, errWatcherAlreadyRunning) {
			return err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
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
// watchers for one server explicit and releases automatically on crashes.
func (s *Source) Watch(ctx context.Context) (err error) {
	watchCtx, stopSignals := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	// A signal is an orderly stop: Init replaces watchers with SIGTERM, and
	// tmux reports any non-zero exit of the run-shell that started this one.
	defer func() {
		if err != nil && ctx.Err() == nil && watchCtx.Err() != nil {
			err = nil
		}
	}()
	watch := s.binary
	if watch == nil {
		watch = newBinaryWatch()
	}
	// Capture before taking the watcher lock or installing hooks. If the
	// initial stat is transiently unavailable, replaced() retries capture on
	// a later poll instead of permanently disabling reloads.
	watch.capture()

	s.mu.Lock()
	if s.server == "" {
		s.server = s.serverName(watchCtx)
	}
	s.mu.Unlock()
	lockPath := s.statePath("lock")
	release, err := acquireWatcherLock(lockPath)
	if err != nil {
		return err
	}
	// The lock fd is O_CLOEXEC (os.OpenFile sets it), so a re-exec in place
	// releases the lock automatically and the new process can take it. The
	// re-exec keeps this PID, so the recorded watcher PID stays valid.
	if err := s.writeWatcherPID(); err != nil {
		release()
		return fmt.Errorf("record tmux agent watcher: %w", err)
	}
	pid := os.Getpid()
	defer watch.clearFailed()
	defer func() {
		s.clearWatcherPID(pid)
		release()
	}()
	// An orderly shutdown clears every highlight this watcher owns. The context
	// is already canceled, so cleanup gets its own bounded one. A crash cannot
	// run this; the next startup reconciles the durable marker instead.
	defer func() {
		if watchCtx.Err() == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), highlightCleanupTimeout)
		defer cancel()
		_ = s.clearHighlights(cleanupCtx)
	}()
	if err := s.installHooksQuiet(watchCtx); err != nil {
		return err
	}
	s.mu.Lock()
	interval := s.config.WithDefaults().PollInterval
	s.mu.Unlock()
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	if _, err := s.poll(watchCtx); err != nil {
		return err
	}
	if err := s.deliverPending(watchCtx); err != nil {
		return err
	}
	for {
		select {
		case <-watchCtx.Done():
			return watchCtx.Err()
		case <-ticker.C:
			if _, err := s.poll(watchCtx); err != nil {
				return err
			}
			if err := s.deliverPending(watchCtx); err != nil {
				return err
			}
			s.reexecIfReplaced(watchCtx, watch)
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
		indices := nexoHookIndices(string(output), hook)
		if len(indices) > 0 {
			if _, err := s.runTmux(ctx, "set-hook", "-g", fmt.Sprintf("%s[%d]", hook, indices[0]), command); err != nil {
				return err
			}
			// Hooks left by older nexo versions may run commands this binary no
			// longer accepts, so keep only the one just rewritten.
			for _, index := range indices[1:] {
				if _, err := s.runTmux(ctx, "set-hook", "-gu", fmt.Sprintf("%s[%d]", hook, index)); err != nil {
					return err
				}
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
	// run-shell does not provide TMUX_PANE to child processes, so pass the
	// focused pane explicitly. Keep the marker first so init can replace its
	// own hook without accumulating duplicates.
	command := focusHookMarker + " TMUX_PANE=#{pane_id} " + shellQuote(binary) + " --backend=" + shellQuote("tmux")
	if configPath != "" {
		command += " --config-file=" + shellQuote(configPath)
	}
	// The focus command records both container history and the agent's seen
	// transition in one backend path.
	command += " focus"
	// -b keeps the durable history write off the synchronous hook queue; the
	// store's flock preserves ordering between concurrent focus events.
	return "run-shell -b " + shellQuote(command)
}

// nexoHookIndices returns the index of every focus hook any nexo version
// installed, in tmux's display order. Older versions ran the hook without -b,
// so match the marker inside any run-shell command rather than one exact form.
func nexoHookIndices(output, hook string) []int {
	var indices []int
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
		command := strings.TrimSpace(line[len(prefix)+end+1:])
		if err != nil || !strings.HasPrefix(command, "run-shell ") || !strings.Contains(command, `"`+focusHookMarker+" ") {
			continue
		}
		indices = append(indices, index)
	}
	return indices
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

var _ capabilities.AgentSource = (*Source)(nil)

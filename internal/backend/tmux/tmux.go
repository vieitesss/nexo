// Package tmux contains the tmux backend adapter.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/git"
	"github.com/vieitesss/nexo/internal/lifecycle"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/proxyidentity"
	"github.com/vieitesss/nexo/internal/runner"
)

// Backend is the tmux backend. Capabilities are added independently as each
// feature is implemented.
type Backend struct {
	Runner   runner.CommandRunner
	Inside   bool
	agents   *Source
	notifier *Notifier

	proxyConfig     config.RemoteConfig
	proxyConfigPath string
	remoteMachines  proxyidentity.MachineSet
	usageStateDir   string
	usageServer     string
}

// New constructs a tmux backend using the supplied command runner. The
// optional argument identifies whether nexo is running inside tmux.
func New(r runner.CommandRunner, inside ...bool) *Backend {
	inTmux := os.Getenv("TMUX") != "" || os.Getenv("TMUX_PANE") != ""
	if len(inside) > 0 {
		inTmux = inside[0]
	}
	notifier := NewNotifier(r)
	agents := NewAgentSource(r)
	agents.SetNotifier(notifier)
	return &Backend{Runner: r, Inside: inTmux, agents: agents, notifier: notifier, remoteMachines: proxyidentity.NewMachineSet(nil)}
}

// UTF8Args returns tmux arguments prefixed with the global -u flag. Without
// it, tmux replaces control characters such as the tab used in format
// separators when the process has no UTF-8 locale, which is the norm for
// non-interactive SSH shells. Every tmux output nexo parses must go through
// this helper.
func UTF8Args(args ...string) []string {
	return append([]string{"-u"}, args...)
}

// Error reports a failed tmux operation and preserves tmux's stderr.
type Error struct {
	Operation string
	Args      []string
	Stderr    string
	Err       error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("tmux %s: %s", e.Operation, strings.TrimSpace(e.Stderr))
	}
	return fmt.Sprintf("tmux %s: %v", e.Operation, e.Err)
}

// Unwrap preserves the command runner error for errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// UnsupportedError reports a project-opening option that tmux does not
// support.
type UnsupportedError struct {
	Option string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("tmux does not support %s", e.Option)
}

func (b *Backend) CreateWorktree(ctx context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	if b == nil || b.Runner == nil {
		return capabilities.Worktree{}, errors.New("tmux worktree creator requires a command runner")
	}
	return lifecycle.CreateWorktree(ctx, options, lifecycle.WorktreeAdapter{
		Create:   b.worktrees().Create,
		Open:     b.openProjectNative,
		Rollback: b.rollbackCreatedWorktree,
	})
}

func (b *Backend) ExtractWorktree(ctx context.Context, options capabilities.WorktreeExtractOptions) (capabilities.Worktree, error) {
	if b == nil || b.Runner == nil {
		return capabilities.Worktree{}, errors.New("tmux worktree extractor requires a command runner")
	}
	return lifecycle.ExtractWorktree(ctx, options, lifecycle.ExtractAdapter{
		WorktreeAdapter: lifecycle.WorktreeAdapter{
			Create:   b.worktrees().Create,
			Open:     b.openProjectNative,
			Rollback: b.rollbackCreatedWorktree,
		},
		Git: git.New(b.Runner),
	})
}

func (b *Backend) worktrees() *git.Worktrees {
	return git.NewWorktrees(b.runGit)
}

func (b *Backend) rollbackCreatedWorktree(ctx context.Context, worktree capabilities.Worktree) error {
	rollbackErr := b.worktrees().Rollback(ctx, worktree)
	if worktree.Container.Created && worktree.SessionName != "" {
		if _, killErr := b.run(ctx, "kill-session", "-t", "="+worktree.SessionName); killErr != nil {
			rollbackErr = errors.Join(rollbackErr, killErr)
		}
	}
	return rollbackErr
}

// RollbackExtractWorktree removes the extracted worktree and restores its
// branch in the original checkout. Cleanup uses a detached bounded context so
// output failures cannot strand the original branch.
func (b *Backend) RollbackExtractWorktree(ctx context.Context, worktree capabilities.Worktree) error {
	cleanupCtx, cancel := lifecycle.CleanupContext(ctx)
	defer cancel()
	cleanupErr := b.rollbackCreatedWorktree(cleanupCtx, worktree)
	if worktree.Repo == "" || worktree.Branch == "" {
		return errors.Join(cleanupErr, errors.New("extracted worktree result lacks original branch identity"))
	}
	restoreErr := git.New(b.Runner).Checkout(cleanupCtx, worktree.Repo, worktree.Branch)
	return errors.Join(cleanupErr, restoreErr)
}

var _ capabilities.WorktreeExtractionRollback = (*Backend)(nil)

func (b *Backend) RemoveWorktree(ctx context.Context, worktree capabilities.Worktree) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux worktree remover requires a command runner")
	}
	var failures []error
	if worktree.SessionName != "" {
		if _, err := b.run(ctx, "kill-session", "-t", "="+worktree.SessionName); err != nil {
			failures = append(failures, err)
		}
	}
	failures = append(failures, b.worktrees().Remove(ctx, worktree))
	return errors.Join(failures...)
}

func (b *Backend) runGit(ctx context.Context, args ...string) ([]byte, error) {
	output, err := b.Runner.Run(ctx, "git", args...)
	if err == nil {
		return output, nil
	}
	stderr := string(output)
	if stderr == "" {
		stderr = err.Error()
	}
	return nil, &Error{Operation: "git", Args: append([]string(nil), args...), Stderr: stderr, Err: err}
}

// OpenProject opens path in a tmux session and focuses it unless NoFocus is
// set. Shared lifecycle policy normalizes the result at this module's seam.
func (b *Backend) OpenProject(ctx context.Context, path string, options capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
	container, err := lifecycle.OpenProject(ctx, path, options, b.openProjectNative)
	if err != nil {
		return container, err
	}
	if options.NoFocus || container.ID == "" {
		return container, nil
	}
	if err := b.recordTarget(ctx, container.ID, ""); err != nil {
		return container, err
	}
	return container, nil
}

func (b *Backend) openProjectNative(ctx context.Context, path string, options capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
	if options.ContainerID != "" {
		return capabilities.OpenedContainer{}, &UnsupportedError{Option: "--workspace-id"}
	}
	if b == nil || b.Runner == nil {
		return capabilities.OpenedContainer{}, errors.New("tmux project opener requires a command runner")
	}

	name := projects.TargetName(path)
	_, err := b.run(ctx, "has-session", "-t", "="+name)
	exists := err == nil
	if err != nil && !isMissingSession(err) {
		return capabilities.OpenedContainer{}, err
	}
	created := false
	if !exists {
		if _, err := b.run(ctx, "new-session", "-d", "-s", name, "-c", path); err != nil {
			return capabilities.OpenedContainer{}, err
		}
		created = true
	}

	container := capabilities.OpenedContainer{Name: name, Path: path, Created: created}
	id, err := b.sessionID(ctx, name)
	if err != nil {
		return container, err
	}
	container.ID = id

	if options.NoFocus {
		return container, nil
	}
	if b.Inside {
		_, err = b.runClient(ctx, "switch-client", "-t", "="+name)
		return container, err
	}
	return container, b.runInteractive(ctx, "attach-session", "-t", "="+name)
}

func (b *Backend) sessionID(ctx context.Context, name string) (string, error) {
	output, err := b.run(ctx, "display-message", "-p", "-t", "="+name+":", "#{session_id}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (b *Backend) runInteractive(ctx context.Context, operation string, args ...string) error {
	interactive, ok := b.Runner.(runner.InteractiveRunner)
	if !ok {
		return &Error{
			Operation: operation,
			Args:      append([]string(nil), args...),
			Stderr:    "interactive command runner is unavailable",
			Err:       errors.New("runner does not support interactive commands"),
		}
	}
	if err := interactive.RunInteractive(ctx, "tmux", append([]string{operation}, args...)...); err != nil {
		stderr := interactiveStderr(err)
		if stderr == "" {
			stderr = err.Error()
		}
		return &Error{
			Operation: operation,
			Args:      append([]string(nil), args...),
			Stderr:    stderr,
			Err:       err,
		}
	}
	return nil
}

func (b *Backend) run(ctx context.Context, operation string, args ...string) ([]byte, error) {
	return b.runWithArgs(ctx, UTF8Args(append([]string{operation}, args...)...), operation, args...)
}

func (b *Backend) runClient(ctx context.Context, operation string, args ...string) ([]byte, error) {
	return b.runWithArgs(ctx, append([]string{operation}, args...), operation, args...)
}

func (b *Backend) runWithArgs(ctx context.Context, tmuxArgs []string, operation string, args ...string) ([]byte, error) {
	output, err := b.Runner.Run(ctx, "tmux", tmuxArgs...)
	if err == nil {
		return output, nil
	}
	stderr := string(output)
	if captured := interactiveStderr(err); captured != "" {
		stderr = captured
	}
	if stderr == "" {
		stderr = err.Error()
	}
	return nil, &Error{
		Operation: operation,
		Args:      append([]string(nil), args...),
		Stderr:    stderr,
		Err:       err,
	}
}

func interactiveStderr(err error) string {
	var interactiveErr *runner.InteractiveError
	if errors.As(err, &interactiveErr) && interactiveErr.Stderr != "" {
		return interactiveErr.Stderr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return string(exitErr.Stderr)
	}
	return ""
}

func isMissingSession(err error) bool {
	var tmuxErr *Error
	if !errors.As(err, &tmuxErr) {
		return false
	}
	message := strings.ToLower(tmuxErr.Stderr)
	return strings.Contains(message, "can't find session") ||
		strings.Contains(message, "session not found") ||
		ReportsNoServer(message)
}

// OpenContainer focuses an existing tmux session by stable session ID.
func (b *Backend) OpenContainer(ctx context.Context, id string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux container opener requires a command runner")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("tmux container opener requires an id")
	}
	var err error
	if b.Inside {
		_, err = b.runClient(ctx, "switch-client", "-t", id)
	} else {
		err = b.runInteractive(ctx, "attach-session", "-t", id)
	}
	if err != nil {
		return err
	}
	return b.recordTarget(ctx, id, "")
}

func (b *Backend) usageStore(ctx context.Context) containerstate.Store {
	return containerstate.New(b.usageStateDir, "tmux", containerstate.LocalMachine(), b.serverName(ctx))
}

// serverName resolves and caches the tmux socket path that scopes native IDs.
func (b *Backend) serverName(ctx context.Context) string {
	if b.usageServer != "" {
		return b.usageServer
	}
	output, err := b.run(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil {
		return "default"
	}
	name := strings.TrimSpace(string(output))
	if name == "" {
		return "default"
	}
	b.usageServer = name
	return name
}

func (b *Backend) recordContainer(ctx context.Context, entry containerstate.Entry) error {
	if b == nil {
		return errors.New("tmux container state is unavailable")
	}
	return b.usageStore(ctx).Record(entry)
}

// RecentContainers returns the recorded identities for this tmux server.
func (b *Backend) RecentContainers(ctx context.Context) ([]containerstate.Entry, error) {
	if b == nil || b.Runner == nil {
		return nil, errors.New("tmux container history requires a command runner")
	}
	return b.usageStore(ctx).Entries()
}

// CurrentContainerID reports the session that owns the calling pane.
func (b *Backend) CurrentContainerID(ctx context.Context) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("tmux current container requires a command runner")
	}
	return b.currentContainer(ctx)
}

// CurrentProxyMachine reports the remote machine attached by the calling pane.
func (b *Backend) CurrentProxyMachine(ctx context.Context) (string, error) {
	pane := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if pane == "" {
		return "", nil
	}
	target, err := b.ProxyTarget(ctx, pane)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(target) == "" {
		return "", nil
	}
	identity, ok := proxyidentity.ParseValue(target)
	if !ok {
		return "", nil
	}
	return proxyidentity.NormalizeMachineName(identity.Machine), nil
}

// RecordFocusEvent records the container owning the pane named by a tmux
// focus hook. A proxy's metadata is authoritative, so reconnecting its
// attachment never turns the entry into a local container. Marking the pane
// seen belongs to this same focus transition, keeping the hook's durable
// effects on one path.
func (b *Backend) RecordFocusEvent(ctx context.Context, _ []byte) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux container state requires a command runner")
	}
	paneID := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if paneID == "" {
		return nil
	}
	historyErr := b.recordTarget(ctx, paneID, "")
	seenErr := b.MarkAgentSeen(ctx, paneID)
	return errors.Join(historyErr, seenErr)
}

// recordTarget classifies one tmux target from authoritative session metadata
// and records it. Failed lookups are silent: a vanished pane or session is not
// a focus transition. remoteKind is the multiplexer a machine proxy's pane
// attaches to; session metadata cannot say it, so callers that do not create
// the proxy pass an empty kind and the recorded one is kept.
func (b *Backend) recordTarget(ctx context.Context, target, remoteKind string) error {
	output, err := b.run(ctx, "display-message", "-p", "-t", target, "#{session_id}\t#{session_name}\t#{@nexo-proxy}\t#{socket_path}")
	if err != nil {
		return nil
	}
	fields := strings.Split(strings.TrimSpace(string(output)), "\t")
	if len(fields) < 2 {
		return nil
	}
	if len(fields) >= 4 {
		if server := strings.TrimSpace(fields[3]); server != "" {
			b.usageServer = server
		}
	}
	if len(fields) >= 3 {
		if machine, _, ok := b.proxyTarget(fields[2]); ok {
			return b.recordContainer(ctx, containerstate.Entry{ID: strings.TrimSpace(fields[0]), Machine: machine, Proxy: true, RemoteKind: remoteKind})
		}
	}
	id := strings.TrimSpace(fields[0])
	if id == "" {
		id = strings.TrimSpace(target)
	}
	return b.recordContainer(ctx, containerstate.Entry{ID: id, Name: strings.TrimSpace(fields[1]), Machine: containerstate.LocalMachine()})
}

// ConfigureAgents applies the shared agents configuration to the tmux source.
func (b *Backend) ConfigureAgents(cfg config.AgentsConfig) {
	b.agents.ConfigureAgents(cfg)
	b.notifier.ConfigureSound(cfg.Sound)
}

// ConfigureGlance applies the shared glance configuration to this server.
func (b *Backend) ConfigureGlance(cfg config.GlanceConfig) {
	b.agents.ConfigureGlance(cfg)
}

// Glance implements the user-bound `nexo tmux glance` command.
func (b *Backend) Glance(ctx context.Context, action capabilities.GlanceAction) error {
	if b == nil || b.agents == nil {
		return errors.New("tmux glance requires an agent source")
	}
	return b.agents.Glance(ctx, action)
}

// ConfigureAgentCommand selects the executable and config file used by focus
// hooks installed by the agent watcher.
func (b *Backend) ConfigureAgentCommand(binary, configPath string) {
	b.agents.ConfigureHookCommand(binary, configPath)
}

// Notify implements capabilities.Notifier for tmux desktop notifications.
func (b *Backend) Notify(ctx context.Context, message string) error {
	if b == nil || b.notifier == nil {
		return errors.New("tmux notifier is unavailable")
	}
	return b.notifier.Notify(ctx, message)
}

// ListAgents implements capabilities.AgentSource.
func (b *Backend) ListAgents(ctx context.Context) ([]capabilities.Agent, error) {
	return b.agents.ListAgents(ctx)
}

// MarkAgentSeen records focus entering one exact stopped pane.
func (b *Backend) MarkAgentSeen(ctx context.Context, paneID string) error {
	return b.agents.MarkSeen(ctx, paneID)
}

// WatchAgents runs the tmux agent poller until ctx is canceled.
func (b *Backend) WatchAgents(ctx context.Context) error {
	return b.agents.Watch(ctx)
}

// DisplayMessage shows message on the attached client's status line. tmux
// expands display-message text as a format, so literal '#' is escaped.
func (b *Backend) DisplayMessage(ctx context.Context, message string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux message display requires a command runner")
	}
	_, err := b.runClient(ctx, "display-message", strings.ReplaceAll(message, "#", "##"))
	return err
}

var _ capabilities.MessageDisplayer = (*Backend)(nil)

// FocusPane switches to the exact tmux pane target. Each operation receives
// the pane ID so tmux resolves its owning container and window before the
// final pane selection.
func (b *Backend) FocusPane(ctx context.Context, paneID string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux pane focuser requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return errors.New("tmux pane focuser requires a pane id")
	}
	if b.Inside {
		if _, err := b.runClient(ctx, "switch-client", "-t", paneID); err != nil {
			return err
		}
		for _, operation := range []string{"select-window", "select-pane"} {
			if _, err := b.run(ctx, operation, "-t", paneID); err != nil {
				return err
			}
		}
		if err := b.MarkAgentSeen(ctx, paneID); err != nil {
			return err
		}
		return nil
	}
	for _, operation := range []string{"select-window", "select-pane"} {
		if _, err := b.run(ctx, operation, "-t", paneID); err != nil {
			return err
		}
	}
	if err := b.MarkAgentSeen(ctx, paneID); err != nil {
		return err
	}
	return b.runInteractive(ctx, "attach-session", "-t", paneID)
}

var _ capabilities.AgentSource = (*Backend)(nil)
var _ capabilities.FocusEventRecorder = (*Backend)(nil)
var _ capabilities.PaneFocuser = (*Backend)(nil)

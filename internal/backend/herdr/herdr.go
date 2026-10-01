// Package herdr contains the Herdr backend adapter.
package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/git"
	"github.com/vieitesss/nexo/internal/lifecycle"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/proxyidentity"
	"github.com/vieitesss/nexo/internal/runner"
)

// Backend is the Herdr backend. Capabilities are added independently as each
// feature is implemented.
type Backend struct {
	Runner runner.CommandRunner
	agents *Source

	proxyConfigPath string
	remoteMachines  proxyidentity.MachineSet
	proxyPolicy     proxyidentity.Policy
	usageStateDir   string
}

// New constructs a Herdr backend using the supplied command runner.
func New(r runner.CommandRunner) *Backend {
	return &Backend{Runner: r, agents: NewAgentSource(r), remoteMachines: proxyidentity.NewMachineSet(nil), proxyPolicy: proxyidentity.NewPolicy(nil)}
}

// OpenContainer focuses an existing Herdr workspace by stable workspace ID.
func (b *Backend) OpenContainer(ctx context.Context, id string) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr container opener requires a command runner")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("herdr container opener requires an id")
	}
	_, err := b.run(ctx, "workspace", "focus", id)
	if err != nil {
		return err
	}
	return b.recordWorkspace(ctx, id)
}

// ListAgents implements capabilities.AgentSource with Herdr's native agent
// awareness. Recording focus is owned by the plugin event hook, not listing.
func (b *Backend) ListAgents(ctx context.Context) ([]capabilities.Agent, error) {
	if b == nil || b.agents == nil {
		return nil, errors.New("herdr agents requires a command runner")
	}
	return b.agents.ListAgents(ctx)
}

// FocusPane focuses the exact agent pane ID through Herdr's agent command.
// Herdr accepts pane IDs as agent targets and marks the pane seen as part of
// the native focus operation.
func (b *Backend) FocusPane(ctx context.Context, paneID string) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr pane focuser requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return errors.New("herdr pane focuser requires a pane id")
	}
	_, err := b.run(ctx, "agent", "focus", paneID)
	return err
}

// ProjectActive performs a read-only workspace inspection. Herdr workspace
// metadata must include a project path for every live workspace; missing path
// metadata is an unknown safety state, not proof that the project is unused.
func (b *Backend) ProjectActive(ctx context.Context, path string) (bool, error) {
	if b == nil || b.Runner == nil {
		return false, errors.New("herdr active-project check requires a command runner")
	}
	output, err := b.run(ctx, "workspace", "list")
	if err != nil {
		return false, err
	}
	workspaces, err := decodeWorkspaceList(output)
	if err != nil {
		return false, err
	}
	path = filepath.Clean(path)
	for _, workspace := range workspaces {
		workspacePath := workspace.ProjectPath()
		if workspacePath == "" {
			return false, fmt.Errorf("workspace %q has no project path", workspace.ID)
		}
		if herdrPathContains(path, workspacePath) {
			return true, nil
		}
	}
	return false, nil
}

func herdrPathContains(project, candidate string) bool {
	project, candidate = filepath.Clean(project), filepath.Clean(candidate)
	if project == candidate || herdrDescendant(project, candidate) {
		return true
	}
	info, err := os.Lstat(project)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	resolvedProject, projectErr := filepath.EvalSymlinks(project)
	resolvedCandidate, candidateErr := filepath.EvalSymlinks(candidate)
	if projectErr != nil || candidateErr != nil {
		return false
	}
	return filepath.Clean(resolvedProject) == filepath.Clean(resolvedCandidate) || herdrDescendant(resolvedProject, resolvedCandidate)
}

func herdrDescendant(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

var _ capabilities.AgentSource = (*Backend)(nil)
var _ capabilities.PaneFocuser = (*Backend)(nil)
var _ capabilities.ProjectUseChecker = (*Backend)(nil)

// Error reports a failed Herdr operation and preserves Herdr's stderr.
type Error struct {
	Operation string
	Args      []string
	Stderr    string
	Err       error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("herdr %s: %s", e.Operation, strings.TrimSpace(e.Stderr))
	}
	return fmt.Sprintf("herdr %s: %v", e.Operation, e.Err)
}

// Unwrap preserves the command runner error for errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

func (b *Backend) CreateWorktree(ctx context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	if b == nil || b.Runner == nil {
		return capabilities.Worktree{}, errors.New("herdr worktree creator requires a command runner")
	}
	return lifecycle.CreateWorktree(ctx, options, lifecycle.WorktreeAdapter{
		Create:   b.createWorktree,
		Open:     b.openProjectNative,
		Rollback: b.removeWorktree,
	})
}

func (b *Backend) ExtractWorktree(ctx context.Context, options capabilities.WorktreeExtractOptions) (capabilities.Worktree, error) {
	if b == nil || b.Runner == nil {
		return capabilities.Worktree{}, errors.New("herdr worktree extractor requires a command runner")
	}
	return lifecycle.ExtractWorktree(ctx, options, lifecycle.ExtractAdapter{
		WorktreeAdapter: lifecycle.WorktreeAdapter{
			Create:   b.createWorktree,
			Open:     b.openProjectNative,
			Rollback: b.removeWorktree,
		},
		Git: git.New(b.Runner),
	})
}

func (b *Backend) createWorktree(ctx context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	args := []string{"worktree", "create", "--cwd", options.Repo, "--branch", options.Branch}
	if options.NewBranch {
		args = append(args, "--base", "HEAD")
	}
	args = append(args, "--path", options.Destination)
	if !options.NoFocus {
		args = append(args, "--focus")
	}
	output, err := b.run(ctx, args[0], args[1:]...)
	if err != nil {
		cleanupErr := b.rollbackPartialCreate(options, workspaceID(output), ctx)
		return capabilities.Worktree{}, errors.Join(err, cleanupErr)
	}
	workspace, err := decodeWorktreeWorkspace(output)
	if err != nil {
		cleanupErr := b.rollbackPartialCreate(options, workspaceID(output), ctx)
		return capabilities.Worktree{}, errors.Join(err, cleanupErr)
	}
	name := workspace.Label
	if name == "" {
		name = projects.TargetName(options.Destination)
	}
	return capabilities.Worktree{
		Path:        options.Destination,
		Branch:      options.Branch,
		Repo:        options.Repo,
		NewBranch:   options.NewBranch,
		ContainerID: workspace.ID,
		Container:   capabilities.OpenedContainer{ID: workspace.ID, Name: name, Path: options.Destination, Created: true},
	}, nil
}

func workspaceID(output []byte) string {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(output, &envelope) != nil {
		return ""
	}
	raw := output
	if len(envelope.Result) != 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result struct {
		Workspace struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return ""
	}
	return result.Workspace.ID
}

func (b *Backend) rollbackPartialCreate(options capabilities.WorktreeCreateOptions, id string, ctx context.Context) error {
	cleanupCtx, cancel := lifecycle.CleanupContext(ctx)
	defer cancel()
	var failures []error
	if id != "" {
		if _, err := b.run(cleanupCtx, "worktree", "remove", "--workspace", id, "--force"); err != nil {
			failures = append(failures, err)
		}
	}
	if _, err := os.Lstat(options.Destination); err == nil {
		if _, err := b.Runner.Run(cleanupCtx, "git", "-C", options.Repo, "worktree", "remove", "--force", options.Destination); err != nil {
			failures = append(failures, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// RollbackExtractWorktree removes the extracted worktree and restores its
// branch in the original checkout. Cleanup is independent of cancellation
// because it is part of the data-safety transaction.
func (b *Backend) RollbackExtractWorktree(ctx context.Context, worktree capabilities.Worktree) error {
	cleanupCtx, cancel := lifecycle.CleanupContext(ctx)
	defer cancel()
	cleanupErr := b.removeWorktree(cleanupCtx, worktree)
	if worktree.Repo == "" || worktree.Branch == "" {
		return errors.Join(cleanupErr, errors.New("extracted worktree result lacks original branch identity"))
	}
	restoreErr := git.New(b.Runner).Checkout(cleanupCtx, worktree.Repo, worktree.Branch)
	return errors.Join(cleanupErr, restoreErr)
}

var _ capabilities.WorktreeExtractionRollback = (*Backend)(nil)

// RemoveWorktree removes a Herdr worktree workspace created by this invocation.
func (b *Backend) RemoveWorktree(ctx context.Context, worktree capabilities.Worktree) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr worktree remover requires a command runner")
	}
	return b.removeWorktree(ctx, worktree)
}

func (b *Backend) removeWorktree(ctx context.Context, worktree capabilities.Worktree) error {
	if worktree.ContainerID == "" {
		return errors.New("herdr worktree result does not include a workspace id")
	}
	_, err := b.run(ctx, "worktree", "remove", "--workspace", worktree.ContainerID, "--force")
	return err
}

func decodeWorktreeWorkspace(output []byte) (workspace, error) {
	if len(output) == 0 {
		return workspace{}, errors.New("herdr create returned no workspace")
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return workspace{}, fmt.Errorf("decode herdr created workspace: %w", err)
	}
	raw := output
	if len(envelope.Result) != 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result struct {
		Workspace workspace `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return workspace{}, fmt.Errorf("decode herdr created workspace: %w", err)
	}
	if result.Workspace.ID == "" {
		return workspace{}, errors.New("herdr create returned a workspace without an id")
	}
	return result.Workspace, nil
}

// OpenProject focuses an existing Herdr workspace selected by label, or
// creates a workspace for path when none is found. With NoFocus it reuses or
// creates the workspace without moving the user's view to it. Shared
// lifecycle policy normalizes the result at this module's seam.
func (b *Backend) OpenProject(ctx context.Context, path string, options capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
	container, err := lifecycle.OpenProject(ctx, path, options, b.openProjectNative)
	if err != nil {
		return container, err
	}
	if options.NoFocus || container.ID == "" {
		return container, nil
	}
	if err := b.recordWorkspace(ctx, container.ID); err != nil {
		return container, err
	}
	return container, nil
}

func (b *Backend) usageStore() containerstate.Store {
	return containerstate.New(b.usageStateDir, "herdr", localMachine(), herdrServerIdentity())
}

func (b *Backend) recordContainer(entry containerstate.Entry) error {
	if b == nil {
		return errors.New("herdr container state is unavailable")
	}
	return b.usageStore().Record(entry)
}

// RecentContainers returns the recorded identities for this Herdr socket.
func (b *Backend) RecentContainers(ctx context.Context) ([]containerstate.Entry, error) {
	if b == nil || b.Runner == nil {
		return nil, errors.New("herdr container history requires a command runner")
	}
	return b.usageStore().Entries()
}

// CurrentContainerID reports the workspace owning the calling pane.
func (b *Backend) CurrentContainerID(ctx context.Context) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("herdr container history requires a command runner")
	}
	if workspace := strings.TrimSpace(os.Getenv("HERDR_WORKSPACE_ID")); workspace != "" {
		return workspace, nil
	}
	pane := strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
	if pane == "" {
		return "", nil
	}
	output, err := b.run(ctx, "pane", "get", pane)
	if err != nil {
		return "", nil
	}
	workspace, err := decodePaneWorkspace(output)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(workspace), nil
}

// CurrentProxyMachine reports the remote machine attached by the calling pane.
func (b *Backend) CurrentProxyMachine(ctx context.Context) (string, error) {
	pane := strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
	if pane == "" {
		return "", nil
	}
	target, err := b.ProxyTarget(ctx, pane)
	if err != nil || strings.TrimSpace(target) == "" {
		return "", nil
	}
	identity, ok := proxyidentity.ParseValue(target)
	if !ok {
		return "", nil
	}
	return proxyidentity.NormalizeMachineName(identity.Machine), nil
}

// recordWorkspace classifies one workspace from authoritative metadata before
// recording it, so a proxy is never stored as a local container.
func (b *Backend) recordWorkspace(ctx context.Context, workspaceID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil
	}
	workspaces, err := b.listWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, workspace := range workspaces {
		if workspace.ID != workspaceID {
			continue
		}
		if machine, container, ok := b.proxyWorkspaceTarget(workspace); ok {
			return b.recordContainer(containerstate.Entry{ID: workspace.ID, Name: container, Machine: machine, Proxy: true})
		}
		return b.recordContainer(containerstate.Entry{ID: workspace.ID, Name: workspace.Label, Machine: localMachine()})
	}
	return nil
}

// RecordFocusEvent records the container named by a Herdr plugin focus event.
// Herdr emits workspace.focused and pane.focused events; the payload carries
// the workspace that gained focus.
func (b *Backend) RecordFocusEvent(ctx context.Context, payload []byte) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr container history requires a command runner")
	}
	workspaceID := focusEventWorkspace(payload)
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(os.Getenv("HERDR_WORKSPACE_ID"))
	}
	if workspaceID == "" {
		return nil
	}
	return b.recordWorkspace(ctx, workspaceID)
}

// focusEventWorkspace extracts the workspace id from a Herdr event payload,
// accepting both the event data itself and a wrapper with an event field.
func focusEventWorkspace(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var event struct {
		WorkspaceID string          `json:"workspace_id"`
		Event       json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return ""
	}
	if len(event.Event) != 0 && string(event.Event) != "null" {
		return focusEventWorkspace(event.Event)
	}
	return strings.TrimSpace(event.WorkspaceID)
}

func (b *Backend) openProjectNative(ctx context.Context, path string, options capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
	if b == nil || b.Runner == nil {
		return capabilities.OpenedContainer{}, errors.New("herdr project opener requires a command runner")
	}
	if options.ContainerID != "" {
		return b.openWorkspaceByID(ctx, path, options)
	}

	output, err := b.run(ctx, "workspace", "list")
	if err != nil {
		return capabilities.OpenedContainer{}, err
	}
	workspaces, err := decodeWorkspaceList(output)
	if err != nil {
		return capabilities.OpenedContainer{}, err
	}

	target := projects.TargetName(path)
	directory := filepath.Base(filepath.Clean(path))
	if workspace, ok := matchWorkspace(workspaces, target, directory); ok {
		if !options.NoFocus {
			if _, err := b.run(ctx, "workspace", "focus", workspace.ID); err != nil {
				return capabilities.OpenedContainer{}, err
			}
		}
		return capabilities.OpenedContainer{ID: workspace.ID, Name: workspace.Label, Path: path}, nil
	}

	args := []string{"workspace", "create", "--cwd", path, "--label", target}
	if !options.NoFocus {
		args = append(args, "--focus")
	}
	createdOutput, err := b.run(ctx, args[0], args[1:]...)
	if err != nil {
		return capabilities.OpenedContainer{}, err
	}
	created, err := decodeWorktreeWorkspace(createdOutput)
	if err != nil {
		return capabilities.OpenedContainer{}, err
	}
	name := created.Label
	if name == "" {
		name = target
	}
	return capabilities.OpenedContainer{ID: created.ID, Name: name, Path: path, Created: true}, nil
}

// openWorkspaceByID resolves the workspace label from the workspace list so the
// reported container is complete whether or not the workspace is focused.
func (b *Backend) openWorkspaceByID(ctx context.Context, path string, options capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
	output, err := b.run(ctx, "workspace", "list")
	if err != nil {
		return capabilities.OpenedContainer{}, err
	}
	workspaces, err := decodeWorkspaceList(output)
	if err != nil {
		return capabilities.OpenedContainer{}, err
	}
	for _, workspace := range workspaces {
		if workspace.ID != options.ContainerID {
			continue
		}
		if !options.NoFocus {
			if _, err := b.run(ctx, "workspace", "focus", options.ContainerID); err != nil {
				return capabilities.OpenedContainer{}, err
			}
		}
		return capabilities.OpenedContainer{ID: workspace.ID, Name: workspace.Label, Path: path}, nil
	}
	return capabilities.OpenedContainer{}, fmt.Errorf("herdr workspace %q not found", options.ContainerID)
}

// matchWorkspace prefers an exact label match and falls back to a workspace
// whose label contains the project directory name.
func matchWorkspace(workspaces []workspace, target, directory string) (workspace, bool) {
	for _, workspace := range workspaces {
		if workspace.Label == target {
			return workspace, true
		}
	}
	for _, workspace := range workspaces {
		if strings.Contains(workspace.Label, directory) {
			return workspace, true
		}
	}
	return workspace{}, false
}

type workspace struct {
	ID          string            `json:"workspace_id"`
	Label       string            `json:"label"`
	Tokens      map[string]string `json:"tokens"`
	Focused     bool              `json:"focused"`
	ActiveTabID string            `json:"active_tab_id"`
	PaneCount   int               `json:"pane_count"`
	Path        string            `json:"path"`
	CWD         string            `json:"cwd"`
	Root        string            `json:"root"`
	Worktree    string            `json:"worktree_path"`
	Panes       []workspacePane   `json:"panes"`
	Tabs        []workspaceTab    `json:"tabs"`
}

type workspaceTab struct {
	Panes []workspacePane `json:"panes"`
}

type workspacePane struct {
	Path             string `json:"path"`
	CWD              string `json:"cwd"`
	WorkingDirectory string `json:"working_directory"`
}

func (w workspace) ProjectPath() string {
	for _, value := range []string{w.Path, w.CWD, w.Root, w.Worktree} {
		if value != "" {
			return value
		}
	}
	for _, pane := range append(append([]workspacePane(nil), w.Panes...), panesFromTabs(w.Tabs)...) {
		for _, value := range []string{pane.Path, pane.CWD, pane.WorkingDirectory} {
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func panesFromTabs(tabs []workspaceTab) []workspacePane {
	var panes []workspacePane
	for _, tab := range tabs {
		panes = append(panes, tab.Panes...)
	}
	return panes
}

// decodeWorkspaceList accepts the JSON envelope emitted by the Herdr CLI and
// the result object directly, which keeps the command seam easy to fake.
func decodeWorkspaceList(output []byte) ([]workspace, error) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, fmt.Errorf("decode herdr workspace list: %w", err)
	}
	raw := output
	if len(envelope.Result) != 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result struct {
		Type       string      `json:"type"`
		Workspaces []workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode herdr workspace list: %w", err)
	}
	if result.Type != "" && result.Type != "workspace_list" {
		return nil, fmt.Errorf("decode herdr workspace list: unexpected result type %q", result.Type)
	}
	return result.Workspaces, nil
}

func (b *Backend) run(ctx context.Context, operation string, args ...string) ([]byte, error) {
	output, err := b.Runner.Run(ctx, "herdr", append([]string{operation}, args...)...)
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
	return nil, &Error{
		Operation: operation,
		Args:      append([]string(nil), args...),
		Stderr:    stderr,
		Err:       err,
	}
}

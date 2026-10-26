package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/lifecycle"
	"github.com/vieitesss/nexo/internal/pathpolicy"
	"github.com/vieitesss/nexo/internal/projects"
)

var (
	_ capabilities.ProjectOpener              = (*Backend)(nil)
	_ capabilities.ProjectUseChecker          = (*Backend)(nil)
	_ capabilities.WorktreeCreator            = (*Backend)(nil)
	_ capabilities.WorktreeExtractor          = (*Backend)(nil)
	_ capabilities.WorktreeExtractionRollback = (*Backend)(nil)
	_ capabilities.WorktreeRemover            = (*Backend)(nil)
)

// shellFlavor is the Rex block flavor used for a project terminal.
const shellFlavor = terminalFlavor + ".shell"

// OpenProject reuses the session nexo opened or adopted for a project, or
// creates one. The shared lifecycle applies the project path preflight and the
// result contract; everything native stays here.
func (b *Backend) OpenProject(ctx context.Context, path string, options capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
	if b == nil || b.Runner == nil {
		return capabilities.OpenedContainer{}, errors.New("rex project opener requires a command runner")
	}
	return lifecycle.OpenProject(ctx, path, options, b.openProjectNative)
}

// openProjectNative resolves a project to one session. Explicit ids are
// honoured as given; automation reuses the recorded session or the single
// session actually working in the project, and creates only when no live
// session qualifies.
func (b *Backend) openProjectNative(ctx context.Context, path string, options capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
	root := filepath.Clean(path)
	if strings.TrimSpace(options.ContainerID) != "" {
		session, err := b.findSession(ctx, options.ContainerID)
		if err != nil {
			return capabilities.OpenedContainer{}, err
		}
		return b.finishOpen(ctx, session, path, false, options, nil)
	}

	sessions, err := b.sessions(ctx)
	if err != nil {
		return capabilities.OpenedContainer{}, err
	}
	index, err := b.projectIndex(ctx)
	if err != nil {
		return capabilities.OpenedContainer{}, err
	}
	if session, ok, err := b.recordedSession(index, sessions, root); err != nil {
		return capabilities.OpenedContainer{}, err
	} else if ok {
		return b.finishOpen(ctx, session, path, false, options, nil)
	}

	candidates, err := b.sessionsUsing(ctx, path, sessions)
	if err != nil {
		return capabilities.OpenedContainer{}, err
	}
	if session, ok, err := b.recentCandidate(ctx, candidates); err != nil {
		return capabilities.OpenedContainer{}, err
	} else if ok {
		return b.finishOpen(ctx, session, path, false, options, func() error { return index.record(root, session.ID) })
	}

	created, err := b.createSession(ctx, path)
	if err != nil {
		return capabilities.OpenedContainer{}, err
	}
	return b.finishOpen(ctx, created, path, true, options, func() error { return index.record(root, created.ID) })
}

// finishOpen focuses a resolved session and then applies its durable project
// record. The container is built from the native identity before either step,
// so a failure still carries the session the shared lifecycle must destroy; the
// record is written last, so it never points at a session the open did not
// finish.
func (b *Backend) finishOpen(ctx context.Context, session sessionInfo, path string, created bool, options capabilities.OpenOptions, record func() error) (capabilities.OpenedContainer, error) {
	container := capabilities.OpenedContainer{ID: session.ID, Name: session.Label, Path: path, Created: created}
	if err := b.focusAndRecord(ctx, session, options.NoFocus); err != nil {
		return container, err
	}
	if record != nil {
		if err := record(); err != nil {
			return container, err
		}
	}
	return container, nil
}

// recordedSession reuses a recorded session only while it still exists. A dead
// record is stale state, not a reason to fail the open.
func (b *Backend) recordedSession(index projectIndex, sessions []sessionInfo, root string) (sessionInfo, bool, error) {
	sessionID, ok, err := index.lookup(root)
	if err != nil || !ok {
		return sessionInfo{}, false, err
	}
	for _, session := range sessions {
		if session.ID == sessionID {
			return session, true, nil
		}
	}
	return sessionInfo{}, false, nil
}

// sessionsUsing finds the sessions whose live blocks work inside the project.
// A label is mutable presentation and never counts as evidence.
func (b *Backend) sessionsUsing(ctx context.Context, path string, sessions []sessionInfo) ([]sessionInfo, error) {
	resolved, err := b.server(ctx)
	if err != nil {
		return nil, err
	}
	link := isSymlink(path)
	var matching []sessionInfo
	for _, session := range sessions {
		blocks, err := b.blocks(ctx, session.ID)
		if err != nil {
			return nil, err
		}
		if b.sessionUses(ctx, resolved, session.ID, blocks, path, link) {
			matching = append(matching, session)
		}
	}
	return matching, nil
}

func (b *Backend) sessionUses(ctx context.Context, server identity, sessionID string, blocks []block, path string, link bool) bool {
	for _, block := range blocks {
		if !terminalBlock(block) || !localBlock(block, server) {
			continue
		}
		paths, err := b.blockPaths(ctx, sessionID, block)
		if err != nil {
			continue
		}
		for _, candidate := range paths {
			if pathpolicy.Contains(path, candidate, link, pathpolicy.OSResolver{}) {
				return true
			}
		}
	}
	return false
}

// recentCandidate narrows several candidates to the one the user touched most
// recently. None in history means the user never saw any of them, so creating a
// session is more honest than guessing.
func (b *Backend) recentCandidate(ctx context.Context, candidates []sessionInfo) (sessionInfo, bool, error) {
	switch len(candidates) {
	case 0:
		return sessionInfo{}, false, nil
	case 1:
		return candidates[0], true, nil
	}
	history, err := b.RecentContainers(ctx)
	if err != nil {
		return sessionInfo{}, false, err
	}
	for _, entry := range history {
		for _, candidate := range candidates {
			if candidate.ID == entry.ID {
				return candidate, true, nil
			}
		}
	}
	return sessionInfo{}, false, nil
}

func (b *Backend) createSession(ctx context.Context, path string) (sessionInfo, error) {
	if err := validateProjectDirectory(path); err != nil {
		return sessionInfo{}, err
	}
	label := truncateLabel(projects.TargetName(path))
	output, err := b.api(ctx, "session.create", createInput{
		Label: label,
		InitialWindows: []createWindow{{
			Layout: createLayout{Block: createBlock{
				Flavor:  shellFlavor,
				Options: createOptions{CWD: path},
			}},
		}},
	})
	if err != nil {
		return sessionInfo{}, err
	}
	var created createResult
	if err := json.Unmarshal(output, &created); err != nil {
		return sessionInfo{}, fmt.Errorf("decode rex session.create: %w", err)
	}
	if strings.TrimSpace(created.SessionID) == "" {
		return sessionInfo{}, errors.New("rex session.create returned no session id")
	}
	return sessionInfo{ID: created.SessionID, Label: label}, nil
}

// validateProjectDirectory refuses a project path Rex cannot use as a working
// directory. Rex silently falls back to the home directory when cwd is not a
// directory, so a vanished checkout must fail here instead of opening a shell
// in the wrong place while nexo reports the project root.
func validateProjectDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("project path %s is not usable: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project path %s is not a directory", path)
	}
	return nil
}

// truncateLabel keeps a generated project name inside Rex's stored label.
func truncateLabel(label string) string {
	runes := []rune(label)
	if len(runes) <= labelLimit {
		return label
	}
	return string(runes[:labelLimit])
}

// ProjectActive reports whether any live block is working in the project.
// Unknown block state is an error: an unreadable working directory is not
// evidence that a project is unused.
func (b *Backend) ProjectActive(ctx context.Context, path string) (bool, error) {
	if b == nil || b.Runner == nil {
		return false, errors.New("rex active-project check requires a command runner")
	}
	path = filepath.Clean(path)
	resolved, err := b.server(ctx)
	if err != nil {
		return false, err
	}
	sessions, err := b.sessions(ctx)
	if err != nil {
		return false, err
	}
	link := isSymlink(path)
	for _, session := range sessions {
		blocks, err := b.blocks(ctx, session.ID)
		if err != nil {
			return false, err
		}
		for _, block := range blocks {
			if !terminalBlock(block) {
				continue
			}
			if !localBlock(block, resolved) {
				return false, fmt.Errorf("rex block %q runs on another host: cannot inspect its working directory", block.ID)
			}
			paths, err := b.blockPaths(ctx, session.ID, block)
			if err != nil {
				return false, err
			}
			if len(paths) == 0 {
				return false, fmt.Errorf("%w: %s", errUninspectableBlock, block.ID)
			}
			for _, candidate := range paths {
				if pathpolicy.Contains(path, candidate, link, pathpolicy.OSResolver{}) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

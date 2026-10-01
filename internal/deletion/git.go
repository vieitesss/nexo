package deletion

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/runner"
)

// GitInspector is the command-runner adapter for deletion preflight. A
// repository is allowed to have no remote; a configured remote that cannot be
// verified is recorded as a hard blocker.
type GitInspector struct{ Runner runner.CommandRunner }

func (g GitInspector) run(ctx context.Context, path string, args ...string) ([]byte, error) {
	if g.Runner == nil {
		return nil, errors.New("git check requires a command runner")
	}
	// Observation commands must not refresh the index or take an optional lock.
	return g.Runner.Run(ctx, "git", append([]string{"--no-optional-locks", "-C", path}, args...)...)
}

// Inspect returns Git facts. Non-Git directories are not errors. A failed
// repository check is only treated as a positive non-repository result when
// the selected directory has no .git entry; if metadata exists, the failure
// remains a hard error.
func (g GitInspector) Inspect(ctx context.Context, path string) (GitState, error) {
	if g.Runner == nil {
		return GitState{}, errors.New("git check requires a command runner")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, metadataErr := os.Lstat(filepath.Join(path, ".git"))
	gitDirOutput, err := g.run(checkCtx, path, "rev-parse", "--git-dir")
	if err != nil {
		if metadataErr != nil && errors.Is(metadataErr, os.ErrNotExist) {
			return GitState{}, nil
		}
		if metadataErr != nil {
			return GitState{}, fmt.Errorf("inspect Git metadata: %w", metadataErr)
		}
		return GitState{}, fmt.Errorf("verify repository: %w", err)
	}
	if metadataErr != nil && !errors.Is(metadataErr, os.ErrNotExist) {
		return GitState{}, fmt.Errorf("inspect Git metadata: %w", metadataErr)
	}

	state := GitState{IsRepository: true}
	commonOutput, commonErr := g.run(checkCtx, path, "rev-parse", "--git-common-dir")
	topOutput, topErr := g.run(checkCtx, path, "rev-parse", "--show-toplevel")
	if commonErr != nil || topErr != nil {
		return state, errors.Join(
			wrapGitCheck("read repository common directory", commonErr),
			wrapGitCheck("read repository top level", topErr),
		)
	}
	gitDir := absoluteGitPath(path, firstLine(gitDirOutput))
	commonDir := absoluteGitPath(path, firstLine(commonOutput))
	top := absoluteGitPath(path, firstLine(topOutput))
	if commonDir == "" || top == "" || gitDir == "" {
		return state, errors.New("repository identity is incomplete")
	}
	if isRegisteredWorktree(gitDir, commonDir) {
		state.LinkedWorktree = true
	}

	worktrees, wtErr := g.run(checkCtx, path, "worktree", "list", "--porcelain")
	if wtErr != nil {
		return state, fmt.Errorf("list worktrees: %w", wtErr)
	}
	worktreeInfo := parseWorktrees(worktrees)
	if state.LinkedWorktree {
		state.PrimaryPath = primaryFromWorktreeRegistration(worktreeInfo, top)
		if state.PrimaryPath == "" {
			return state, errors.New("linked worktree registration has no primary checkout")
		}
	}
	roots := make([]string, 0, len(worktreeInfo))
	for _, worktree := range worktreeInfo {
		roots = append(roots, worktree.Path)
		if state.LinkedWorktree && filepath.Clean(worktree.Path) == filepath.Clean(top) {
			state.WorktreeLocked = worktree.Locked
		}
	}
	if !state.LinkedWorktree {
		for _, root := range roots {
			if filepath.Clean(root) != filepath.Clean(top) {
				state.DependentWorktrees = append(state.DependentWorktrees, root)
			}
		}
	}

	status, statusErr := g.run(checkCtx, path, "status", "--porcelain=v2", "--branch", "--untracked-files=all")
	if statusErr != nil {
		return state, fmt.Errorf("read repository status: %w", statusErr)
	}
	state.Dirty, state.Untracked, _, _ = parseStatus(status)

	branches, branchErr := g.run(checkCtx, path, "for-each-ref", "--format=%(refname:short)\t%(upstream:short)", "refs/heads")
	if branchErr != nil {
		return state, fmt.Errorf("enumerate local branches: %w", branchErr)
	}
	branchRecords := parseBranches(branches)
	if len(branchRecords) == 0 {
		if _, headErr := g.run(checkCtx, path, "rev-parse", "--verify", "--quiet", "HEAD"); headErr == nil {
			state.Unpushed = true
		}
	}

	remotes, remoteErr := g.run(checkCtx, path, "remote")
	if remoteErr != nil {
		return state, fmt.Errorf("list remotes: %w", remoteErr)
	}
	remoteNames := strings.Fields(string(remotes))
	state.RemoteChecked = true
	verifiedRemoteOIDs := make(map[string]bool)
	for _, remote := range remoteNames {
		remoteOutput, checkErr := g.run(checkCtx, path, "ls-remote", remote)
		if checkErr != nil {
			state.RemoteCheckError = fmt.Sprintf("%s: %v", remote, checkErr)
			state.RemoteChecked = false
			return state, nil
		}
		for _, oid := range remoteOIDs(remoteOutput) {
			verifiedRemoteOIDs[oid] = true
		}
	}
	for _, branch := range branchRecords {
		if branch.Upstream == "" {
			if branchHasCommit(checkCtx, g, path, branch.Name) {
				state.Unpushed = true
			}
			continue
		}
		remote, remoteBranch, ok := strings.Cut(branch.Upstream, "/")
		if !ok || remote == "" || remoteBranch == "" {
			return state, fmt.Errorf("branch %q has malformed upstream %q", branch.Name, branch.Upstream)
		}
		remoteOutput, checkErr := g.run(checkCtx, path, "ls-remote", remote, "refs/heads/"+remoteBranch)
		if checkErr != nil {
			state.RemoteCheckError = fmt.Sprintf("%s/%s: %v", remote, remoteBranch, checkErr)
			state.RemoteChecked = false
			return state, nil
		}
		remoteOID := firstField(remoteOutput)
		if remoteOID == "" {
			// An empty, successful answer means the remote branch was removed.
			// Its local commits are therefore local-only data.
			state.Unpushed = true
			continue
		}
		countOutput, countErr := g.run(checkCtx, path, "rev-list", "--count", branch.Name, "--not", remoteOID)
		if countErr != nil {
			return state, fmt.Errorf("compare branch %q with verified remote: %w", branch.Name, countErr)
		}
		if count, parseErr := strconv.Atoi(strings.TrimSpace(string(countOutput))); parseErr != nil {
			return state, fmt.Errorf("parse branch %q divergence: %w", branch.Name, parseErr)
		} else if count > 0 {
			state.Unpushed = true
		}
	}

	// A detached HEAD is not represented by for-each-ref. Check it separately
	// and compare its reachable history against the remote OIDs verified above.
	// Any inability to establish that boundary is conservative local-only data.
	if detached, detachedErr := g.detachedHead(checkCtx, path); detachedErr != nil {
		return state, detachedErr
	} else if detached {
		headOID, headErr := g.run(checkCtx, path, "rev-parse", "--verify", "--quiet", "HEAD")
		if headErr != nil {
			return state, fmt.Errorf("read detached HEAD: %w", headErr)
		}
		head := firstField(headOID)
		if head == "" {
			return state, errors.New("detached HEAD identity is empty")
		}
		if len(verifiedRemoteOIDs) == 0 {
			state.Unpushed = true
		} else {
			remoteOIDs := make([]string, 0, len(verifiedRemoteOIDs))
			for remoteOID := range verifiedRemoteOIDs {
				remoteOIDs = append(remoteOIDs, remoteOID)
			}
			sort.Strings(remoteOIDs)
			args := []string{"rev-list", "--count", "HEAD", "--not"}
			args = append(args, remoteOIDs...)
			countOutput, countErr := g.run(checkCtx, path, args...)
			if countErr != nil {
				return state, fmt.Errorf("compare detached HEAD with verified remote: %w", countErr)
			}
			count, parseErr := strconv.Atoi(strings.TrimSpace(string(countOutput)))
			if parseErr != nil {
				return state, fmt.Errorf("parse detached HEAD divergence: %w", parseErr)
			}
			if count > 0 {
				state.Unpushed = true
			}
		}
	}

	state.Stashes, err = g.inspectStash(checkCtx, path, commonDir)
	if err != nil {
		return state, err
	}
	if submoduleOutput, submoduleErr := g.run(checkCtx, path, "submodule", "status", "--recursive"); submoduleErr != nil {
		return state, fmt.Errorf("inspect submodules: %w", submoduleErr)
	} else {
		state.HasSubmodules = strings.TrimSpace(string(submoduleOutput)) != ""
	}
	return state, nil
}

func (g GitInspector) inspectStash(ctx context.Context, path, commonDir string) (bool, error) {
	output, err := g.run(ctx, path, "for-each-ref", "--format=%(refname)", "refs/stash")
	if err != nil {
		return false, fmt.Errorf("inspect stash refs: %w", err)
	}
	listed := false
	for _, ref := range strings.Fields(string(output)) {
		if ref != "refs/stash" {
			return false, fmt.Errorf("inspect stash refs: unexpected ref %q", ref)
		}
		listed = true
	}

	loosePath := filepath.Join(commonDir, "refs", "stash")
	loosePresent, looseErr := stashMetadataPresent(loosePath)
	if looseErr != nil {
		return false, fmt.Errorf("inspect stash ref %q: %w", loosePath, looseErr)
	}
	if loosePresent && !listed {
		return false, fmt.Errorf("inspect stash ref %q: Git did not return the existing ref", loosePath)
	}

	packedPresent, packedErr := packedStashPresent(filepath.Join(commonDir, "packed-refs"))
	if packedErr != nil {
		return false, packedErr
	}
	if packedPresent && !listed {
		return false, errors.New("inspect packed stash ref: Git did not return the existing ref")
	}

	// A stale stash reflog still names recoverable local objects even when the
	// ref itself has been deleted. Treat it as local data rather than silently
	// classifying the repository as stash-free.
	logPresent, logErr := stashMetadataPresent(filepath.Join(commonDir, "logs", "refs", "stash"))
	if logErr != nil {
		return false, fmt.Errorf("inspect stash reflog: %w", logErr)
	}
	return listed || loosePresent || packedPresent || logPresent, nil
}

func stashMetadataPresent(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if info.IsDir() {
		return false, errors.New("stash metadata is a directory")
	}
	if _, err := os.ReadFile(path); err != nil {
		return false, err
	}
	return true, nil
}

func packedStashPresent(path string) (bool, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect packed refs: %w", err)
	}
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !validObjectID(fields[0]) {
			return false, fmt.Errorf("inspect packed refs: malformed entry %q", line)
		}
		if fields[1] == "refs/stash" {
			return true, nil
		}
	}
	return false, nil
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, digit := range value {
		if (digit < '0' || digit > '9') && (digit < 'a' || digit > 'f') && (digit < 'A' || digit > 'F') {
			return false
		}
	}
	return true
}

func wrapGitCheck(label string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", label, err)
}

type branchRecord struct {
	Name     string
	Upstream string
}

func parseBranches(output []byte) []branchRecord {
	var records []branchRecord
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		record := branchRecord{Name: fields[0]}
		if len(fields) == 2 {
			record.Upstream = fields[1]
		}
		if record.Name != "" {
			records = append(records, record)
		}
	}
	return records
}

func branchHasCommit(ctx context.Context, inspector GitInspector, path, branch string) bool {
	_, err := inspector.run(ctx, path, "rev-parse", "--verify", "--quiet", branch)
	return err == nil
}

func (g GitInspector) detachedHead(ctx context.Context, path string) (bool, error) {
	output, err := g.run(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		if strings.TrimSpace(string(output)) == "" {
			return false, errors.New("symbolic HEAD identity is empty")
		}
		return false, nil
	}
	// symbolic-ref exits non-zero for a detached HEAD. Verify that HEAD itself
	// exists so a malformed repository remains a hard inspection failure.
	if _, headErr := g.run(ctx, path, "rev-parse", "--verify", "--quiet", "HEAD"); headErr != nil {
		return false, fmt.Errorf("inspect HEAD attachment: %w", headErr)
	}
	return true, nil
}

func remoteOIDs(output []byte) []string {
	seen := make(map[string]bool)
	var result []string
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || seen[fields[0]] {
			continue
		}
		seen[fields[0]] = true
		result = append(result, fields[0])
	}
	return result
}

func isRegisteredWorktree(gitDir, commonDir string) bool {
	gitDir, commonDir = filepath.Clean(gitDir), filepath.Clean(commonDir)
	if gitDir == commonDir {
		return false
	}
	relative, err := filepath.Rel(commonDir, gitDir)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) != 2 || parts[0] != "worktrees" || parts[1] == "" {
		return false
	}
	// Git's --git-dir for a linked checkout is the registered
	// common/worktrees/<name> directory. A separate-git-dir standalone
	// checkout has gitDir == commonDir and is excluded above. The caller has
	// already verified both values through rev-parse, so no extra filesystem
	// read is needed here (and fakes can model the identity directly).
	return true
}

// primaryFromWorktreeRegistration uses Git's registered checkout paths rather
// than guessing from the common metadata directory. This also handles a
// primary checkout created with separate Git metadata.
func primaryFromWorktreeRegistration(worktrees []worktreeInfo, selected string) string {
	selected = filepath.Clean(selected)
	for _, worktree := range worktrees {
		if filepath.Clean(worktree.Path) != selected {
			return filepath.Clean(worktree.Path)
		}
	}
	return ""
}

type worktreeInfo struct {
	Path   string
	Locked bool
}

func parseWorktrees(output []byte) []worktreeInfo {
	var result []worktreeInfo
	var current *worktreeInfo
	flush := func() {
		if current != nil && current.Path != "" {
			result = append(result, *current)
		}
		current = nil
	}
	for _, raw := range strings.Split(string(output), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &worktreeInfo{Path: filepath.Clean(strings.TrimPrefix(line, "worktree "))}
		case strings.HasPrefix(line, "locked") && current != nil:
			current.Locked = true
		case line == "":
			flush()
		}
	}
	flush()
	return result
}

func firstLine(output []byte) string {
	line := string(output)
	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}
	return strings.TrimSuffix(line, "\r")
}

func firstField(output []byte) string {
	line := firstLine(output)
	if index := strings.IndexAny(line, " \t"); index >= 0 {
		return line[:index]
	}
	return line
}

func absoluteGitPath(base, value string) string {
	if value == "" {
		return ""
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	if absolute, err := filepath.Abs(value); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(value)
}

func parseStatus(output []byte) (dirty, untracked, unpushed, hasUpstream bool) {
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "# branch.upstream ") {
			hasUpstream = true
			continue
		}
		if strings.HasPrefix(line, "# branch.ab ") {
			fields := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(fields) == 2 && strings.HasPrefix(fields[0], "+") {
				var count int
				if _, err := fmt.Sscanf(fields[0], "+%d", &count); err == nil && count > 0 {
					unpushed = true
				}
			}
			continue
		}
		if strings.HasPrefix(line, "? ") {
			untracked = true
			dirty = true
			continue
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			dirty = true
		}
	}
	return dirty, untracked, unpushed, hasUpstream
}

// RemoveWorktree deliberately omits --force. Git's refusal is a hard safety
// blocker, even when a caller requested force for other warnings.
func (g GitInspector) RemoveWorktree(ctx context.Context, repo, path string) error {
	if g.Runner == nil {
		return errors.New("worktree removal requires a Git adapter")
	}
	_, err := g.Runner.Run(ctx, "git", "-C", repo, "worktree", "remove", path)
	if err != nil {
		return fmt.Errorf("git worktree remove %q: %w", path, err)
	}
	return nil
}

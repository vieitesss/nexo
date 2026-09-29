package picker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/runner"
)

func TestGitContextLoaderReadsRealRepositoryState(t *testing.T) {
	root := t.TempDir()
	gitTestCommand(t, root, "init", "-b", "main")
	gitTestCommand(t, root, "config", "user.email", "nexo@example.test")
	gitTestCommand(t, root, "config", "user.name", "Nexo Test")
	writeGitFile(t, root, "tracked.txt", "one\n")
	gitTestCommand(t, root, "add", "tracked.txt")
	gitTestCommand(t, root, "commit", "-m", "initial")

	bare := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatalf("mkdir bare repository: %v", err)
	}
	gitTestCommand(t, bare, "init", "--bare")
	gitTestCommand(t, root, "remote", "add", "origin", bare)
	gitTestCommand(t, root, "push", "-u", "origin", "main")

	// Publish another commit and fetch only while setting up the local refs.
	publisher := filepath.Join(t.TempDir(), "publisher")
	gitTestCommand(t, "", "clone", bare, publisher)
	gitTestCommand(t, publisher, "config", "user.email", "nexo@example.test")
	gitTestCommand(t, publisher, "config", "user.name", "Nexo Test")
	writeGitFile(t, publisher, "remote.txt", "remote\n")
	gitTestCommand(t, publisher, "add", "remote.txt")
	gitTestCommand(t, publisher, "commit", "-m", "remote")
	gitTestCommand(t, publisher, "push", "origin", "main")
	gitTestCommand(t, root, "fetch", "origin")

	loader := NewGitContextLoader(runner.ExecRunner{})
	got, err := loader.Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Identity != "main" || got.Behind != 1 || got.Ahead != 0 || got.Dirty {
		t.Fatalf("clean behind context = %#v, want main behind=1", got)
	}

	// Add a local commit after the remote commit to exercise ahead and a
	// working-tree diff against HEAD in one checkout.
	writeGitFile(t, root, "tracked.txt", "one\ntwo\n")
	gitTestCommand(t, root, "add", "tracked.txt")
	gitTestCommand(t, root, "commit", "-m", "local")
	writeGitFile(t, root, "tracked.txt", "one\ntwo\nthree\n")
	got, err = loader.Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load() after local commit error = %v", err)
	}
	if got.Identity != "main" || got.Ahead != 1 || got.Behind != 1 || got.Adds != 1 || got.Deletes != 0 || !got.Dirty {
		t.Fatalf("dirty ahead context = %#v, want main +1 ↓1 ↑1", got)
	}
	if got.Decoration().Text != "main +1 -0 ↓1 ↑1" {
		t.Fatalf("decoration = %q, want compact Git context", got.Decoration().Text)
	}
}

func TestGitContextLoaderUsesShortSHAWhenDetached(t *testing.T) {
	root := setupGitRepo(t)
	head := strings.TrimSpace(gitTestCommand(t, root, "rev-parse", "HEAD"))
	gitTestCommand(t, root, "checkout", "--detach", "HEAD")
	got, err := NewGitContextLoader(runner.ExecRunner{}).Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Identity != head[:7] || got.Identity == "main" {
		t.Fatalf("identity = %q, want detached short SHA %q", got.Identity, head[:7])
	}
}

func TestGitContextLoaderResolvesLinkedWorktreePrimary(t *testing.T) {
	root := setupGitRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	gitTestCommand(t, root, "worktree", "add", "-b", "linked", linked)
	got, err := NewGitContextLoader(runner.ExecRunner{}).Load(context.Background(), linked)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if filepath.Clean(got.PrimaryPath) != filepath.Clean(root) {
		t.Fatalf("primary path = %q, want %q", got.PrimaryPath, root)
	}
	if filepath.Base(got.CommonDir) != ".git" {
		t.Fatalf("common dir = %q, want primary .git", got.CommonDir)
	}

	model := NewModel([]Place{
		{Kind: ProjectKind, Group: "/projects", Path: root, Git: true},
		{Kind: ProjectKind, Group: "/projects", Path: linked, Git: true},
	}, Theme{})
	updated, _ := model.Update(GitContextMsg{Path: linked, Context: got})
	model = updated.(Model)
	if rows := model.Rows(); len(rows) != 1 {
		// The group starts folded; expansion below checks the actual nested
		// order and depth without coupling to Git command output.
		t.Fatalf("rows = %#v, want folded group", rows)
	}
	updated, _ = model.Update(teaRightKey())
	model = updated.(Model)
	rows := model.Rows()
	if len(rows) != 3 || rows[2].Place.Path != linked || rows[2].Depth != 2 {
		t.Fatalf("nested rows = %#v, want linked child under primary", rows)
	}
	if !strings.Contains(model.View(), "│ └") {
		t.Fatalf("view = %q, want nested tree spine", model.View())
	}
}

func TestGitContextLoaderFailureDoesNotInventContext(t *testing.T) {
	loader := NewGitContextLoader(runner.Func(func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("git unavailable")
	}))
	if got, err := loader.Load(context.Background(), "/not-a-repository"); err == nil || got.Identity != "" {
		t.Fatalf("Load() = %#v, %v; want no context on failure", got, err)
	}
}

func TestGitContextLoaderDoesNotFetch(t *testing.T) {
	root := setupGitRepo(t)
	var mu sync.Mutex
	var calls []string
	recorder := runner.Func(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		mu.Lock()
		calls = append(calls, strings.Join(append([]string{name}, args...), " "))
		mu.Unlock()
		return runner.ExecRunner{}.Run(ctx, name, args...)
	})
	if _, err := NewGitContextLoader(recorder).Load(context.Background(), root); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, call := range calls {
		if strings.Contains(call, " fetch ") {
			t.Fatalf("Git call = %q, must never fetch", call)
		}
	}
}

func TestGitContextCommandsBoundConcurrencyAndTimeout(t *testing.T) {
	loader := &blockingGitLoader{delay: 100 * time.Millisecond}
	places := make([]Place, 0, 6)
	for index := 0; index < 6; index++ {
		places = append(places, Place{Path: fmt.Sprintf("repo-%d", index), Git: true})
	}
	model := NewModel(places, Theme{}, loader)
	if model.gitPool == nil || cap(model.gitPool.slots) != 4 {
		t.Fatalf("git pool = %#v, want default bounded concurrency", model.gitPool)
	}
	// Input is handled synchronously even while a repository command is slow;
	// the command itself is returned for Bubble Tea's asynchronous runner.
	started := time.Now()
	updated, cmd := model.Update(teaRuneKey('x'))
	if cmd != nil || updated.(Model).Query() != "x" || time.Since(started) > 50*time.Millisecond {
		t.Fatalf("Update() blocked on Git: cmd=%v model=%#v", cmd, updated)
	}
}

type blockingGitLoader struct {
	delay time.Duration
}

func (l *blockingGitLoader) Load(ctx context.Context, _ string) (GitContext, error) {
	select {
	case <-time.After(l.delay):
		return GitContext{Identity: "main"}, nil
	case <-ctx.Done():
		return GitContext{}, ctx.Err()
	}
}

func setupGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitTestCommand(t, root, "init", "-b", "main")
	gitTestCommand(t, root, "config", "user.email", "nexo@example.test")
	gitTestCommand(t, root, "config", "user.name", "Nexo Test")
	writeGitFile(t, root, "file.txt", "one\n")
	gitTestCommand(t, root, "add", "file.txt")
	gitTestCommand(t, root, "commit", "-m", "initial")
	return root
}

func writeGitFile(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func gitTestCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	if dir != "" {
		command.Dir = dir
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

// Keeping key construction here avoids tests depending on the model's private
// key map while using the same public Bubble Tea message shape.
func teaRightKey() tea.KeyMsg      { return tea.KeyMsg{Type: tea.KeyRight} }
func teaRuneKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

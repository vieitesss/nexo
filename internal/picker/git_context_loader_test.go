package picker

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitContextLoaderHandlesDetachedWorktreeAndFailures(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	gitRunner := funcRunner(func(_ context.Context, command string, args ...string) ([]byte, error) {
		call := strings.Join(append([]string{command}, args...), " ")
		switch call {
		case "git -C " + worktree + " status --porcelain=v2 --branch":
			return []byte("# branch.oid 0123456789abcdef\n1 .M N... 100644 100644 100644 a b file\n"), nil
		case "git -C " + worktree + " diff --numstat HEAD --":
			return []byte("2\t4\tfile\n"), nil
		case "git -C " + worktree + " rev-parse --git-common-dir":
			return []byte(filepath.Join(root, "repo", ".git") + "\n"), nil
		case "git -C " + worktree + " rev-parse --show-toplevel":
			return []byte(worktree + "\n"), nil
		default:
			return nil, errors.New("unexpected git command: " + call)
		}
	})
	value, err := NewGitContextLoader(gitRunner).Load(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	if value.Identity != "0123456" || value.Adds != 2 || value.Deletes != 4 || value.PrimaryPath != filepath.Join(root, "repo") {
		t.Fatalf("detached worktree context = %#v", value)
	}

	failed := funcRunner(func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("denied") })
	if _, err := NewGitContextLoader(failed).Load(context.Background(), worktree); err == nil {
		t.Fatal("failed Git status returned nil error")
	}
}

func TestGitContextLoaderHonorsParentCancellationAndTimeout(t *testing.T) {
	started := make(chan struct{})
	blockedRunner := funcRunner(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	loader := NewGitContextLoader(blockedRunner)
	loader.PerRepoTimeout = 10 * time.Millisecond
	_, err := loader.Load(context.Background(), "/repo")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	<-started
}

func TestGitContextLoaderUsesOnlyLocalGitCommands(t *testing.T) {
	var calls []string
	r := funcRunner(func(_ context.Context, command string, args ...string) ([]byte, error) {
		call := strings.Join(append([]string{command}, args...), " ")
		calls = append(calls, call)
		switch call {
		case "git -C /repo status --porcelain=v2 --branch":
			return []byte("# branch.head main\n# branch.ab +1 -2\n1 .M N... 100644 100644 100644 a b file\n"), nil
		case "git -C /repo diff --numstat HEAD --":
			return []byte("3\t1\tfile\n"), nil
		case "git -C /repo rev-parse --git-common-dir":
			return []byte(".git\n"), nil
		case "git -C /repo rev-parse --show-toplevel":
			return []byte("/repo\n"), nil
		default:
			return nil, nil
		}
	})
	value, err := NewGitContextLoader(r).Load(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if value.Identity != "main" || value.Adds != 3 || value.Deletes != 1 || value.Ahead != 1 || value.Behind != 2 {
		t.Fatalf("context = %#v", value)
	}
	for _, call := range calls {
		if strings.Contains(call, "fetch") {
			t.Fatalf("unexpected fetch command %q", call)
		}
	}
}

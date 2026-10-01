package deletion

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vieitesss/nexo/internal/runner"
)

func TestGitInspectorUsesRegisteredPrimaryForSeparateMetadataWorktree(t *testing.T) {
	setGitTestEnv(t)
	root := t.TempDir()
	metadata := filepath.Join(root, "metadata.git")
	primary := filepath.Join(root, "primary")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, primary, "init", "--separate-git-dir", metadata, "-q")
	gitTestCommand(t, primary, "config", "user.email", "test@example.invalid")
	gitTestCommand(t, primary, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(primary, "tracked"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, primary, "add", "tracked")
	gitTestCommand(t, primary, "commit", "-qm", "initial")
	linked := filepath.Join(root, "linked")
	gitTestCommand(t, primary, "worktree", "add", "-q", "-b", "linked", linked)

	state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LinkedWorktree || state.PrimaryPath != metadata {
		t.Fatalf("state = %#v, want registered primary %q", state, metadata)
	}
}

func gitTestCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func setGitTestEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
}

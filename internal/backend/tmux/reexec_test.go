package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/config"
	"golang.org/x/sys/unix"
)

// executableFile writes an executable file and returns its path.
func executableFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// replaceFile atomically replaces path with a fresh inode, the way an upgrade
// installs a new binary.
func replaceFile(t *testing.T, path, contents string) {
	t.Helper()
	replacement := path + ".new"
	if err := os.WriteFile(replacement, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	// Pin the old inode for the rest of the test. Linux compares dev+inode
	// against the running image, and filesystems may otherwise reuse a freed
	// inode for the next replacement.
	if old, err := os.Open(path); err == nil {
		t.Cleanup(func() { _ = old.Close() })
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
}

func capturedWatch(t *testing.T, path string) *binaryWatch {
	t.Helper()
	watch := newBinaryWatch()
	watch.launch = func() (string, error) { return path, nil }
	watch.executable = func() (string, error) { return path, nil }
	watch.runningImage = func() (os.FileInfo, error) { return os.Stat(path) }
	// Most change-detection tests use placeholder files rather than runnable
	// nexo binaries; preflight is covered separately below.
	watch.preflight = func(context.Context, string) error { return nil }
	watch.capture()
	if !watch.started {
		t.Fatalf("capture() did not record %s", path)
	}
	return watch
}

func TestBinaryWatchIgnoresUnchangedExecutable(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)
	for i := 0; i < 3; i++ {
		if got := watch.replaced(); got != "" {
			t.Fatalf("replaced() = %q on unchanged binary, want no exec", got)
		}
	}
}

func TestBinaryWatchReexecsAfterTwoConsecutivePolls(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)
	replaceFile(t, path, "new binary with different size")

	if got := watch.replaced(); got != "" {
		t.Fatalf("replaced() = %q on first poll, want debounce", got)
	}
	if got := watch.replaced(); got != path {
		t.Fatalf("replaced() = %q on second poll, want %q", got, path)
	}
}

func TestBinaryWatchResetsCandidateWhenReplacementChangesAgain(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)

	replaceFile(t, path, "candidate one")
	if got := watch.replaced(); got != "" {
		t.Fatalf("replaced() = %q for first candidate, want debounce", got)
	}
	replaceFile(t, path, "candidate two!")
	if got := watch.replaced(); got != "" {
		t.Fatalf("replaced() = %q when candidate changed, want debounce reset", got)
	}
	if got := watch.replaced(); got != path {
		t.Fatalf("replaced() = %q after stable candidate, want %q", got, path)
	}
}

func swapSymlink(t *testing.T, path, target string) {
	t.Helper()
	temporary := path + ".new"
	if err := os.Symlink(target, temporary); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func TestBinaryWatchDetectsInstallSymlinkSwap(t *testing.T) {
	dir := t.TempDir()
	oldPath := executableFile(t, dir, "nexo-old", "old binary")
	newPath := executableFile(t, dir, "nexo-new", "new binary")
	installPath := filepath.Join(dir, "nexo")
	if err := os.Symlink(oldPath, installPath); err != nil {
		t.Fatal(err)
	}

	watch := newBinaryWatch()
	watch.launch = func() (string, error) { return installPath, nil }
	watch.runningImage = func() (os.FileInfo, error) { return os.Stat(oldPath) }
	watch.preflight = func(context.Context, string) error { return nil }
	watch.capture()
	swapSymlink(t, installPath, newPath)

	if got := watch.replaced(); got != "" {
		t.Fatalf("replaced() = %q on first symlink-swap poll, want debounce", got)
	}
	if got := watch.replaced(); got != installPath {
		t.Fatalf("replaced() = %q on second symlink-swap poll, want unresolved %q", got, installPath)
	}
	oldContents, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(oldContents) != "old binary" {
		t.Fatalf("old target changed to %q during symlink swap", oldContents)
	}
}

func TestBinaryWatchPreservesSymlinkedInstallParent(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "install")
	if err := os.Symlink(realDir, parent); err != nil {
		t.Fatal(err)
	}
	oldPath := executableFile(t, realDir, "nexo-old", "old binary")
	newPath := executableFile(t, realDir, "nexo-new", "new binary")
	installPath := filepath.Join(parent, "nexo")
	if err := os.Symlink(oldPath, installPath); err != nil {
		t.Fatal(err)
	}

	watch := newBinaryWatch()
	watch.launch = func() (string, error) { return installPath, nil }
	watch.runningImage = func() (os.FileInfo, error) { return os.Stat(oldPath) }
	watch.preflight = func(context.Context, string) error { return nil }
	watch.capture()
	swapSymlink(t, installPath, newPath)

	if watch.replaced() != "" {
		t.Fatal("first parent-symlink poll should only record the candidate")
	}
	if got := watch.replaced(); got != installPath {
		t.Fatalf("replaced() = %q, want original path %q", got, installPath)
	}
}

func TestBinaryWatchDetectsReplacementBeforeCapture(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("non-Linux capture baselines the launch path and cannot see this replacement")
	}
	dir := t.TempDir()
	oldPath := executableFile(t, dir, "nexo-old", "old binary")
	newPath := executableFile(t, dir, "nexo-new", "new binary")
	installPath := filepath.Join(dir, "nexo")
	if err := os.Symlink(newPath, installPath); err != nil {
		t.Fatal(err)
	}

	watch := newBinaryWatch()
	watch.launch = func() (string, error) { return installPath, nil }
	// The process is still running oldPath even though the install pathname
	// now points at newPath before the watcher gets its initial capture.
	watch.runningImage = func() (os.FileInfo, error) { return os.Stat(oldPath) }
	watch.preflight = func(context.Context, string) error { return nil }
	watch.capture()
	if !watch.started {
		t.Fatal("capture() did not record the running image")
	}
	if watch.replaced() != "" {
		t.Fatal("first replacement poll should only record the candidate")
	}
	if got := watch.replaced(); got != installPath {
		t.Fatalf("replaced() = %q, want %q", got, installPath)
	}
}

func TestBinaryWatchRetriesInitialStatFailure(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := newBinaryWatch()
	watch.launch = func() (string, error) { return path, nil }
	watch.runningImage = func() (os.FileInfo, error) { return os.Stat(path) }
	watch.preflight = func(context.Context, string) error { return nil }
	statCalls := 0
	watch.stat = func(name string) (os.FileInfo, error) {
		statCalls++
		if statCalls == 1 {
			return nil, errors.New("transient stat failure")
		}
		return os.Stat(name)
	}
	watch.capture()
	if watch.started {
		t.Fatal("capture() succeeded despite the initial stat failure")
	}
	if got := watch.replaced(); got != "" {
		t.Fatalf("replaced() = %q while retrying initial capture, want no exec", got)
	}
	if !watch.started {
		t.Fatal("replaced() did not retry the initial capture")
	}
}

func TestBinaryWatchMissingFileNeverExecsAndRetries(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)

	// Keep the original inode allocated while making the launch path
	// disappear, so restoring it cannot look like the unchanged image on
	// Linux filesystems that eagerly reuse freed inodes.
	if err := os.Rename(path, path+".missing"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if got := watch.replaced(); got != "" {
			t.Fatalf("replaced() = %q while binary is missing, want no exec", got)
		}
	}

	replaceFile(t, path, "restored new binary")
	if got := watch.replaced(); got != "" {
		t.Fatalf("replaced() = %q on first poll after restore, want debounce", got)
	}
	if got := watch.replaced(); got != path {
		t.Fatalf("replaced() = %q after restore, want %q", got, path)
	}
}

func TestBinaryWatchRejectsNonExecutableAndNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)

	replaceFile(t, path, "new binary")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if got := watch.replaced(); got != "" {
			t.Fatalf("replaced() = %q for non-executable file, want no exec", got)
		}
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := watch.replaced(); got != "" {
		t.Fatalf("replaced() = %q for a directory, want no exec", got)
	}
}

func TestBinaryWatchStableNonRunnableCandidateNeverExecs(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)
	watch.preflight = preflightBinary
	if err := os.WriteFile(path+".new", []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}

	executed := false
	watch.exec = func(string, []string, []string) error {
		executed = true
		return nil
	}
	source := NewAgentSource(nil)
	for i := 0; i < 5; i++ {
		source.reexecIfReplaced(context.Background(), watch)
	}
	if executed {
		t.Fatal("exec ran for a stable candidate rejected by --version")
	}
}

func TestBinaryWatchHalfWrittenReplacementNeverExecs(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)

	// Every poll sees a different size, as a writer mid-copy would produce.
	for _, contents := range []string{"partial", "partial write", "partial write in progress"} {
		replaceFile(t, path, contents)
		if got := watch.replaced(); got != "" {
			t.Fatalf("replaced() = %q for in-progress write %q, want no exec", got, contents)
		}
	}
}

func TestBinaryWatchSameInodeRewrite(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("new binary"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	changed := time.Unix(4_000_000_000, 123)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS == "linux" {
		// The running image and launch path deliberately share dev+inode on
		// Linux, so metadata-only changes are not a replacement signal.
		for i := 0; i < 2; i++ {
			if got := watch.replaced(); got != "" {
				t.Fatalf("replaced() = %q for same running inode on Linux", got)
			}
		}
		return
	}
	if got := watch.replaced(); got != "" {
		t.Fatalf("replaced() = %q on first same-inode rewrite poll, want debounce", got)
	}
	if got := watch.replaced(); got != path {
		t.Fatalf("replaced() = %q on second same-inode rewrite poll, want %q", got, path)
	}
}

func TestReexecIfReplacedPassesArgvEnvAndPath(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)
	replaceFile(t, path, "new binary")

	var gotPath string
	var gotArgv, gotEnv []string
	watch.exec = func(binary string, argv, env []string) error {
		gotPath, gotArgv, gotEnv = binary, argv, env
		return errors.New("exec failed")
	}

	source := NewAgentSource(nil)
	// First poll only records the candidate.
	source.reexecIfReplaced(context.Background(), watch)
	if gotPath != "" {
		t.Fatalf("exec called on debounce poll with %q", gotPath)
	}
	source.reexecIfReplaced(context.Background(), watch)
	if gotPath != path {
		t.Fatalf("exec path = %q, want %q", gotPath, path)
	}
	if len(gotArgv) != len(os.Args) || gotArgv[0] != os.Args[0] {
		t.Fatalf("exec argv = %#v, want os.Args", gotArgv)
	}
	if len(gotEnv) != len(os.Environ()) {
		t.Fatalf("exec env = %#v, want os.Environ()", gotEnv)
	}
	// A failed exec is remembered by identity, so the same file is not
	// attempted again.
	if watch.candidate != nil {
		t.Fatalf("candidate = %#v after failed exec, want reset", watch.candidate)
	}
	if got := watch.replaced(); got != "" {
		t.Fatalf("replaced() = %q after failed exec, want the failed identity skipped", got)
	}
}

func TestReexecIfReplacedSkipsWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)
	executed := false
	watch.exec = func(string, []string, []string) error {
		executed = true
		return nil
	}
	source := NewAgentSource(nil)
	for i := 0; i < 4; i++ {
		source.reexecIfReplaced(context.Background(), watch)
	}
	if executed {
		t.Fatal("exec ran for an unchanged binary")
	}
}

// TestWatchReexecsReplacedBinary exercises the watcher loop end to end with a
// seam for exec, so a replaced binary is detected on consecutive polls and the
// watcher keeps running after the exec attempt fails.
func TestWatchReexecsReplacedBinary(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")

	runner := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" && len(args) > 0 && args[0] == "display-message" {
			return []byte("/tmp/tmux-test\n"), nil
		}
		return nil, nil
	})
	source := NewAgentSource(runner)
	setStateDir(source, t.TempDir())
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, PollInterval: 1})

	executed := make(chan string, 1)
	watch := newBinaryWatch()
	watch.launch = func() (string, error) { return path, nil }
	watch.executable = func() (string, error) { return path, nil }
	watch.runningImage = func() (os.FileInfo, error) { return os.Stat(path) }
	watch.preflight = func(context.Context, string) error { return nil }
	watch.exec = func(binary string, _ []string, _ []string) error {
		executed <- binary
		return errors.New("exec failed")
	}
	watch.capture()
	source.binary = watch

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- source.Watch(ctx) }()

	replaceFile(t, path, "replacement binary with a different size")
	select {
	case got := <-executed:
		if got != path {
			t.Fatalf("exec path = %q, want %q", got, path)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watcher did not re-exec a replaced binary")
	}

	// The failed exec must not stop the watcher.
	select {
	case err := <-done:
		t.Fatalf("Watch() returned after a failed exec: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Watch() error = %v, want context.Canceled", err)
	}
}

// TestBinaryWatchStartupReplacementIsBestEffortOutsideLinux documents the
// non-Linux limitation: without /proc/self/exe the baseline is whatever the
// launch path holds when capture runs, so a replacement published before
// capture is missed until the file changes again.
func TestBinaryWatchStartupReplacementIsBestEffortOutsideLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux capture compares against /proc/self/exe")
	}
	dir := t.TempDir()
	oldPath := executableFile(t, dir, "nexo-old", "old binary")
	newPath := executableFile(t, dir, "nexo-new", "new binary")
	installPath := filepath.Join(dir, "nexo")
	if err := os.Symlink(newPath, installPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal(err)
	}

	watch := newBinaryWatch()
	watch.launch = func() (string, error) { return installPath, nil }
	watch.preflight = func(context.Context, string) error { return nil }
	watch.capture()
	if !watch.started {
		t.Fatal("capture() did not record the launch path")
	}
	// The process is still running oldPath, but non-Linux capture records
	// the newPath that the install path already points at.
	for i := 0; i < 3; i++ {
		if got := watch.replaced(); got != "" {
			t.Fatalf("replaced() = %q for a pre-capture replacement, want the best-effort baseline", got)
		}
	}
}

func TestBinaryWatchDoesNotRetryFailedIdentity(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)
	replaceFile(t, path, "bad candidate")

	preflights := 0
	watch.preflight = func(context.Context, string) error {
		preflights++
		return errors.New("candidate is not runnable")
	}
	watch.exec = func(string, []string, []string) error {
		t.Fatal("exec ran after a failed preflight")
		return nil
	}
	source := NewAgentSource(nil)
	source.reexecIfReplaced(context.Background(), watch) // debounce
	source.reexecIfReplaced(context.Background(), watch) // failed preflight
	if preflights != 1 {
		t.Fatalf("preflights = %d, want 1", preflights)
	}
	for i := 0; i < 4; i++ {
		source.reexecIfReplaced(context.Background(), watch)
	}
	if preflights != 1 {
		t.Fatalf("preflights = %d for the same failed identity, want no retry", preflights)
	}

	// A changed file is a new identity: it goes through the usual two-poll
	// confirmation and is preflighted again.
	replaceFile(t, path, "different bad candidate")
	source.reexecIfReplaced(context.Background(), watch)
	if preflights != 1 {
		t.Fatalf("preflights = %d on the first poll of a new identity, want debounce", preflights)
	}
	source.reexecIfReplaced(context.Background(), watch)
	if preflights != 2 {
		t.Fatalf("preflights = %d after confirming a new identity, want 2", preflights)
	}
}

func TestBinaryWatchRetriesWhenFailedCandidateCannotBePinned(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)
	replaceFile(t, path, "candidate")
	watch.open = func(string) (*os.File, error) {
		return nil, errors.New("open failed")
	}
	preflights := 0
	watch.preflight = func(context.Context, string) error {
		preflights++
		return errors.New("candidate is not runnable")
	}
	source := NewAgentSource(nil)
	source.reexecIfReplaced(context.Background(), watch)
	source.reexecIfReplaced(context.Background(), watch)
	source.reexecIfReplaced(context.Background(), watch)
	source.reexecIfReplaced(context.Background(), watch)
	if preflights != 2 {
		t.Fatalf("preflights = %d when pinning failed, want 2 retries", preflights)
	}
}

func TestBinaryWatchFailedInodeReuseDoesNotHideValidCandidate(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "old binary")
	watch := capturedWatch(t, path)
	source := NewAgentSource(nil)

	var opened []*os.File
	var openedFDs []int
	watch.open = func(name string) (*os.File, error) {
		file, err := os.Open(name)
		if err == nil {
			assertCloseOnExec(t, file)
			opened = append(opened, file)
			openedFDs = append(openedFDs, int(file.Fd()))
		}
		return file, err
	}
	preflights := 0
	watch.preflight = func(_ context.Context, candidatePath string) error {
		preflights++
		contents, err := os.ReadFile(candidatePath)
		if err != nil {
			t.Fatalf("read candidate: %v", err)
		}
		if string(contents) != "candidate-good!" {
			return errors.New("candidate is not runnable")
		}
		return nil
	}
	watch.exec = func(binary string, _ []string, _ []string) error {
		if binary != path {
			t.Fatalf("exec path = %q, want %q", binary, path)
		}
		return nil
	}

	// The failed candidate is opened before preflight and remains pinned after
	// its pathname is removed and replaced. The assertions below test the
	// descriptor lifetime directly, rather than relying on inode allocation.
	replaceFile(t, path, "candidate-bad!!")
	source.reexecIfReplaced(context.Background(), watch)
	source.reexecIfReplaced(context.Background(), watch)
	if preflights != 1 {
		t.Fatalf("preflights = %d after failed candidate, want 1", preflights)
	}
	if len(opened) != 1 || watch.failed == nil || watch.failed.file != opened[0] {
		t.Fatalf("failed cache = %#v, opened = %d; want the pinned candidate", watch.failed, len(opened))
	}
	firstFile := opened[0]
	firstFD := int(firstFile.Fd())
	firstIdentity := watch.failed.identity
	assertCloseOnExec(t, firstFile)
	assertFileIdentity(t, firstFile, firstIdentity)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replaceFile(t, path, "candidate-second")
	// The first failed inode is still open even though the launch path now
	// names a distinct replacement.
	assertFileIdentity(t, firstFile, firstIdentity)

	source.reexecIfReplaced(context.Background(), watch)
	source.reexecIfReplaced(context.Background(), watch)
	if preflights != 2 {
		t.Fatalf("preflights = %d after second failed candidate, want 2", preflights)
	}
	if len(opened) != 2 || watch.failed == nil || watch.failed.file != opened[1] {
		t.Fatalf("failed cache = %#v, opened = %d; want the second pinned candidate", watch.failed, len(opened))
	}
	assertClosedFD(t, firstFD)
	secondFD := openedFDs[1]

	// A successful mocked exec clears and closes both the cached failed file
	// and the newly opened candidate descriptor.
	replaceFile(t, path, "candidate-good!")
	source.reexecIfReplaced(context.Background(), watch)
	source.reexecIfReplaced(context.Background(), watch)
	if watch.failed != nil {
		t.Fatalf("failed cache = %#v after successful exec, want nil", watch.failed)
	}
	if len(opened) != 3 {
		t.Fatalf("opened files = %d after successful exec, want 3", len(opened))
	}
	assertClosedFD(t, secondFD)
	assertClosedFD(t, openedFDs[2])
}

func assertFileIdentity(t *testing.T, file *os.File, want fileIdentity) {
	t.Helper()
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("fstat failed: %v", err)
	}
	got, ok := identityOf(info)
	if !ok {
		t.Fatal("fstat did not provide a file identity")
	}
	if got.dev != want.dev || got.ino != want.ino {
		t.Fatalf("fstat identity = dev %d inode %d, want dev %d inode %d", got.dev, got.ino, want.dev, want.ino)
	}
}

func assertCloseOnExec(t *testing.T, file *os.File) {
	t.Helper()
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil {
		t.Fatalf("fcntl(F_GETFD) = %v", err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("fd %d is not close-on-exec", file.Fd())
	}
}

func assertClosedFD(t *testing.T, fd int) {
	t.Helper()
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("fcntl(F_GETFD) on fd %d = %v, want EBADF", fd, err)
	}
}

func processIsZombie(pid int) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	contents, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	end := strings.LastIndexByte(string(contents), ')')
	return end >= 0 && len(contents) > end+2 && contents[end+2] == 'Z'
}

// TestPreflightBinaryKillsDescendantHoldingPipes is the regression for a
// candidate wrapper that backgrounds a long sleep inheriting stdout and then
// waits for it. Killing only the immediate child would leave the pipe open and
// Wait blocked far past the deadline.
func TestPreflightBinaryKillsDescendantHoldingPipes(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "nexo")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30 &\nwait\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := binaryPreflightTimeout
	binaryPreflightTimeout = 2 * time.Second
	defer func() { binaryPreflightTimeout = previous }()

	start := time.Now()
	err := preflightBinary(context.Background(), script)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("preflightBinary() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("preflightBinary() took %v with a 2s deadline; a descendant held the pipes", elapsed)
	}
}

// TestPreflightBinaryKillsDescendantAfterCandidateExit verifies that an
// exited candidate's background process is killed after WaitDelay returns.
func TestPreflightBinaryKillsDescendantAfterCandidateExit(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "child.pid")
	script := filepath.Join(dir, "nexo")
	contents := fmt.Sprintf("#!/bin/sh\nprintf 'nexo\\n'\nsleep 30 &\nprintf '%%s' \"$!\" > %q\nexit 0\n", pidPath)
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err := preflightBinary(context.Background(), script)
	elapsed := time.Since(start)
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("preflightBinary() error = %v, want exec.ErrWaitDelay", err)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("preflightBinary() took %v after the candidate exited", elapsed)
	}
	pidContents, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(string(pidContents))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", pidContents, err)
	}
	// Register cleanup immediately once the child PID is known, before any
	// further assertion can fail.
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	deadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) || processIsZombie(pid) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background process %d still exists after preflight cleanup", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPreflightBinaryStopsOnWatcherCancellation verifies that the watcher
// context, not just the preflight deadline, stops a hanging candidate.
func TestPreflightBinaryStopsOnWatcherCancellation(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	script := filepath.Join(dir, "nexo")
	contents := fmt.Sprintf("#!/bin/sh\ntouch %q\nsleep 30\n", marker)
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- preflightBinary(ctx, script) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("candidate never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	cancel()
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("preflightBinary() error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancellation took %v, want a prompt return", elapsed)
	}
}

func TestBoundedBufferCapsOutput(t *testing.T) {
	var buffer boundedBuffer
	buffer.limit = 4
	contents := "nexo and more"
	written, err := buffer.Write([]byte(contents))
	if written != len(contents) || err != nil {
		t.Fatalf("Write() = %d, %v; want %d, nil", written, err, len(contents))
	}
	if got := buffer.buf.String(); got != "nexo" {
		t.Fatalf("buffer = %q, want %q", got, "nexo")
	}
}

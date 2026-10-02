package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// binaryPreflightTimeout bounds one candidate validation. It is a variable so
// tests can shorten it.
var binaryPreflightTimeout = 5 * time.Second

const (
	// binaryPreflightWaitDelay bounds how long Wait may keep reading the
	// candidate's pipes after the deadline, for example when a descendant
	// escaped the preflight process group.
	binaryPreflightWaitDelay = time.Second
	// binaryPreflightOutputLimit caps how much of the candidate's stdout is
	// retained for the version check.
	binaryPreflightOutputLimit = 4 << 10
)

// fileIdentity is the stat identity of one file. Device and inode identify
// the file itself; size and mtime catch an in-place rewrite (for example a
// copy) on platforms where the running image does not provide a stable
// identity for that comparison.
type fileIdentity struct {
	dev   uint64
	ino   uint64
	size  int64
	mtime int64
}

func identityOf(info os.FileInfo) (fileIdentity, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, false
	}
	return fileIdentity{
		dev:   uint64(stat.Dev),
		ino:   uint64(stat.Ino),
		size:  info.Size(),
		mtime: info.ModTime().UnixNano(),
	}, true
}

// sameRunningImage reports whether path still points at the image that is
// running. On Linux an unchanged launch path shares its inode with
// /proc/self/exe, while an atomic replacement leaves /proc/self/exe on the old
// inode — which is exactly what makes the replacement detectable. Size and
// mtime are ignored so a metadata-only change on that same image (for
// example a touch) is not mistaken for a replacement.
func sameRunningImage(a, b fileIdentity) bool {
	if runtime.GOOS == "linux" {
		return a.dev == b.dev && a.ino == b.ino
	}
	return a == b
}

// launchPath returns the unresolved pathname used to start the process. In
// particular, it intentionally preserves an install symlink so replacing
// that symlink is visible to os.Stat and the subsequent exec.
func launchPath() (string, error) {
	path := os.Args[0]
	if path == "" {
		return "", fmt.Errorf("argv[0] is empty")
	}
	if !strings.ContainsRune(path, filepath.Separator) {
		var err error
		path, err = exec.LookPath(path)
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(path)
}

// binaryWatch detects when the executable a watcher started from has been
// replaced and re-execs the new file in place. It is a plain value owned by
// the single Watch goroutine.
type failedCandidate struct {
	identity fileIdentity
	file     *os.File
}

type binaryWatch struct {
	path      string
	start     fileIdentity
	started   bool
	candidate *fileIdentity

	// failed is the identity of a candidate that failed preflight or exec,
	// together with a read-only descriptor that pins its inode. The same
	// unchanged file is never attempted again; only a new identity, after the
	// usual two-poll confirmation, is retried.
	failed *failedCandidate

	// Seams, overridable in tests.
	launch       func() (string, error)
	executable   func() (string, error)
	runningImage func() (os.FileInfo, error)
	stat         func(string) (os.FileInfo, error)
	open         func(string) (*os.File, error)
	preflight    func(context.Context, string) error
	exec         func(string, []string, []string) error
}

func newBinaryWatch() *binaryWatch {
	return &binaryWatch{
		launch:       launchPath,
		executable:   os.Executable,
		runningImage: func() (os.FileInfo, error) { return os.Stat("/proc/self/exe") },
		stat:         os.Stat,
		open:         os.Open,
		preflight:    preflightBinary,
		exec:         syscall.Exec,
	}
}

// capture records the running executable at watcher startup. The launch path
// is retained unresolved so an install symlink can later be swapped. On
// Linux, the baseline comes from /proc/self/exe, which remains the image that
// is actually running even after its pathname has been replaced or deleted.
// A failure leaves the watch uncaptured; replaced retries capture on a later
// poll rather than disabling the check permanently.
func (w *binaryWatch) capture() {
	if w == nil || w.started {
		return
	}
	if w.launch == nil {
		w.launch = launchPath
	}
	if w.executable == nil {
		w.executable = os.Executable
	}
	if w.stat == nil {
		w.stat = os.Stat
	}
	path, err := w.launch()
	if err != nil || path == "" {
		// os.Executable is only a fallback when argv[0] cannot identify the
		// launch path. It may resolve symlinks on some platforms, but there
		// is no unresolved path to preserve in that case.
		path, err = w.executable()
	}
	if err != nil || path == "" {
		return
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return
	}
	if _, err := w.stat(path); err != nil {
		return
	}

	var info os.FileInfo
	if runtime.GOOS == "linux" {
		if w.runningImage == nil {
			w.runningImage = func() (os.FileInfo, error) { return os.Stat("/proc/self/exe") }
		}
		info, err = w.runningImage()
	} else {
		// Outside Linux there is no stable handle on the running image, so
		// the baseline is best effort: a replacement published before this
		// capture is recorded as the new baseline and is noticed only when
		// the file changes again. Linux closes that startup race with
		// /proc/self/exe above.
		info, err = w.stat(path)
	}
	if err != nil {
		return
	}
	identity, ok := identityOf(info)
	if !ok {
		return
	}
	w.path = path
	w.start = identity
	w.started = true
}

// replaced reports that the watched launch pathname differs from the running
// image, after seeing the same replacement on two consecutive polls. It
// returns the unresolved path to exec, or "" when there is nothing to do. A
// missing, unreadable, non-regular or non-executable file never counts and
// resets the candidate, so a replacement caught mid-write is retried later.
func (w *binaryWatch) replaced() string {
	if w == nil {
		return ""
	}
	if !w.started {
		w.capture()
		return ""
	}
	if w.stat == nil {
		w.candidate = nil
		return ""
	}
	info, err := w.stat(w.path)
	if err != nil {
		w.candidate = nil
		return ""
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		w.candidate = nil
		return ""
	}
	identity, ok := identityOf(info)
	if !ok {
		w.candidate = nil
		return ""
	}
	if w.failed != nil && w.failed.identity == identity {
		// This exact file already failed preflight or exec. Retrying it would
		// stall polling again, so wait for a different identity.
		w.candidate = nil
		return ""
	}
	if sameRunningImage(identity, w.start) {
		w.candidate = nil
		return ""
	}
	if w.candidate == nil || *w.candidate != identity {
		pending := identity
		w.candidate = &pending
		return ""
	}
	return w.path
}

// boundedBuffer retains at most limit bytes and silently discards the rest,
// so an untrusted candidate cannot make the preflight allocate without bound.
// It never fails a write, so a verbose candidate cannot block on its output.
type boundedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if remaining := b.limit - b.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.buf.Write(p)
	}
	return written, nil
}

// preflightBinary checks that a candidate is a complete nexo binary before
// the watcher gives up its current process image. Installations are expected
// to publish completed files atomically; this check also rejects stable
// partial or otherwise non-runnable candidates.
//
// The candidate runs in its own process group, and the deadline derives from
// the watcher context, so shutdown cancels it. A candidate that hangs, or
// leaves a descendant holding the pipes, cannot stall the watcher
// indefinitely.
func preflightBinary(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, binaryPreflightTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	// Kill the whole group, not just the immediate child, so descendants that
	// inherited stdout or stderr cannot keep the pipes open past the deadline.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			// The group already exited; report the process result rather
			// than a needless kill error.
			return os.ErrProcessDone
		}
		return err
	}
	// Bound the pipe wait even if a descendant escaped the process group.
	cmd.WaitDelay = binaryPreflightWaitDelay
	var stdout boundedBuffer
	stdout.limit = binaryPreflightOutputLimit
	cmd.Stdout = &stdout
	err := cmd.Run()
	if cmd.Process != nil {
		// WaitDelay also covers a leader that exits before its descendants. In
		// that case cmd.Cancel is not called, so clean up the whole group for
		// every outcome once Run has returned.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if !strings.HasPrefix(stdout.buf.String(), "nexo") {
		return fmt.Errorf("--version output does not start with %q", "nexo")
	}
	return nil
}

// reexecBinary replaces the running process with the binary at path, keeping
// the original argv and environment so flags such as --config-file survive.
// The flock fd is close-on-exec, so the new process takes the watcher lock and
// reconciles highlights and glance on startup. Exec replaces the process
// image, so no deferred cleanup runs; on failure it returns and the watcher
// keeps polling.
func (w *binaryWatch) reexecBinary(path string) error {
	return w.exec(path, os.Args, os.Environ())
}

func (w *binaryWatch) openFailedCandidate(path string, candidate *fileIdentity) *os.File {
	if candidate == nil {
		return nil
	}
	open := w.open
	if open == nil {
		open = os.Open
	}
	file, err := open(path)
	if err != nil {
		return nil
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil
	}
	identity, ok := identityOf(info)
	if !ok || identity != *candidate {
		_ = file.Close()
		return nil
	}
	return file
}

func (w *binaryWatch) clearFailed() {
	if w.failed == nil {
		return
	}
	if w.failed.file != nil {
		_ = w.failed.file.Close()
	}
	w.failed = nil
}

func (w *binaryWatch) rememberFailed(candidate *fileIdentity, file *os.File) {
	if candidate == nil || file == nil {
		if file != nil {
			_ = file.Close()
		}
		return
	}
	w.clearFailed()
	w.failed = &failedCandidate{identity: *candidate, file: file}
}

// reexecIfReplaced validates and re-execs a replaced nexo binary in place. A
// failed preflight or exec is reported. When the failed candidate can be
// pinned, that identity is never retried: the watcher keeps polling and only
// a new identity, after the usual two-poll confirmation, is attempted again.
// If pinning fails, the candidate remains retryable. The watcher context
// bounds the preflight so shutdown cancels a hanging candidate.
func (s *Source) reexecIfReplaced(ctx context.Context, w *binaryWatch) {
	path := w.replaced()
	if path == "" {
		return
	}
	candidate := w.candidate
	w.candidate = nil
	failedFile := w.openFailedCandidate(path, candidate)
	recordFailure := func(err error, message string) {
		if failedFile != nil {
			w.rememberFailed(candidate, failedFile)
			failedFile = nil
		}
		fmt.Fprintf(os.Stderr, "nexo: %s %s: %v\n", message, path, err)
	}
	preflight := w.preflight
	if preflight == nil {
		preflight = preflightBinary
	}
	if err := preflight(ctx, path); err != nil {
		recordFailure(err, "preflight")
		return
	}
	if err := w.reexecBinary(path); err != nil {
		recordFailure(err, "re-exec")
		return
	}
	if failedFile != nil {
		_ = failedFile.Close()
	}
	w.clearFailed()
}

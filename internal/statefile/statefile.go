// Package statefile provides locked, atomic state-file primitives shared by
// backend adapters.
package statefile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// DefaultStateDir returns nexo's XDG state directory for backend state.
func DefaultStateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "nexo")
	}
	if dir, err := os.UserHomeDir(); err == nil {
		return filepath.Join(dir, ".local", "state", "nexo")
	}
	return filepath.Join(os.TempDir(), "nexo")
}

// ServerFileName returns a filesystem-safe, server-specific state-file stem.
func ServerFileName(server string) string {
	if server == "" {
		server = "default"
	}
	digest := sha256.Sum256([]byte(server))
	return filepath.Base(server) + "-" + hex.EncodeToString(digest[:8])
}

// Path returns a state path for one server and suffix.
func Path(dir, server, suffix string) string {
	if dir == "" {
		dir = DefaultStateDir()
	}
	return filepath.Join(dir, ServerFileName(server)+"."+suffix)
}

// Lock obtains an advisory exclusive lock. A non-blocking lock returns the
// underlying EWOULDBLOCK/EAGAIN error when another process holds it.
func Lock(path string, nonBlocking bool) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open state lock: %w", err)
	}
	flags := syscall.LOCK_EX
	if nonBlocking {
		flags |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), flags); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("flock state lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

// WriteAtomic writes contents with restrictive permissions and atomically
// replaces path after syncing the temporary file.
func WriteAtomic(path string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".nexo-state-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

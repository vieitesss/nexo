// Package destination resolves paths used by filesystem-producing actions.
//
// It is deliberately independent of any backend: callers get one normalized
// path, a preflight error when the destination cannot be used, and an offer
// when its existing parent is not yet a configured project path.
package destination

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Result is the validated destination and the parent-path offer associated
// with it. AddParent is a suggestion; Resolve never edits configuration.
type Result struct {
	Path      string
	Parent    string
	AddParent bool
}

// Options supplies project paths and home expansion to Resolver.
type Options struct {
	Paths []string
	Home  string
	// AllowMissingParent keeps the destination itself safe to create while
	// leaving parent-directory policy to a caller such as worktree extraction.
	AllowMissingParent bool
}

// Resolver is the backend-neutral destination preflight module.
type Resolver struct{ Options }

// New returns a resolver for one configuration snapshot.
func New(options Options) Resolver { return Resolver{Options: options} }

// Resolve validates a destination, returning an absolute path. The path must
// not exist and its parent must already be an existing directory.
func (r Resolver) Resolve(path string) (Result, error) {
	path = expandHome(strings.TrimSpace(path), r.Home)
	if path == "" {
		return Result{}, errors.New("destination is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Result{}, fmt.Errorf("resolve destination %q: %w", path, err)
	}
	absolute = filepath.Clean(absolute)
	if _, err := os.Lstat(absolute); err == nil {
		return Result{}, fmt.Errorf("destination %q already exists", absolute)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("check destination %q: %w", absolute, err)
	}
	parent := filepath.Dir(absolute)
	info, err := os.Stat(parent)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Result{}, fmt.Errorf("check destination parent %q: %w", parent, err)
		}
		if !r.AllowMissingParent {
			return Result{}, fmt.Errorf("destination parent %q does not exist", parent)
		}
	} else if !info.IsDir() {
		return Result{}, fmt.Errorf("destination parent %q is not a directory", parent)
	}
	return Result{Path: absolute, Parent: parent, AddParent: !covered(r.Paths, parent, r.Home)}, nil
}

// ResolvePath is a convenience wrapper for callers that do not need to retain
// a Resolver value.
func ResolvePath(path string, paths []string, home string) (Result, error) {
	return New(Options{Paths: paths, Home: home}).Resolve(path)
}

func expandHome(path, home string) string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	switch {
	case path == "~", path == "$HOME":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	case strings.HasPrefix(path, "$HOME/"):
		return filepath.Join(home, strings.TrimPrefix(path, "$HOME/"))
	default:
		return path
	}
}

func covered(paths []string, parent, home string) bool {
	parent = cleanAbs(parent)
	for _, value := range paths {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		value = expandHome(value, home)
		if strings.HasSuffix(value, string(filepath.Separator)+"*") {
			value = strings.TrimSuffix(value, string(filepath.Separator)+"*")
		}
		if cleanAbs(value) == parent {
			return true
		}
	}
	return false
}

func cleanAbs(path string) string {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}

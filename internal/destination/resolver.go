// Package destination resolves paths used by filesystem-producing actions.
//
// It is deliberately independent of any backend: callers get one normalized
// path, a preflight error when the destination cannot be used, and an offer
// when its existing parent is not yet a configured project path.
package destination

import (
	"errors"
	"fmt"
	"net/url"
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
	// leaving parent-directory policy to the caller's operation flow.
	AllowMissingParent bool
}

// Resolver is the backend-neutral destination preflight module.
type Resolver struct{ Options }

// New returns a resolver for one configuration snapshot.
func New(options Options) Resolver { return Resolver{Options: options} }

// Resolve validates a destination, returning an absolute path. The path must
// not exist and its parent must already be an existing directory.
func (r Resolver) Resolve(path string) (Result, error) {
	path = ExpandHome(strings.TrimSpace(path), r.Home)
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
	return Result{Path: absolute, Parent: parent, AddParent: !Covered(r.Paths, parent, r.Home)}, nil
}

// ResolvePath is a convenience wrapper for callers that do not need to retain
// a Resolver value.
func ResolvePath(path string, paths []string, home string) (Result, error) {
	return New(Options{Paths: paths, Home: home}).Resolve(path)
}

// ExpandHome expands the explicit home forms accepted by destination paths.
// Unlike a shell, callers invoke it directly before passing an operand to Git.
func ExpandHome(path, home string) string {
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

// Covered reports whether parent is already represented by one of the
// configured project paths. Physical aliases are treated as the same path so
// callers do not make different add-parent decisions for symlinked locations.
func Covered(paths []string, parent, home string) bool {
	parent = coveredRoot(parent, home)
	for _, value := range paths {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if coveredRoot(value, home) == parent {
			return true
		}
	}
	return false
}

// coveredRoot puts a project path in the one spelling Covered compares, so
// "~/work", "$HOME/work", and "~/work/*" all name the same root.
func coveredRoot(path, home string) string {
	path = ExpandHome(strings.TrimSpace(path), home)
	path = strings.TrimSuffix(path, string(filepath.Separator)+"*")
	return realAbs(path)
}

// DefaultPath derives Git's implicit destination from a complete source
// operand and leaves all preflight checks to Resolver.Resolve.
func DefaultPath(source, cwd, home string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", errors.New("clone source is empty")
	}
	operand := source
	if parsed, err := url.Parse(source); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		operand = parsed.Path
	} else if at := strings.Index(source, ":"); at > 0 && !strings.Contains(source[:at], string(filepath.Separator)) {
		operand = source[at+1:]
	}
	operand = strings.TrimRight(operand, "/\\")
	name := filepath.Base(ExpandHome(operand, home))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "", fmt.Errorf("cannot derive clone destination from %q", source)
	}
	name = strings.TrimSuffix(name, ".git")
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "", fmt.Errorf("cannot derive clone destination from %q", source)
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
	}
	return filepath.Join(cwd, name), nil
}

func realAbs(path string) string {
	absolute := cleanAbs(path)
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return cleanAbs(resolved)
	}
	return absolute
}

func cleanAbs(path string) string {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}

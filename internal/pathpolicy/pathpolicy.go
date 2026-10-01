// Package pathpolicy contains the backend-neutral policy for deciding whether
// an observed working directory uses a selected project.
package pathpolicy

import (
	"os"
	"path/filepath"
	"strings"
)

// Resolver supplies the minimal filesystem observations needed for the
// physical-alias check. Backends provide the platform-independent default or
// a fake in tests.
type Resolver interface {
	Lstat(string) (os.FileInfo, error)
	EvalSymlinks(string) (string, error)
}

type OSResolver struct{}

func (OSResolver) Lstat(path string) (os.FileInfo, error)   { return os.Lstat(path) }
func (OSResolver) EvalSymlinks(path string) (string, error) { return filepath.EvalSymlinks(path) }

// Contains reports whether observed is the selected project or a descendant.
// A selected symlink is removed as a link, so its target is deliberately not
// considered. The lexical check is pure; physical aliases are checked only for
// an ordinary selected directory.
func Contains(selected, observed string, selectedIsSymlink bool, resolver Resolver) bool {
	selected, observed = filepath.Clean(selected), filepath.Clean(observed)
	if lexicalContains(selected, observed) {
		return true
	}
	if selectedIsSymlink || resolver == nil {
		return false
	}
	selectedPhysical, selectedErr := resolver.Lstat(selected)
	if selectedErr != nil || selectedPhysical.Mode()&os.ModeSymlink != 0 {
		return false
	}
	resolvedSelected, selectedErr := resolver.EvalSymlinks(selected)
	resolvedObserved, observedErr := resolver.EvalSymlinks(observed)
	if selectedErr != nil || observedErr != nil {
		return false
	}
	return lexicalContains(resolvedSelected, resolvedObserved)
}

func lexicalContains(parent, child string) bool {
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

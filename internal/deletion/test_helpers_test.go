package deletion

import (
	"path/filepath"
	"testing"
)

// realTempDir returns t.TempDir() with symlinks resolved. macOS puts temp dirs
// under /var, which is a symlink to /private/var, and deletion refuses paths
// below a symlinked ancestor.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

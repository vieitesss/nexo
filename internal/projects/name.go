package projects

import (
	"path/filepath"
	"strings"
)

// TargetName derives the stable container name shared by backends from a
// project path.
func TargetName(path string) string {
	clean := filepath.Clean(path)
	parent := filepath.Base(filepath.Dir(clean))
	base := filepath.Base(clean)
	return strings.NewReplacer(".", "_", ":", "_").Replace(parent + "_" + base)
}

package destination

import (
	"fmt"
	"os"
)

// CreateDirectory creates path and any missing parents. Callers validate path
// with Resolver.Resolve first; this only performs the filesystem mutation.
func CreateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create directory %q: %w", path, err)
	}
	return nil
}

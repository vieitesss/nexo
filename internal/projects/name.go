package projects

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/runner"
	"github.com/vieitesss/nexo/internal/sessionname"
)

// ResolveTargetName retains legacy naming unless a template is configured.
func ResolveTargetName(ctx context.Context, path string, template *string, commandRunner runner.CommandRunner) (string, error) {
	if template == nil {
		return TargetName(path), nil
	}
	return sessionname.Resolve(ctx, path, *template, commandRunner)
}

// TargetName derives the stable container name shared by backends from a
// project path.
func TargetName(path string) string {
	clean := filepath.Clean(path)
	parent := filepath.Base(filepath.Dir(clean))
	base := filepath.Base(clean)
	return strings.NewReplacer(".", "_", ":", "_").Replace(parent + "_" + base)
}

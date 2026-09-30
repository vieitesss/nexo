package tui

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/picker"
)

type configuredGitLoader struct{}

func (configuredGitLoader) Load(context.Context, string) (picker.GitContext, error) {
	return picker.GitContext{}, nil
}
func (configuredGitLoader) GitConcurrency() int { return 2 }

func TestGitSchedulingUsesConfiguredBound(t *testing.T) {
	model := NewModel(context.Background(), Deps{GitLoader: configuredGitLoader{}, Projects: []picker.Place{{Kind: picker.ProjectKind, Path: "/one", Git: true}}}, ProjectsTab)
	if cap(model.gitSlots) != 2 {
		t.Fatalf("git slots = %d, want configured bound", cap(model.gitSlots))
	}
}

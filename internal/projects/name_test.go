package projects

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTargetNameUsesParentAndBaseAndIsTmuxSafe(t *testing.T) {
	if got, want := TargetName("/work/acme/my.project:dev"), "acme_my_project_dev"; got != want {
		t.Fatalf("TargetName() = %q, want %q", got, want)
	}
}

func TestResolveTargetNameUnsetUsesLegacyWithoutCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme", "my.project")
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r := nameRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		calls++
		return []byte("https://host/remote/repo"), nil
	})
	got, err := ResolveTargetName(context.Background(), path, nil, r)
	if err != nil || got != TargetName(path) || calls != 0 {
		t.Fatalf("unset = %q, %v; calls %d", got, err, calls)
	}
}
func TestResolveTargetNameConfiguredDelegatesAndReturnsRenderErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme", "api")
	template := "{org}/{team}/{repo}"
	got, err := ResolveTargetName(context.Background(), path, &template, nil)
	if err != nil || got != "acme/api" {
		t.Fatalf("configured = %q, %v", got, err)
	}
	template = "{team}"
	if _, err := ResolveTargetName(context.Background(), path, &template, nil); err == nil {
		t.Fatal("missing team render error discarded")
	}
}

type nameRunner func(context.Context, string, ...string) ([]byte, error)

func (f nameRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

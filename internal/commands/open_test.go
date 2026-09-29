package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

type fakeProjectOpener struct {
	path    string
	options capabilities.OpenOptions
}

func (f *fakeProjectOpener) OpenProject(_ context.Context, path string, options capabilities.OpenOptions) error {
	f.path = path
	f.options = options
	return nil
}

func TestOpenCommandOpensOnlyCurrentCandidate(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "app")
	if err := osMkdir(candidate); err != nil {
		t.Fatal(err)
	}
	opener := &fakeProjectOpener{}
	command, ok := NewRegistry().Lookup("open")
	if !ok {
		t.Fatal("open command is not registered")
	}
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Herdr},
		Config:    config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}},
		Adapter:   opener,
		Args:      []string{candidate, "--workspace-id", "workspace-1"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if opener.path != candidate || opener.options.ContainerID != "workspace-1" {
		t.Fatalf("open call = path %q options %#v", opener.path, opener.options)
	}
}

func TestOpenCommandRejectsNonCandidate(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "app")
	if err := osMkdir(candidate); err != nil {
		t.Fatal(err)
	}
	opener := &fakeProjectOpener{}
	command, _ := NewRegistry().Lookup("open")
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}},
		Adapter:   opener,
		Args:      []string{filepath.Join(root, "other")},
	})
	if err == nil || !strings.Contains(err.Error(), "not a current candidate") {
		t.Fatalf("error = %v, want current-candidate rejection", err)
	}
	if opener.path != "" {
		t.Fatalf("opener path = %q, want no call", opener.path)
	}
}

func TestParseOpenArgsRejectsUnknownOptionAndMissingPath(t *testing.T) {
	for _, args := range [][]string{{"--unknown", "/tmp/app"}, {"--workspace-id", "id"}} {
		if _, err := parseOpenArgs(args); err == nil {
			t.Fatalf("parseOpenArgs(%v) error = nil", args)
		}
	}
}

func TestOpenCommandPropagatesOpenerError(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "app")
	if err := osMkdir(candidate); err != nil {
		t.Fatal(err)
	}
	want := errors.New("cannot open")
	opener := &errorProjectOpener{err: want}
	command, _ := NewRegistry().Lookup("open")
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}},
		Adapter:   opener,
		Args:      []string{candidate},
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

type errorProjectOpener struct{ err error }

func (f *errorProjectOpener) OpenProject(context.Context, string, capabilities.OpenOptions) error {
	return f.err
}

func osMkdir(path string) error {
	return os.MkdirAll(path, 0o755)
}

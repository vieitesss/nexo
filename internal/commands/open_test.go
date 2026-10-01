package commands

import (
	"context"
	"encoding/json"
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
	path      string
	options   capabilities.OpenOptions
	container capabilities.OpenedContainer
}

func (f *fakeProjectOpener) OpenProject(_ context.Context, path string, options capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
	f.path = path
	f.options = options
	return f.container, nil
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
	for _, args := range [][]string{{"--unknown", "/tmp/app"}, {"--workspace-id", "id"}, {"/tmp/app", "--no-focus", "--no-focus"}} {
		if _, err := parseOpenArgs(args); err == nil {
			t.Fatalf("parseOpenArgs(%v) error = nil", args)
		}
	}
	parsed, err := parseOpenArgs([]string{"/tmp/app", "--no-focus"})
	if err != nil || !parsed.Options.NoFocus {
		t.Fatalf("parseOpenArgs() = %#v, %v; want NoFocus", parsed, err)
	}
}

func TestOpenCommandNoFocusPrintsOpenedLine(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "app")
	if err := osMkdir(candidate); err != nil {
		t.Fatal(err)
	}
	opener := &fakeProjectOpener{container: capabilities.OpenedContainer{ID: "$12", Name: "root_app", Path: candidate, Created: true}}
	command, _ := NewRegistry().Lookup("open")
	var output strings.Builder
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}},
		Adapter:   opener,
		Args:      []string{candidate, "--no-focus"},
		Stdout:    &output,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := output.String(), "opened root_app ($12)\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if !opener.options.NoFocus {
		t.Fatalf("options = %#v, want NoFocus", opener.options)
	}
}

func TestOpenCommandPlainOpenPrintsNothing(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "app")
	if err := osMkdir(candidate); err != nil {
		t.Fatal(err)
	}
	opener := &fakeProjectOpener{container: capabilities.OpenedContainer{ID: "$12", Name: "root_app", Path: candidate}}
	command, _ := NewRegistry().Lookup("open")
	var output strings.Builder
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}},
		Adapter:   opener,
		Args:      []string{candidate},
		Stdout:    &output,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if output.String() != "" {
		t.Fatalf("output = %q, want empty", output.String())
	}
}

func TestOpenCommandJSONReportsContainer(t *testing.T) {
	for _, created := range []bool{true, false} {
		root := t.TempDir()
		candidate := filepath.Join(root, "app")
		if err := osMkdir(candidate); err != nil {
			t.Fatal(err)
		}
		opener := &fakeProjectOpener{container: capabilities.OpenedContainer{ID: "$12", Name: "root_app", Path: candidate, Created: created}}
		command, _ := NewRegistry().Lookup("open")
		var output strings.Builder
		err := command.Run(context.Background(), Invocation{
			Selection: backend.Selection{Kind: backend.Tmux},
			Config:    config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}},
			Adapter:   opener,
			Args:      []string{candidate, "--no-focus"},
			Stdout:    &output,
			JSON:      true,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		var got struct {
			Backend string `json:"backend"`
			ID      string `json:"id"`
			Name    string `json:"name"`
			Path    string `json:"path"`
			Created bool   `json:"created"`
		}
		if err := json.Unmarshal([]byte(output.String()), &got); err != nil {
			t.Fatalf("decode output %q: %v", output.String(), err)
		}
		if got.Backend != "tmux" || got.ID != "$12" || got.Name != "root_app" || got.Path != candidate || got.Created != created {
			t.Fatalf("output = %#v, want container with created=%v", got, created)
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

func (f *errorProjectOpener) OpenProject(context.Context, string, capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
	return capabilities.OpenedContainer{}, f.err
}

func osMkdir(path string) error {
	return os.MkdirAll(path, 0o755)
}

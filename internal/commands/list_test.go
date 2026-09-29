package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/projects"
)

type fakeContainerAdapter struct {
	containers []capabilities.Container
	previews   map[string]string
}

func (f fakeContainerAdapter) ListContainers(context.Context) ([]capabilities.Container, error) {
	return f.containers, nil
}

func (f fakeContainerAdapter) ReadScreen(_ context.Context, paneID string) (string, error) {
	return f.previews[paneID], nil
}

func TestListPlacesJSONIncludesContainersAndPreview(t *testing.T) {
	command, ok := NewRegistry().Lookup("list")
	if !ok {
		t.Fatal("list command is not registered")
	}
	var output, stderr strings.Builder
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{t.TempDir()}}},
		Adapter:   fakeContainerAdapter{containers: []capabilities.Container{{ID: "$1", Name: "dev", Machine: "host", ActivePaneID: "%1"}}, previews: map[string]string{"%1": "shell$"}},
		Stdout:    &output,
		Stderr:    &stderr,
		JSON:      true,
		Args:      []string{"--places"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var places []picker.Place
	if err := json.Unmarshal([]byte(output.String()), &places); err != nil {
		t.Fatalf("JSON = %q: %v", output.String(), err)
	}
	if len(places) != 1 || places[0].Kind != picker.ContainerKind || places[0].ContainerID != "$1" || places[0].Machine != "host" || places[0].Preview != "shell$" {
		t.Fatalf("places = %#v, want container place", places)
	}
}

func TestListCommandJSONIsStable(t *testing.T) {
	command, ok := NewRegistry().Lookup("list")
	if !ok {
		t.Fatal("list command is not registered")
	}
	var output, stderr strings.Builder
	err := command.Run(context.Background(), Invocation{
		Config: config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{t.TempDir()}}},
		Stdout: &output,
		Stderr: &stderr,
		JSON:   true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var candidates []projects.Candidate
	if err := json.Unmarshal([]byte(output.String()), &candidates); err != nil {
		t.Fatalf("JSON = %q: %v", output.String(), err)
	}
	if len(candidates) != 0 || stderr.Len() != 0 {
		t.Fatalf("candidates = %#v, stderr = %q", candidates, stderr.String())
	}
}

func TestListCommandPrintsGroups(t *testing.T) {
	var output strings.Builder
	err := writeCandidates(&output, []projects.Candidate{{Path: "/a", Group: "/group", Git: true, LinkedWorktree: true}})
	if err != nil {
		t.Fatalf("writeCandidates() error = %v", err)
	}
	if got, want := output.String(), "/group:\n  /a [git] [linked worktree]\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

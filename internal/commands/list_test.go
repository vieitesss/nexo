package commands

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/containerstate"
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

func TestListPlacesKeepsSameLabelContainers(t *testing.T) {
	var output, stderr strings.Builder
	err := listCommand{}.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{t.TempDir()}}},
		Adapter: fakeContainerAdapter{containers: []capabilities.Container{
			{ID: "$1", Name: "api", Machine: "host"},
			{ID: "$2", Name: "api", Machine: "host"},
		}},
		Stdout: &output,
		Stderr: &stderr,
		JSON:   true,
		Args:   []string{"--places"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var places []picker.Place
	if err := json.Unmarshal([]byte(output.String()), &places); err != nil {
		t.Fatalf("JSON = %q: %v", output.String(), err)
	}
	ids := make([]string, 0, len(places))
	for _, place := range places {
		if place.Kind == picker.ContainerKind {
			ids = append(ids, place.ContainerID)
		}
	}
	if len(ids) != 2 || ids[0] != "$1" || ids[1] != "$2" {
		t.Fatalf("ids = %v, want both same-label local containers in %#v", ids, places)
	}
}

func TestListPlacesMergesLocalProxyWithRemoteContainer(t *testing.T) {
	var output, stderr strings.Builder
	runner := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "ssh" {
			t.Fatalf("runner = %q, want ssh", name)
		}
		return []byte("__NEXO_REMOTE_KIND__tmux\n$9\tapi\n"), nil
	})
	err := listCommand{}.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config: config.Config{
			ProjectsConfig: config.ProjectsConfig{Paths: []string{t.TempDir()}},
			Remote:         config.RemoteConfig{Machines: []string{"dev"}, Backends: map[string]string{"dev": "tmux"}},
		},
		Adapter: fakeContainerAdapter{containers: []capabilities.Container{{ID: "workspace-1", Name: "api", Machine: "dev", Proxy: true}}},
		Runner:  runner,
		Stdout:  &output,
		Stderr:  &stderr,
		JSON:    true,
		Args:    []string{"--places"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var places []picker.Place
	if err := json.Unmarshal([]byte(output.String()), &places); err != nil {
		t.Fatalf("JSON = %q: %v", output.String(), err)
	}
	if len(places) != 2 || places[0].Machine != "dev" || places[0].ContainerName != "api" || places[0].ContainerID != "workspace-1" || !places[0].Proxy || places[1].ContainerID != "$9" || places[1].Proxy {
		t.Fatalf("places = %#v, want separate local proxy and remote place", places)
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

func TestListPlacesKeepsProjectAndMachineNamespacesSeparate(t *testing.T) {
	var output strings.Builder
	host := containerstate.LocalMachine()
	err := writePlaces(&output, []picker.Place{
		{Kind: picker.ProjectKind, Group: host, DisplayRemainder: "project"},
		{Kind: picker.ContainerKind, Machine: host, Group: host, DisplayRemainder: "container"},
	})
	if err != nil {
		t.Fatalf("writePlaces() error = %v", err)
	}
	want := host + ":\n  project\n" + host + ":\n  container\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want separate typed groups %q", got, want)
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

func TestListRejectsUnknownArgumentsAsUsageError(t *testing.T) {
	err := listCommand{}.Run(context.Background(), Invocation{Stdout: &strings.Builder{}, Args: []string{"--unknown"}})
	var usage UsageError
	if !errors.As(err, &usage) {
		t.Fatalf("error = %T (%v), want UsageError", err, err)
	}
}

func TestWritePlacesOmitsEmptyPreview(t *testing.T) {
	var output strings.Builder
	err := writePlaces(&output, []picker.Place{{Kind: picker.ContainerKind, Machine: "dev", Group: "dev", DisplayRemainder: "api", Preview: "\n  \n"}})
	if err != nil {
		t.Fatalf("writePlaces() error = %v", err)
	}
	if got, want := output.String(), "dev:\n  api\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

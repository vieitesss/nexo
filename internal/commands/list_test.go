package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/projects"
)

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

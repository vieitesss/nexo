package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
)

type fakeWorktreeCreator struct {
	options        capabilities.WorktreeCreateOptions
	extractOptions capabilities.WorktreeExtractOptions
	result         capabilities.Worktree
	err            error
	removed        bool
	removeError    error
}

func (f *fakeWorktreeCreator) CreateWorktree(_ context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	f.options = options
	if f.err != nil {
		return capabilities.Worktree{}, f.err
	}
	return f.result, nil
}

func (f *fakeWorktreeCreator) ExtractWorktree(_ context.Context, options capabilities.WorktreeExtractOptions) (capabilities.Worktree, error) {
	f.extractOptions = options
	if f.err != nil {
		return capabilities.Worktree{}, f.err
	}
	return f.result, nil
}

func (f *fakeWorktreeCreator) RemoveWorktree(context.Context, capabilities.Worktree) error {
	f.removed = true
	return f.removeError
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output failed") }

func TestWorktreeCreateCommandPrintsJSON(t *testing.T) {
	creator := &fakeWorktreeCreator{result: capabilities.Worktree{Path: "/tmp/created", Branch: "feature"}}
	command, ok := NewRegistry().Lookup("worktree")
	if !ok {
		t.Fatal("worktree command is not registered")
	}
	var output strings.Builder
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   creator,
		Args:      []string{"create", "--new-branch", "/repo", "feature", "/tmp/created"},
		Stdout:    &output,
		JSON:      true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), `"path":"/tmp/created"`) || !strings.Contains(output.String(), `"branch":"feature"`) {
		t.Fatalf("output = %q", output.String())
	}
	if !creator.options.NewBranch || creator.options.Repo != "/repo" {
		t.Fatalf("options = %#v", creator.options)
	}
}

func TestWorktreeCreateCommandAddsParentAndRestoresOnFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	original := "# retain\npaths = [\"/existing\"]\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("create failed")
	creator := &fakeWorktreeCreator{err: wantErr}
	command, _ := NewRegistry().Lookup("worktree")
	destination := filepath.Join(filepath.Dir(configPath), "group", "created")
	err := command.Run(context.Background(), Invocation{
		Selection:  backend.Selection{Kind: backend.Tmux},
		ConfigPath: configPath,
		Adapter:    creator,
		Args:       []string{"create", "--add-parent", "/repo", "feature", destination},
		Stdout:     &bytes.Buffer{},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
	got, readErr := os.ReadFile(configPath)
	if readErr != nil || string(got) != original {
		t.Fatalf("config after failure = %q, %v", got, readErr)
	}
}

func TestWorktreeCreateCommandRollsBackOnOutputFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	original := "paths = [\"/existing\"]\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	creator := &fakeWorktreeCreator{result: capabilities.Worktree{Path: "/tmp/created", Branch: "feature"}}
	command, _ := NewRegistry().Lookup("worktree")
	err := command.Run(context.Background(), Invocation{
		Selection:  backend.Selection{Kind: backend.Tmux},
		ConfigPath: configPath,
		Adapter:    creator,
		Args:       []string{"create", "--add-parent", "/repo", "feature", filepath.Join(filepath.Dir(configPath), "group", "created")},
		Stdout:     failingWriter{},
		JSON:       true,
	})
	if err == nil || !strings.Contains(err.Error(), "output failed") {
		t.Fatalf("Run() error = %v, want output failure", err)
	}
	if !creator.removed {
		t.Fatal("creator was not asked to roll back")
	}
	got, readErr := os.ReadFile(configPath)
	if readErr != nil || string(got) != original {
		t.Fatalf("config after output failure = %q, %v", got, readErr)
	}
}

func TestWorktreeCreateCommandAcceptsDocumentedInvocation(t *testing.T) {
	creator := &fakeWorktreeCreator{result: capabilities.Worktree{Path: "/tmp/created", Branch: "feature"}}
	command, _ := NewRegistry().Lookup("worktree")
	var output bytes.Buffer
	if err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   creator,
		Args:      []string{"create", "/repo", "feature", "/tmp/created"},
		Stdout:    &output,
	}); err != nil {
		t.Fatalf("documented invocation error = %v", err)
	}
	if !strings.Contains(output.String(), "created worktree") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestWorktreeCreateCommandJSONIncludesContainer(t *testing.T) {
	creator := &fakeWorktreeCreator{result: capabilities.Worktree{
		Path:   "/tmp/created",
		Branch: "feature",
		Container: capabilities.OpenedContainer{
			ID: "$12", Name: "repo_feature", Path: "/tmp/created", Created: true,
		},
	}}
	command, _ := NewRegistry().Lookup("worktree")
	var output strings.Builder
	if err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   creator,
		Args:      []string{"create", "--no-focus", "/repo", "feature", "/tmp/created"},
		Stdout:    &output,
		JSON:      true,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var got struct {
		Path      string `json:"path"`
		Branch    string `json:"branch"`
		Container struct {
			Backend string `json:"backend"`
			ID      string `json:"id"`
			Name    string `json:"name"`
			Path    string `json:"path"`
			Created bool   `json:"created"`
		} `json:"container"`
	}
	if err := json.Unmarshal([]byte(output.String()), &got); err != nil {
		t.Fatalf("decode output %q: %v", output.String(), err)
	}
	if got.Path != "/tmp/created" || got.Branch != "feature" || got.Container.Backend != "tmux" || got.Container.ID != "$12" || got.Container.Name != "repo_feature" || !got.Container.Created {
		t.Fatalf("output = %#v", got)
	}
	if !creator.options.NoFocus {
		t.Fatalf("options = %#v, want NoFocus", creator.options)
	}
}

func TestParseWorktreeCreateArgs(t *testing.T) {
	parsed, err := parseWorktreeCreateArgs([]string{"create", "--add-parent", "--no-focus", "/repo", "branch", "/tmp/destination", "--new-branch"})
	if err != nil {
		t.Fatalf("parseWorktreeCreateArgs() error = %v", err)
	}
	if !parsed.AddParent || !parsed.NewBranch || !parsed.NoFocus || parsed.Repo != "/repo" || parsed.Branch != "branch" || parsed.Destination != "/tmp/destination" {
		t.Fatalf("parsed = %#v", parsed)
	}
	for _, args := range [][]string{{"create", "/repo", "branch"}, {"create", "--unknown", "/repo", "branch", "/tmp/destination"}, {"create", "--no-focus", "--no-focus", "/repo", "branch", "/tmp/destination"}} {
		if _, err := parseWorktreeCreateArgs(args); err == nil {
			t.Fatalf("parseWorktreeCreateArgs(%v) error = nil", args)
		}
	}
}

func TestWorktreeCreateCommandRequiresCreator(t *testing.T) {
	command, _ := NewRegistry().Lookup("worktree")
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Args:      []string{"create", "/repo", "branch", "/tmp/destination"},
	})
	if err == nil || !strings.Contains(err.Error(), "does not support creating worktrees") {
		t.Fatalf("error = %v", err)
	}
}

func TestWorktreeExtractCommandPrintsJSON(t *testing.T) {
	extractor := &fakeWorktreeCreator{result: capabilities.Worktree{
		Path:         "/tmp/extracted",
		Branch:       "feature",
		ReturnBranch: "main",
		Container:    capabilities.OpenedContainer{ID: "$3", Name: "repo_feature", Path: "/tmp/extracted", Created: true},
	}}
	command, _ := NewRegistry().Lookup("worktree")
	var output strings.Builder
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   extractor,
		Args:      []string{"extract", "--return-branch", "main", "--no-focus", "/repo", "/tmp/extracted"},
		Stdout:    &output,
		JSON:      true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var got struct {
		Path         string `json:"path"`
		Branch       string `json:"branch"`
		ReturnBranch string `json:"return_branch"`
		Container    struct {
			Backend string `json:"backend"`
			ID      string `json:"id"`
			Created bool   `json:"created"`
		} `json:"container"`
	}
	if err := json.Unmarshal([]byte(output.String()), &got); err != nil {
		t.Fatalf("decode output %q: %v", output.String(), err)
	}
	if got.Path != "/tmp/extracted" || got.Branch != "feature" || got.ReturnBranch != "main" || got.Container.Backend != "tmux" || got.Container.ID != "$3" || !got.Container.Created {
		t.Fatalf("output = %#v", got)
	}
	options := extractor.extractOptions
	if options.Repo != "/repo" || options.Destination != "/tmp/extracted" || options.ReturnBranch != "main" || !options.NoFocus {
		t.Fatalf("extract options = %#v", options)
	}
}

func TestWorktreeExtractCommandPrintsSummary(t *testing.T) {
	extractor := &fakeWorktreeCreator{result: capabilities.Worktree{Path: "/tmp/extracted", Branch: "feature", ReturnBranch: "main"}}
	command, _ := NewRegistry().Lookup("worktree")
	var output bytes.Buffer
	if err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   extractor,
		Args:      []string{"extract", "/repo", "/tmp/extracted"},
		Stdout:    &output,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "extracted worktree /tmp/extracted on feature") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestWorktreeExtractCommandRollsBackOnOutputFailure(t *testing.T) {
	extractor := &fakeWorktreeCreator{result: capabilities.Worktree{Path: "/tmp/extracted", Branch: "feature", ReturnBranch: "main"}}
	command, _ := NewRegistry().Lookup("worktree")
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   extractor,
		Args:      []string{"extract", "/repo", "/tmp/extracted"},
		Stdout:    failingWriter{},
		JSON:      true,
	})
	if err == nil || !strings.Contains(err.Error(), "output failed") {
		t.Fatalf("Run() error = %v, want output failure", err)
	}
	if !extractor.removed {
		t.Fatal("extractor was not asked to roll back")
	}
}

func TestWorktreeExtractCommandAddsParentAndRestoresOnFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	original := "# retain\npaths = [\"/existing\"]\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("extract failed")
	extractor := &fakeWorktreeCreator{err: wantErr}
	command, _ := NewRegistry().Lookup("worktree")
	destination := filepath.Join(filepath.Dir(configPath), "group", "extracted")
	err := command.Run(context.Background(), Invocation{
		Selection:  backend.Selection{Kind: backend.Tmux},
		ConfigPath: configPath,
		Adapter:    extractor,
		Args:       []string{"extract", "--add-parent", "/repo", destination},
		Stdout:     &bytes.Buffer{},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
	got, readErr := os.ReadFile(configPath)
	if readErr != nil || string(got) != original {
		t.Fatalf("config after failure = %q, %v", got, readErr)
	}
}

func TestWorktreeExtractCommandRequiresExtractor(t *testing.T) {
	command, _ := NewRegistry().Lookup("worktree")
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   creatorOnly{},
		Args:      []string{"extract", "/repo", "/tmp/destination"},
		Stdout:    &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "does not support extracting worktrees") {
		t.Fatalf("error = %v", err)
	}
}

type creatorOnly struct{}

func (creatorOnly) CreateWorktree(context.Context, capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	return capabilities.Worktree{}, nil
}

func TestParseWorktreeExtractArgs(t *testing.T) {
	parsed, err := parseWorktreeExtractArgs([]string{"extract", "--return-branch=main", "--add-parent", "--no-focus", "/repo", "/tmp/destination"})
	if err != nil {
		t.Fatalf("parseWorktreeExtractArgs() error = %v", err)
	}
	if parsed.ReturnBranch != "main" || !parsed.AddParent || !parsed.NoFocus || parsed.Repo != "/repo" || parsed.Destination != "/tmp/destination" {
		t.Fatalf("parsed = %#v", parsed)
	}
	bad := [][]string{
		{"extract", "/repo"},
		{"extract", "--unknown", "/repo", "/tmp/destination"},
		{"extract", "--return-branch", "/repo", "/tmp/destination"},
		{"extract", "--no-focus", "--no-focus", "/repo", "/tmp/destination"},
		{"create", "/repo", "/tmp/destination"},
	}
	for _, args := range bad {
		if _, err := parseWorktreeExtractArgs(args); err == nil {
			t.Fatalf("parseWorktreeExtractArgs(%v) error = nil", args)
		}
	}
}

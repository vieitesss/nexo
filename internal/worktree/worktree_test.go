package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
)

type fakeCreator struct {
	result capabilities.Worktree
	err    error
}

func (f fakeCreator) CreateWorktree(context.Context, capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	if f.err != nil {
		return capabilities.Worktree{}, f.err
	}
	return f.result, nil
}

type fakeRemover struct {
	removed capabilities.Worktree
}

func (f *fakeRemover) RemoveWorktree(_ context.Context, worktree capabilities.Worktree) error {
	f.removed = worktree
	return nil
}

type fakeExtractor struct {
	result capabilities.Worktree
	err    error
}

func (f fakeExtractor) ExtractWorktree(context.Context, capabilities.WorktreeExtractOptions) (capabilities.Worktree, error) {
	if f.err != nil {
		return capabilities.Worktree{}, f.err
	}
	return f.result, nil
}

type fakeExtractionRollback struct {
	rolledBack capabilities.Worktree
}

func (f *fakeExtractionRollback) RollbackExtractWorktree(_ context.Context, worktree capabilities.Worktree) error {
	f.rolledBack = worktree
	return nil
}

func TestValidateNormalizesDestinationAndPreservesRepositoryIdentity(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "checkout ")
	trimmedRepo := filepath.Join(root, "checkout")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(trimmedRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	request, err := Validate(Request{Repo: repo, Destination: " " + root + "/created"}, nil, root)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if request.Repo != repo || request.Destination != filepath.Join(root, "created") {
		t.Fatalf("Validate() request = %#v, want repo %q", request, repo)
	}
}

func TestValidateRequiresDestination(t *testing.T) {
	if _, err := Validate(Request{}, nil, t.TempDir()); !errors.Is(err, ErrDestinationRequired) {
		t.Fatalf("Validate() error = %v, want destination-required", err)
	}
}

func TestCreateRegistersParentAfterNativeSuccess(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	parent := filepath.Dir(configPath)
	result, err := Create(context.Background(), Options{
		Creator:    fakeCreator{result: capabilities.Worktree{Path: filepath.Join(parent, "created"), Branch: "feature"}},
		ConfigPath: configPath,
		Home:       t.TempDir(),
	}, Request{Repo: "/repo", Branch: "feature", Destination: filepath.Join(parent, "created"), AddParent: true})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(contents), parent) {
		t.Fatalf("config = %q, %v; want parent %q", contents, err, parent)
	}
	if result.Path == "" || result.Branch != "feature" {
		t.Fatalf("result = %#v", result)
	}
}

func TestCreateRollsBackConfigAndNativeResultOnOutputFailure(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	original := "paths = [\"/existing\"]\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	remover := &fakeRemover{}
	result, err := Create(context.Background(), Options{
		Creator:    fakeCreator{result: capabilities.Worktree{Path: filepath.Join(root, "created"), Branch: "feature"}},
		Remover:    remover,
		ConfigPath: configPath,
	}, Request{Repo: "/repo", Branch: "feature", Destination: filepath.Join(root, "created"), AddParent: true})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := result.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil || string(contents) != original {
		t.Fatalf("config after rollback = %q, %v; want original", contents, err)
	}
	if remover.removed.Path != filepath.Join(root, "created") {
		t.Fatalf("removed = %#v", remover.removed)
	}
}

func TestCreateLeavesConfigUnchangedWhenNativeCreationFails(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	original := "paths = [\"/existing\"]\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("create failed")
	_, err := Create(context.Background(), Options{
		Creator:    fakeCreator{err: wantErr},
		ConfigPath: configPath,
	}, Request{Destination: filepath.Join(root, "created"), AddParent: true})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Create() error = %v, want %v", err, wantErr)
	}
	contents, readErr := os.ReadFile(configPath)
	if readErr != nil || string(contents) != original {
		t.Fatalf("config after failed creation = %q, %v; want original", contents, readErr)
	}
}

func TestExtractUsesExtractionRollback(t *testing.T) {
	root := t.TempDir()
	rollback := &fakeExtractionRollback{}
	result, err := Extract(context.Background(), Options{
		Extractor:          fakeExtractor{result: capabilities.Worktree{Path: filepath.Join(root, "extracted"), Branch: "feature"}},
		ExtractionRollback: rollback,
	}, Request{Repo: "/repo", Destination: filepath.Join(root, "extracted")})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if err := result.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if rollback.rolledBack.Branch != "feature" {
		t.Fatalf("rolled back = %#v", rollback.rolledBack)
	}
}

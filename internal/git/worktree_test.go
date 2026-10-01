package git

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls   []string
	outputs map[string][]byte
	errors  map[string]error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if err := f.errors[key]; err != nil {
		return nil, err
	}
	if output, ok := f.outputs[key]; ok {
		return output, nil
	}
	return nil, errors.New("unexpected command " + key)
}

func TestCurrentBranch(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{"git -C /repo branch --show-current": []byte("feature\n")}}
	got, err := New(runner).CurrentBranch(context.Background(), "/repo")
	if err != nil || got != "feature" {
		t.Fatalf("CurrentBranch() = %q, %v", got, err)
	}
}

func TestIsClean(t *testing.T) {
	clean := &fakeRunner{outputs: map[string][]byte{"git -C /repo status --porcelain": []byte("")}}
	if ok, err := New(clean).IsClean(context.Background(), "/repo"); err != nil || !ok {
		t.Fatalf("IsClean() = %t, %v", ok, err)
	}
	dirty := &fakeRunner{outputs: map[string][]byte{"git -C /repo status --porcelain": []byte(" M README\n")}}
	if ok, err := New(dirty).IsClean(context.Background(), "/repo"); err != nil || ok {
		t.Fatalf("IsClean() = %t, %v, want dirty", ok, err)
	}
}

func TestBranchOwnersParsesPorcelain(t *testing.T) {
	list := "worktree /repo\nHEAD aaaa\nbranch refs/heads/feature\n\n" +
		"worktree /elsewhere\nHEAD bbbb\nbranch refs/heads/occupied\n\n" +
		"worktree /detached\nHEAD cccc\ndetached\n"
	runner := &fakeRunner{outputs: map[string][]byte{"git -C /repo worktree list --porcelain": []byte(list)}}
	commands := New(runner)
	got, err := commands.BranchOwners(context.Background(), "/repo", "feature")
	if err != nil {
		t.Fatalf("BranchOwners() error = %v", err)
	}
	if want := []string{"/repo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BranchOwners(feature) = %v, want %v", got, want)
	}
	got, err = commands.BranchOwners(context.Background(), "/repo", "occupied")
	if err != nil {
		t.Fatalf("BranchOwners() error = %v", err)
	}
	if want := []string{"/elsewhere"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BranchOwners(occupied) = %v, want %v", got, want)
	}
	if got, _ := commands.BranchOwners(context.Background(), "/repo", "main"); len(got) != 0 {
		t.Fatalf("BranchOwners(main) = %v, want none", got)
	}
}

func TestPreviousBranchTreatsDetachedAndFailureAsEmpty(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string][]byte{"git -C /repo rev-parse --abbrev-ref @{-1}": []byte("main\n")},
		errors:  map[string]error{"git -C /repo rev-parse --abbrev-ref @{-1}": errors.New("boom")},
	}
	// Errors take precedence in the fake, so exercise the detached handling
	// through a separate runner.
	if got, err := New(runner).PreviousBranch(context.Background(), "/repo"); err != nil || got != "" {
		t.Fatalf("PreviousBranch() on failure = %q, %v, want empty", got, err)
	}
	detached := &fakeRunner{outputs: map[string][]byte{"git -C /repo rev-parse --abbrev-ref @{-1}": []byte("HEAD\n")}}
	if got, err := New(detached).PreviousBranch(context.Background(), "/repo"); err != nil || got != "" {
		t.Fatalf("PreviousBranch() on detached = %q, %v, want empty", got, err)
	}
}

func TestDefaultBranchFallsBackInOrder(t *testing.T) {
	withRemote := &fakeRunner{outputs: map[string][]byte{
		"git -C /repo symbolic-ref --short refs/remotes/origin/HEAD": []byte("origin/trunk\n"),
	}}
	if got, err := New(withRemote).DefaultBranch(context.Background(), "/repo"); err != nil || got != "trunk" {
		t.Fatalf("DefaultBranch() = %q, %v, want trunk", got, err)
	}

	configured := &fakeRunner{
		errors:  map[string]error{"git -C /repo symbolic-ref --short refs/remotes/origin/HEAD": errors.New("no remote")},
		outputs: map[string][]byte{"git -C /repo config --get init.defaultBranch": []byte("develop\n")},
	}
	if got, err := New(configured).DefaultBranch(context.Background(), "/repo"); err != nil || got != "develop" {
		t.Fatalf("DefaultBranch() = %q, %v, want develop", got, err)
	}

	conventional := &fakeRunner{
		errors: map[string]error{
			"git -C /repo symbolic-ref --short refs/remotes/origin/HEAD": errors.New("no remote"),
			"git -C /repo config --get init.defaultBranch":               errors.New("unset"),
			"git -C /repo show-ref --verify --quiet refs/heads/main":     errors.New("missing"),
		},
		outputs: map[string][]byte{"git -C /repo show-ref --verify --quiet refs/heads/master": []byte("")},
	}
	if got, err := New(conventional).DefaultBranch(context.Background(), "/repo"); err != nil || got != "master" {
		t.Fatalf("DefaultBranch() = %q, %v, want master", got, err)
	}
}

func TestBranchExistsAndCheckout(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"git -C /repo show-ref --verify --quiet refs/heads/main": []byte(""),
		"git -C /repo checkout main":                             []byte(""),
	}}
	commands := New(runner)
	if ok, err := commands.BranchExists(context.Background(), "/repo", "main"); err != nil || !ok {
		t.Fatalf("BranchExists(main) = %t, %v", ok, err)
	}
	if ok, err := commands.BranchExists(context.Background(), "/repo", "missing"); err != nil || ok {
		t.Fatalf("BranchExists(missing) = %t, %v", ok, err)
	}
	if err := commands.Checkout(context.Background(), "/repo", "main"); err != nil {
		t.Fatalf("Checkout() error = %v", err)
	}
	if runner.calls[len(runner.calls)-1] != "git -C /repo checkout main" {
		t.Fatalf("last call = %q", runner.calls[len(runner.calls)-1])
	}
}

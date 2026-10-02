package clone

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/runner"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls []call
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, call{name: name, args: append([]string(nil), args...)})
	return nil, nil
}

func TestSourceOperand(t *testing.T) {
	home := "/home/tester"
	for _, test := range []struct {
		name   string
		source string
		prefix string
		want   string
	}{
		{name: "repository shorthand", source: "owner/repo", prefix: "git@github.com:", want: "git@github.com:owner/repo"},
		{name: "url", source: "https://example.test/repo.git", prefix: "git@github.com:", want: "https://example.test/repo.git"},
		{name: "scp", source: "git@example.test:team/repo", prefix: "https://example.test/", want: "git@example.test:team/repo"},
		{name: "home path", source: "~/src/repo.git", prefix: "git@github.com:", want: "/home/tester/src/repo.git"},
		{name: "bare git path", source: "repo.git", prefix: "git@github.com:", want: "repo.git"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := SourceOperand(test.source, test.prefix, home); got != test.want {
				t.Fatalf("SourceOperand() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCloneUsesResolvedDestinationAndOperand(t *testing.T) {
	runner := &fakeRunner{}
	root := t.TempDir()
	result, err := Clone(context.Background(), Options{Runner: runner, Home: root}, Request{
		Source:      "owner/repo",
		Prefix:      "git@example.test:",
		Destination: root + "/repo",
	})
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}
	if result.Destination != root+"/repo" || result.Source != "owner/repo" {
		t.Fatalf("Clone() result = %#v", result)
	}
	want := []call{{name: "git", args: []string{"clone", "--", "git@example.test:owner/repo", root + "/repo"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Git calls = %#v, want %#v", runner.calls, want)
	}
}

func TestCloneRejectsMissingRunner(t *testing.T) {
	_, err := Clone(context.Background(), Options{}, Request{Source: "owner/repo"})
	if err == nil || !strings.Contains(err.Error(), "command runner") {
		t.Fatalf("Clone() error = %v, want missing-runner error", err)
	}
}

var _ runner.CommandRunner = (*fakeRunner)(nil)

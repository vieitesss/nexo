package picker

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/runner"
)

func TestGitContextLoaderUsesOnlyLocalGitCommands(t *testing.T) {
	var calls []string
	r := runner.Func(func(_ context.Context, command string, args ...string) ([]byte, error) {
		call := strings.Join(append([]string{command}, args...), " ")
		calls = append(calls, call)
		switch call {
		case "git -C /repo status --porcelain=v2 --branch":
			return []byte("# branch.head main\n# branch.ab +1 -2\n1 .M N... 100644 100644 100644 a b file\n"), nil
		case "git -C /repo diff --numstat HEAD --":
			return []byte("3\t1\tfile\n"), nil
		case "git -C /repo rev-parse --git-common-dir":
			return []byte(".git\n"), nil
		case "git -C /repo rev-parse --show-toplevel":
			return []byte("/repo\n"), nil
		default:
			return nil, nil
		}
	})
	value, err := NewGitContextLoader(r).Load(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if value.Identity != "main" || value.Adds != 3 || value.Deletes != 1 || value.Ahead != 1 || value.Behind != 2 {
		t.Fatalf("context = %#v", value)
	}
	for _, call := range calls {
		if strings.Contains(call, "fetch") {
			t.Fatalf("unexpected fetch command %q", call)
		}
	}
}

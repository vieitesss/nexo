package sessionname

import (
	"context"
	"errors"
	"github.com/vieitesss/nexo/internal/runner"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type testRunner func(context.Context, string, ...string) ([]byte, error)

func (f testRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

func marker(t *testing.T, path, name string) {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, name), 0700); err != nil {
		t.Fatal(err)
	}
}
func gitFixture(t *testing.T) (string, testRunner) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "acme", "api")
	marker(t, path, ".git")
	return path, testRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "git" {
			t.Fatalf("unexpected executable %s", name)
		}
		switch strings.Join(args[2:], " ") {
		case "rev-parse --show-toplevel":
			return []byte(path + "\n"), nil
		case "remote":
			return []byte("origin\n"), nil
		case "remote get-url --all -- origin":
			return []byte("https://host/remote/platform/service.git\n"), nil
		}
		t.Fatalf("unexpected git call %v", args)
		return nil, nil
	})
}
func resolveWant(t *testing.T, path, template, want string, r testRunner) {
	t.Helper()
	var commandRunner runner.CommandRunner
	if r != nil {
		commandRunner = r
	}
	got, err := Resolve(context.Background(), path, template, commandRunner)
	if err != nil || got != want {
		t.Fatalf("Resolve(%q) = %q, %v; want %q", template, got, err, want)
	}
}
func TestResolveFilesystemFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme", "api")
	for template, want := range map[string]string{"{org}/{team}/{repo}": "acme/api", "{org}-{team}-{repo}": "acme-api", "{org}_{team}_{repo}": "acme_api"} {
		resolveWant(t, path, template, want, nil)
	}
	marker(t, path, ".git")
	for name, response := range map[string]string{"command error": "error", "malformed output": "garbage", "local URL": "/local/api", "deep namespace": "https://host/a/b/c/d", "multiple URLs": "https://host/a/api\nhttps://host/b/api"} {
		t.Run(name, func(t *testing.T) {
			r := testRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
				switch strings.Join(args[2:], " ") {
				case "rev-parse --show-toplevel":
					return []byte(path), nil
				case "remote":
					return []byte("origin\n"), nil
				default:
					if response == "error" {
						return nil, errors.New("https://secret:password@host/a/api")
					}
					return []byte(response), nil
				}
			})
			resolveWant(t, path, "{org}/{team}/{repo}", "acme/api", r)
		})
	}
	resolveWant(t, path, "{org}/{repo}", "acme/api", nil)
}
func TestResolveRootOwnership(t *testing.T) {
	path, r := gitFixture(t)
	resolveWant(t, path, "{org}/{team}/{repo}", "remote/platform/service", r)
	if err := os.RemoveAll(filepath.Join(path, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: elsewhere"), 0600); err != nil {
		t.Fatal(err)
	}
	resolveWant(t, path, "{org}/{repo}", "remote/service", r)
	sub := filepath.Join(path, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	resolveWant(t, sub, "{org}/{repo}", "api/sub", r)
	foreign := testRunner(func(ctx context.Context, n string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "rev-parse") {
			return []byte(t.TempDir()), nil
		}
		return r.Run(ctx, n, args...)
	})
	resolveWant(t, path, "{org}/{repo}", "acme/api", foreign)
	link := filepath.Join(t.TempDir(), "alias", "api")
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	var opened string
	aliasRunner := testRunner(func(ctx context.Context, n string, args ...string) ([]byte, error) {
		opened = args[1]
		return r.Run(ctx, n, append([]string{"-C", path}, args[2:]...)...)
	})
	resolveWant(t, link, "{org}/{repo}", "remote/service", aliasRunner)
	if opened != link {
		t.Fatalf("command path %q, want lexical %q", opened, link)
	}
	if _, err := Resolve(context.Background(), string(filepath.Separator), "{org}/{repo}", nil); err == nil {
		t.Fatal("root with missing values accepted")
	}
	got, err := Resolve(context.Background(), filepath.Join(string(filepath.Separator), "api"), "{repo}", nil)
	if err != nil || got != "api" {
		t.Fatalf("repo-only filesystem edge = %q, %v", got, err)
	}
}
func TestResolveGitSelection(t *testing.T) {
	path, _ := gitFixture(t)
	cases := []struct{ name, remotes, urls, want string }{
		{"origin wins", "origin\nupstream\n", "https://host/fork/api.git\n", "fork/api"},
		{"sole remote", "upstream\n", "https://host/solo/api\n", "solo/api"},
		{"ambiguous", "one\ntwo\n", "", "acme/api"},
		{"malformed origin", "origin\nupstream\n", "bad", "acme/api"},
		{"two fetch urls", "origin\n", "https://host/a/api\nhttps://host/b/api\n", "acme/api"},
		{"push irrelevant", "origin\n", "", "acme/api"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			r := testRunner(func(_ context.Context, n string, args ...string) ([]byte, error) {
				calls = append(calls, strings.Join(args, " "))
				if n != "git" {
					t.Fatal(n)
				}
				switch strings.Join(args[2:], " ") {
				case "rev-parse --show-toplevel":
					return []byte(path), nil
				case "remote":
					return []byte(tc.remotes), nil
				case "remote get-url --all -- origin", "remote get-url --all -- upstream":
					return []byte(tc.urls), nil
				}
				t.Fatalf("unexpected command %v", args)
				return nil, nil
			})
			resolveWant(t, path, "{org}/{repo}", tc.want, r)
			for _, call := range calls {
				if strings.Contains(call, "push") || (strings.Contains(call, "upstream") && strings.Contains(tc.remotes, "origin")) {
					t.Fatalf("incorrect remote selection %v", calls)
				}
			}
		})
	}
}
func TestResolveGitFetchURLRecords(t *testing.T) {
	for _, vcs := range []string{"git", "jj"} {
		t.Run(vcs, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "local", "checkout")
			marker(t, path, "."+vcs)
			gitDir := filepath.Join(path, "backing.git")
			for _, tc := range []struct{ name, output, want string }{
				{"no EOF newline", "https://host/remote/service.git", "remote/service"},
				{"EOF newline", "https://host/remote/service.git\n", "remote/service"},
				{"CRLF", "https://host/remote/service.git\r\n", "remote/service"},
				{"empty", "", "local/checkout"},
				{"empty record", "\n", "local/checkout"},
				{"leading empty record", "\nhttps://host/remote/service.git\n", "local/checkout"},
				{"trailing empty record", "https://host/remote/service.git\n\n", "local/checkout"},
				{"leading empty CRLF record", "\r\nhttps://host/remote/service.git\r\n", "local/checkout"},
				{"trailing empty CRLF record", "https://host/remote/service.git\r\n\r\n", "local/checkout"},
				{"whitespace record", "https://host/remote/service.git\n \n", "local/checkout"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					r := testRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
						if name == "jj" {
							switch strings.Join(args[5:], " ") {
							case "root":
								return []byte(path + "\r\n"), nil
							case "git root":
								return []byte(gitDir + "\r\n"), nil
							}
						}
						if name == "git" {
							command := args[2:]
							if vcs == "jj" {
								if args[2] != "--git-dir="+gitDir {
									t.Fatalf("unexpected backing Git directory: %q", args[2])
								}
								command = args[3:]
							}
							switch strings.Join(command, " ") {
							case "rev-parse --show-toplevel":
								return []byte(path + "\r\n"), nil
							case "remote":
								return []byte("origin\r\n"), nil
							case "remote get-url --all -- origin":
								return []byte(tc.output), nil
							}
						}
						t.Fatalf("unexpected command %s %v", name, args)
						return nil, nil
					})
					resolveWant(t, path, "{org}/{repo}", tc.want, r)
				})
			}
		})
	}
}

func TestResolveMercurialSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme", "api")
	marker(t, path, ".hg")
	for _, tc := range []struct{ name, output, want string }{
		{"default and pushurl", `[{"name":"default","url":"https://host/remote/api","pushurl":"https://host/push/api"}]`, "remote/api"},
		{"resolved alias", `[{"name":"default","url":"ssh://user@host/acme/platform/api"}]`, "acme/platform/api"},
		{"JSON whitespace", " \r\n[{\"name\":\"default\",\"url\":\"https://host/remote/api\"}]\r\n ", "remote/api"},
		{"missing", `[]`, "acme/api"}, {"push only", `[{"name":"default","pushurl":"https://host/a/api"}]`, "acme/api"},
		{"bad JSON", `{`, "acme/api"}, {"multi", `[{"url":"https://host/a/api"},{"url":"https://host/b/api"}]`, "acme/api"},
		{"local", `[{"url":"/local/api"}]`, "acme/api"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRunner(func(_ context.Context, n string, args ...string) ([]byte, error) {
				if n != "hg" {
					t.Fatal(n)
				}
				switch strings.Join(args, " ") {
				case "--noninteractive --cwd " + path + " root":
					return []byte(path), nil
				case "--noninteractive --cwd " + path + " paths default -Tjson":
					return []byte(tc.output), nil
				}
				t.Fatalf("unexpected %v", args)
				return nil, nil
			})
			resolveWant(t, path, "{org}/{team}/{repo}", tc.want, r)
		})
	}
}
func TestResolveJujutsuSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme", "api")
	marker(t, path, ".jj")
	marker(t, path, ".git")
	gitDir := filepath.Join(path, "backing.git")
	var calls []string
	r := testRunner(func(_ context.Context, n string, args ...string) ([]byte, error) {
		calls = append(calls, n+" "+strings.Join(args, " "))
		if n == "jj" {
			if strings.HasSuffix(strings.Join(args, " "), "git root") {
				return []byte(gitDir + "\n"), nil
			}
			if args[len(args)-1] == "root" {
				return []byte(path + "\n"), nil
			}
			t.Fatalf("unexpected jj %v", args)
		}
		if n == "git" {
			switch strings.Join(args[3:], " ") {
			case "remote":
				return []byte("origin\n"), nil
			case "remote get-url --all -- origin":
				return []byte("https://host/a/api\nhttps://host/b/api\n"), nil
			}
			t.Fatalf("unexpected git %v", args)
		}
		t.Fatal(n)
		return nil, nil
	})
	resolveWant(t, path, "{org}/{repo}", "acme/api", r)
	for _, call := range calls {
		if strings.HasPrefix(call, "jj ") && (!strings.Contains(call, "--ignore-working-copy") || !strings.Contains(call, "--color=never") || !strings.Contains(call, "--no-pager") || !strings.Contains(call, "-R "+path)) {
			t.Fatalf("unsafe jj command: %s", call)
		}
		if strings.HasPrefix(call, "git ") && !strings.Contains(call, "--git-dir="+gitDir) {
			t.Fatalf("missing git directory: %s", call)
		}
	}
	valid := testRunner(func(ctx context.Context, n string, args ...string) ([]byte, error) {
		if n == "git" && strings.HasSuffix(strings.Join(args, " "), "remote get-url --all -- origin") {
			return []byte("https://host/remote/api.git\n"), nil
		}
		return r.Run(ctx, n, args...)
	})
	resolveWant(t, path, "{org}/{repo}", "remote/api", valid)
	unavailable := testRunner(func(_ context.Context, n string, _ ...string) ([]byte, error) {
		if n == "git" {
			t.Fatal("fell back to colocated Git after jj failed")
		}
		return nil, errors.New("jj unavailable")
	})
	resolveWant(t, path, "{org}/{repo}", "acme/api", unavailable)
}
func TestResolveTimeoutAndCancellation(t *testing.T) {
	path, _ := gitFixture(t)
	start := time.Now()
	var deadlines []time.Time
	r := testRunner(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		d, ok := ctx.Deadline()
		if !ok {
			t.Fatal("missing deadline")
		}
		deadlines = append(deadlines, d)
		if d.After(start.Add(time.Second + 100*time.Millisecond)) {
			t.Fatal("deadline too late")
		}
		return nil, errors.New("metadata unavailable")
	})
	resolveWant(t, path, "{org}/{repo}", "acme/api", r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Resolve(ctx, path, "{org}/{repo}", r); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := Resolve(expired, path, "{org}/{repo}", r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired: %v", err)
	}
	wait := testRunner(func(ctx context.Context, _ string, _ ...string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() })
	resolveWant(t, path, "{org}/{repo}", "acme/api", wait)
	if len(deadlines) == 0 {
		t.Fatal("reader not called")
	}
	var shared []time.Time
	root, _ := gitFixture(t)
	multi := testRunner(func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		d, _ := ctx.Deadline()
		shared = append(shared, d)
		switch strings.Join(args[2:], " ") {
		case "rev-parse --show-toplevel":
			return []byte(root), nil
		case "remote":
			return []byte("origin"), nil
		default:
			return []byte("https://host/remote/api"), nil
		}
	})
	resolveWant(t, root, "{org}/{repo}", "remote/api", multi)
	if len(shared) != 3 || !shared[0].Equal(shared[1]) || !shared[1].Equal(shared[2]) {
		t.Fatalf("metadata subprocess deadlines changed: %v", shared)
	}
}
func TestResolveDoesNotMutateOrContactNetwork(t *testing.T) {
	path, _ := gitFixture(t)
	var calls []string
	r := testRunner(func(_ context.Context, n string, args ...string) ([]byte, error) {
		calls = append(calls, n+" "+strings.Join(args, " "))
		switch strings.Join(args[2:], " ") {
		case "rev-parse --show-toplevel":
			return []byte(path), nil
		case "remote":
			return []byte("origin"), nil
		case "remote get-url --all -- origin":
			return nil, errors.New("https://user:secret@host/a/api")
		}
		return nil, errors.New("unknown")
	})
	got, err := Resolve(context.Background(), path, "{org}/{repo}", r)
	if got != "acme/api" || err != nil {
		t.Fatalf("got %s, %v", got, err)
	}
	for _, call := range calls {
		for _, bad := range []string{"fetch", "clone", "push", "import", "snapshot", "safe.directory", "--trust"} {
			if strings.Contains(call, bad) {
				t.Fatalf("unsafe call: %s", call)
			}
		}
	}
	if !reflect.DeepEqual(calls, []string{"git -C " + path + " rev-parse --show-toplevel", "git -C " + path + " remote", "git -C " + path + " remote get-url --all -- origin"}) {
		t.Fatalf("calls: %v", calls)
	}
}

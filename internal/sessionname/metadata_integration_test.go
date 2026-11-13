package sessionname

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Each command gets a private environment; parallel tests never change process env.
func nativeMetadataRunner(t *testing.T, tools ...string) testRunner {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed; native naming metadata integration unavailable", tool)
		}
	}
	home := t.TempDir()
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "HOME" || strings.HasPrefix(key, "XDG_") || strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, "HG") || strings.HasPrefix(key, "JJ_") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "HOME="+home, "XDG_CONFIG_HOME="+home, "XDG_CACHE_HOME="+home,
		"XDG_STATE_HOME="+home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(home, "gitconfig"),
		"HGRCPATH=", "HGPLAIN=1", "JJ_CONFIG="+filepath.Join(home, "jj.toml"))
	if err := os.WriteFile(filepath.Join(home, "jj.toml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return testRunner(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = env
		return cmd.Output()
	})
}

func nativeMetadataCommand(t *testing.T, r testRunner, name string, args ...string) string {
	t.Helper()
	out, err := r.Run(context.Background(), name, args...)
	if err != nil {
		t.Fatalf("fixture command %s %v: %v", name, args, err)
	}
	return strings.TrimSpace(string(out))
}

// Fingerprint every metadata file and the dirty working file, including operation state.
func metadataFingerprint(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var contents []byte
		if info.Mode()&os.ModeSymlink != 0 {
			target, linkErr := os.Readlink(path)
			err, contents = linkErr, []byte(target)
		} else {
			contents, err = os.ReadFile(path)
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[rel] = fmt.Sprintf("%v %d %x", info.Mode(), info.ModTime().UnixNano(), sha256.Sum256(contents))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertNativeMetadataRead(t *testing.T, path, want string, r testRunner) {
	t.Helper()
	dirty := filepath.Join(path, "dirty.txt")
	if err := os.WriteFile(dirty, []byte("unsnapshotted local edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := metadataFingerprint(t, path)
	resolveWant(t, path, "{org}/{team}/{repo}", want, r)
	if after := metadataFingerprint(t, path); !reflect.DeepEqual(before, after) {
		t.Fatalf("naming discovery mutated repository/dirty-file fingerprint\nbefore: %v\nafter: %v", before, after)
	}
}

func TestGitMetadataIntegration(t *testing.T) {
	t.Parallel()
	r := nativeMetadataRunner(t, "git")
	path := filepath.Join(t.TempDir(), "local", "checkout")
	nativeMetadataCommand(t, r, "git", "init", "-q", path)
	nativeMetadataCommand(t, r, "git", "-C", path, "remote", "add", "origin", "https://example.invalid/remote/platform/service.git")
	nativeMetadataCommand(t, r, "git", "-C", path, "remote", "add", "upstream", "https://example.invalid/upstream/api.git")
	nativeMetadataCommand(t, r, "git", "-C", path, "remote", "set-url", "--push", "origin", "https://example.invalid/push/api.git")
	assertNativeMetadataRead(t, path, "remote/platform/service", r)
	nativeMetadataCommand(t, r, "git", "-C", path, "config", "--add", "remote.origin.url", "https://example.invalid/other/service.git")
	assertNativeMetadataRead(t, path, "local/checkout", r)
}

func TestMercurialMetadataIntegration(t *testing.T) {
	t.Parallel()
	r := nativeMetadataRunner(t, "hg")
	path := filepath.Join(t.TempDir(), "local", "checkout")
	nativeMetadataCommand(t, r, "hg", "init", path)
	for _, tc := range []struct {
		name, paths, want string
		count             int
	}{
		{"resolved alias", "source = https://example.invalid/remote/platform/service\ndefault = path://source\ndefault:pushurl = https://example.invalid/push/api\n", "remote/platform/service", 1},
		{"multiple URLs", "default = https://example.invalid/one/api, https://example.invalid/two/api\ndefault:multi-urls = yes\n", "local/checkout", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(path, ".hg", "hgrc"), []byte("[paths]\n"+tc.paths), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := r.Run(context.Background(), "hg", "--noninteractive", "--cwd", path, "paths", "default", "-Tjson")
			var paths []struct {
				URL string `json:"url"`
			}
			if err != nil || json.Unmarshal(out, &paths) != nil || len(paths) != tc.count || (tc.count == 1 && paths[0].URL != "https://example.invalid/remote/platform/service") {
				assertNativeMetadataRead(t, path, "local/checkout", r)
				t.Skip("installed hg does not support the required resolved paths JSON/alias/multi-URL output; filesystem fallback verified")
			}
			assertNativeMetadataRead(t, path, tc.want, r)
		})
	}
}

func TestJujutsuMetadataIntegration(t *testing.T) {
	t.Parallel()
	for _, layout := range []string{"--colocate", "--no-colocate"} {
		t.Run(layout, func(t *testing.T) {
			r := nativeMetadataRunner(t, "jj", "git")
			path := filepath.Join(t.TempDir(), "local", "checkout")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			nativeMetadataCommand(t, r, "jj", "git", "init", layout, path)
			args := []string{"--ignore-working-copy", "--color=never", "--no-pager", "-R", path}
			root, rootErr := r.Run(context.Background(), "jj", append(args, "root")...)
			gitRoot, gitErr := r.Run(context.Background(), "jj", append(args, "git", "root")...)
			gitDir := strings.TrimSpace(string(gitRoot))
			if rootErr != nil || gitErr != nil || !filepath.IsAbs(gitDir) {
				assertNativeMetadataRead(t, path, "local/checkout", r)
				t.Skip("installed jj lacks supported documented root/git root output; filesystem fallback verified")
			}
			actual, err := filepath.EvalSymlinks(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(root)) != actual {
				t.Fatalf("jj root = %q; want candidate %q", root, actual)
			}
			_, gitMarkerErr := os.Stat(filepath.Join(path, ".git"))
			if (layout == "--colocate" && gitMarkerErr != nil) || (layout == "--no-colocate" && !os.IsNotExist(gitMarkerErr)) {
				t.Fatalf("unexpected %s layout .git marker: %v", layout, gitMarkerErr)
			}
			nativeMetadataCommand(t, r, "git", "--git-dir="+gitDir, "remote", "add", "origin", "https://example.invalid/remote/platform/service.git")
			assertNativeMetadataRead(t, path, "remote/platform/service", r)
			nativeMetadataCommand(t, r, "git", "--git-dir="+gitDir, "config", "--add", "remote.origin.url", "https://example.invalid/other/service.git")
			assertNativeMetadataRead(t, path, "local/checkout", r)
		})
	}
}

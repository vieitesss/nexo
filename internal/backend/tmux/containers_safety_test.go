package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectActiveBlocksDescendantAndPhysicalAliasButNotLinkTarget(t *testing.T) {
	setSafetyEnv(t)
	root := t.TempDir()
	project := filepath.Join(root, "project")
	child := filepath.Join(project, "src")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cases := []struct {
		name string
		pane string
		want bool
	}{
		{name: "descendant", pane: child, want: true},
		{name: "physical alias", pane: filepath.Join(alias, "src"), want: true},
		{name: "trailing space is distinct", pane: project + " ", want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			backend := New(funcRunner(func(context.Context, string, ...string) ([]byte, error) {
				return []byte(test.pane + "\n"), nil
			}))
			got, err := backend.ProjectActive(context.Background(), project)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("ProjectActive() = %v, want %v", got, test.want)
			}
		})
	}

	linkBackend := New(funcRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(filepath.Join(project, "src") + "\n"), nil
	}))
	linkActive, err := linkBackend.ProjectActive(context.Background(), alias)
	if err != nil {
		t.Fatal(err)
	}
	if linkActive {
		t.Fatal("link target usage made link deletion active")
	}
}

func TestProjectActivePreservesEmbeddedNewlineAndCarriageReturnPaths(t *testing.T) {
	setSafetyEnv(t)
	root := t.TempDir()
	newlineProject := filepath.Join(root, "project\nname")
	if err := os.MkdirAll(filepath.Join(newlineProject, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	carriageProject := filepath.Join(root, "project\r")
	if err := os.MkdirAll(filepath.Join(carriageProject, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		project string
		pane    string
	}{
		{name: "newline", project: newlineProject, pane: filepath.Join(newlineProject, "src")},
		{name: "trailing carriage return", project: carriageProject, pane: filepath.Join(carriageProject, "src")},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := New(funcRunner(func(context.Context, string, ...string) ([]byte, error) {
				return []byte(tmuxQuotePath(test.pane) + "\n"), nil
			}))
			active, err := backend.ProjectActive(context.Background(), test.project)
			if err != nil {
				t.Fatal(err)
			}
			if !active {
				t.Fatalf("ProjectActive() = false for pane %q", test.pane)
			}
		})
	}
	unsafe := New(funcRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(newlineProject + "\n"), nil
	}))
	if _, err := unsafe.ProjectActive(context.Background(), newlineProject); err == nil {
		t.Fatal("ProjectActive() error = nil for unencoded embedded-newline output")
	}
}

func tmuxQuotePath(path string) string {
	var quoted strings.Builder
	quoted.WriteByte('\'')
	for _, value := range path {
		switch value {
		case '\\':
			quoted.WriteString("\\\\")
		case '\n':
			quoted.WriteString("\\n")
		case '\r':
			quoted.WriteString("\\r")
		case '\'':
			quoted.WriteString("\\'")
		default:
			quoted.WriteRune(value)
		}
	}
	quoted.WriteByte('\'')
	return quoted.String()
}

func TestProjectActiveFailsClosedWithoutRunner(t *testing.T) {
	setSafetyEnv(t)
	var backend *Backend
	if _, err := backend.ProjectActive(context.Background(), "/tmp/project"); err == nil {
		t.Fatal("ProjectActive() error = nil without backend runner")
	}
}

func setSafetyEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
}

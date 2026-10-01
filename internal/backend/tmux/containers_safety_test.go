package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectActiveUsesActualTmuxEscapingAndSharedPathPolicy(t *testing.T) {
	setSafetyEnv(t)
	root := t.TempDir()
	for _, test := range []struct {
		name string
		part string
	}{
		{name: "space", part: "repo space"},
		{name: "backslash", part: `repo\\slash`},
		{name: "quote", part: `repo"quote`},
		{name: "carriage return", part: "repo\r"},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := filepath.Join(root, test.part)
			child := filepath.Join(project, "src")
			if err := os.MkdirAll(child, 0o755); err != nil {
				t.Fatal(err)
			}
			backend := New(panePathRunner("%1\n", "%1\t"+tmuxActualQuote(child)+"\n"))
			active, err := backend.ProjectActive(context.Background(), project)
			if err != nil || !active {
				t.Fatalf("ProjectActive() = %v, %v; want descendant active", active, err)
			}
			exactBackend := New(panePathRunner("%1\n", "%1\t"+tmuxActualQuote(project)+"\n"))
			active, err = exactBackend.ProjectActive(context.Background(), project)
			if err != nil || !active {
				t.Fatalf("exact ProjectActive() = %v, %v; want active", active, err)
			}
		})
	}
}

func TestProjectActiveFailsClosedWhenPathRecordsDoNotMatchLivePanes(t *testing.T) {
	setSafetyEnv(t)
	root := t.TempDir()
	project := filepath.Join(root, "p", "a\n%999\t/x")
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, runner := range map[string]funcRunner{
		// A live pane whose cwd contains a newline, percent sign and tab prints
		// as two well-formed records. The forged second record is not a live
		// pane, so the real cwd must not be observed.
		"forged record": panePathRunner(
			"%1\n",
			"%1\t"+filepath.Join(root, "p")+"\n%999\t/x\n",
		),
		// A pane that disappeared between the two listings cannot be attributed
		// to a path either.
		"pane vanished between listings": panePathRunner(
			"%1\n%2\n",
			"%1\t"+filepath.Join(root, "p")+"\n",
		),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(runner).ProjectActive(context.Background(), project); err == nil {
				t.Fatal("ProjectActive() error = nil for path records that do not match the live panes")
			}
		})
	}
}

func TestProjectActiveFailsClosedForLiteralNewlineRecord(t *testing.T) {
	setSafetyEnv(t)
	root := t.TempDir()
	project := filepath.Join(root, "repo\nname")
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	backend := New(panePathRunner("%1\n", "%1\t"+filepath.Join(project, "src")+"\n"))
	if _, err := backend.ProjectActive(context.Background(), project); err == nil {
		t.Fatal("ProjectActive() error = nil for an unparseable newline record")
	}
}

func TestProjectActiveFailsClosedForNewlineTabNativeRecord(t *testing.T) {
	setSafetyEnv(t)
	root := t.TempDir()
	project := filepath.Join(root, "repo\n%99\tother")
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	// This is the literal native output from tmux q: for a path containing a
	// newline, percent sign and tab. The continuation looks like a second record
	// unless pane IDs and absolute paths are validated.
	backend := New(panePathRunner("%7\n", "%7\t"+filepath.Join(root, "repo")+"\n\\%99\tother/src\n"))
	if _, err := backend.ProjectActive(context.Background(), project); err == nil {
		t.Fatal("ProjectActive() error = nil for a newline/tab-framing ambiguity")
	}
}

func TestDecodePanePathRecordsRejectsEmbeddedTabAndRelativeContinuation(t *testing.T) {
	for name, output := range map[string]string{
		"embedded tab":      "%1\t/tmp/repo\tchild\n",
		"trailing newline":  "%1\t/tmp/repo\n\n",
		"relative path":     "%1\tchild/src\n",
		"malformed pane id": "pane\t/tmp/repo\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodePanePathRecords(output); err == nil {
				t.Fatalf("decodePanePathRecords(%q) error = nil", output)
			}
		})
	}
}

// panePathRunner answers the two listings ProjectActive issues: the
// authoritative pane ID list and the pane ID/path records. The formats are
// distinguished by their final format argument.
func panePathRunner(ids, records string) funcRunner {
	return funcRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[len(args)-1] == "#{pane_id}" {
			return []byte(ids), nil
		}
		return []byte(records), nil
	})
}

func tmuxActualQuote(path string) string {
	var quoted strings.Builder
	for _, value := range path {
		switch value {
		case ' ', '\\', '"':
			quoted.WriteByte('\\')
			quoted.WriteRune(value)
		default:
			quoted.WriteRune(value)
		}
	}
	return quoted.String()
}

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
	backend := New(panePathRunner("%1\n", "%1\t"+tmuxActualQuote(filepath.Join(alias, "src"))+"\n"))
	active, err := backend.ProjectActive(context.Background(), project)
	if err != nil || !active {
		t.Fatalf("physical alias ProjectActive() = %v, %v", active, err)
	}
	linkBackend := New(panePathRunner("%1\n", "%1\t"+tmuxActualQuote(filepath.Join(project, "src"))+"\n"))
	active, err = linkBackend.ProjectActive(context.Background(), alias)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("link target usage made link deletion active")
	}
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

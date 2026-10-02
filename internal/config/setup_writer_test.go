package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeSetupPreservesCommentsAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "# keep\nbackend = \"tmux\"\n[agents]\n# notify choice\nnotify = false\n[remote]\nmachines = [\"old\"]\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	notify := true
	changed, err := MergeSetup(path, SetupValues{Notify: &notify, Machines: []string{"devbox"}, Backends: map[string]string{"devbox": "tmux"}})
	if err != nil || !changed {
		t.Fatalf("MergeSetup() = changed=%t err=%v", changed, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# keep\nbackend = \"tmux\"\n[agents]\n# notify choice\nnotify = true\n[remote]\nmachines = [\"old\", \"devbox\"]\n[remote.backends]\ndevbox = \"tmux\"\n"
	if string(got) != want {
		t.Fatalf("config = %q, want %q", got, want)
	}
	changed, err = MergeSetup(path, SetupValues{Notify: &notify, Machines: []string{"devbox"}, Backends: map[string]string{"devbox": "tmux"}})
	if err != nil || changed {
		t.Fatalf("second MergeSetup() = changed=%t err=%v", changed, err)
	}
}

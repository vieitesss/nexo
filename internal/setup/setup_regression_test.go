package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingSetupRunner struct {
	calls []string
}

func (r *recordingSetupRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil, nil
}

func TestReplaceManagedBlockPreservesUserOrdering(t *testing.T) {
	before := "before\n" + ManagedStart + "\nold\n" + ManagedEnd + "\nafter override\n"
	got, changed, err := ReplaceManagedBlock(before, "new")
	if err != nil || !changed {
		t.Fatalf("replace = %q changed=%t err=%v", got, changed, err)
	}
	want := "before\n" + ManagedStart + "\nnew\n" + ManagedEnd + "\nafter override\n"
	if got != want {
		t.Fatalf("replace moved outside bytes: %q, want %q", got, want)
	}
}

func TestRemoteFileReadFailureIsNotMissing(t *testing.T) {
	runner := &recordingReadRunner{output: []byte("partial"), err: errors.New("permission denied")}
	if _, _, err := readRemoteFile(context.Background(), runner, "dev", "", "/home/u/.bashrc"); err == nil {
		t.Fatal("readRemoteFile accepted a failed read")
	}
}

type recordingReadRunner struct {
	output []byte
	err    error
}

func (r *recordingReadRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return r.output, r.err
}

func TestParseSSHHostsIgnoresInlineComments(t *testing.T) {
	got := ParseSSHHosts("Host dev # comment here\nHost gpu !excluded\nHost *.example\n")
	if strings.Join(got, ",") != "dev,gpu" {
		t.Fatalf("hosts = %#v", got)
	}
}

func TestDryRunLeavesFilesystemUntouched(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "nexo", "config.toml")
	notify := true
	runner := &recordingSetupRunner{}
	output := &bytes.Buffer{}
	err := Run(context.Background(), Input{Runner: runner, Home: home, Stdout: output, Stdin: strings.NewReader(""), TTY: false}, Options{Backend: "tmux", ConfigPath: configPath, LocalOnly: true, DryRun: true, Notify: &notify, Paths: []string{home}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run wrote config: %v", err)
	}
	if files, err := os.ReadDir(home); err != nil || len(files) != 0 {
		t.Fatalf("dry-run wrote files: %v %#v", err, files)
	}
	if !strings.Contains(output.String(), "dry run: no changes written") {
		t.Fatalf("output did not identify dry-run: %q", output.String())
	}
}

func TestShellPathBodyUsesFishSyntax(t *testing.T) {
	body := shellPathBody("/usr/bin/fish", "C.UTF-8", true)
	if strings.Contains(body, "export PATH") || !strings.Contains(body, "fish_add_path") || !strings.Contains(body, "set -gx LC_ALL") {
		t.Fatalf("fish body = %q", body)
	}
}

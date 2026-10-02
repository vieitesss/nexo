package setup

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	output []byte
	err    error
}

func (f *fakeRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return f.output, f.err
}

func TestReplaceManagedBlockIsIdempotentAndRepairsDuplicates(t *testing.T) {
	before := "# keep\n" + ManagedStart + "\nold\n" + ManagedEnd + "\n" + ManagedStart + "\nold2\n" + ManagedEnd + "\n"
	got, changed, err := ReplaceManagedBlock(before, "new")
	if err != nil || !changed {
		t.Fatalf("ReplaceManagedBlock() = %q, %v, changed=%t", got, err, changed)
	}
	want := "# keep\n" + ManagedStart + "\nnew\n" + ManagedEnd + "\n"
	if got != want {
		t.Fatalf("block = %q, want %q", got, want)
	}
	got, changed, err = ReplaceManagedBlock(got, "new")
	if err != nil || changed || got != want {
		t.Fatalf("second replacement = %q, changed=%t, err=%v", got, changed, err)
	}
}

func TestParseSSHHostsSkipsPatterns(t *testing.T) {
	got := ParseSSHHosts("Host devbox gpu\n Host *.example !bad\nHost Devbox\nHost work\n")
	if strings.Join(got, ",") != "devbox,gpu,work" {
		t.Fatalf("hosts = %#v", got)
	}
}

func TestProbeRemote(t *testing.T) {
	f := &fakeRunner{output: []byte("home=/home/u\nshell=/bin/zsh\nplatform=Linux x86_64\nnexo=/home/u/.local/bin/nexo\ntmux=/usr/bin/tmux\nherdr=\nlocale=UTF-8\n")}
	got := ProbeRemote(context.Background(), f, "devbox", "")
	if got.Error != nil || !got.Reachable || got.OS != "linux" || got.Arch != "amd64" || !got.HasNexo || !got.HasTmux || !got.LocaleUTF8 {
		t.Fatalf("probe = %#v", got)
	}
}

func TestProbeRemoteReportsUnreachable(t *testing.T) {
	got := ProbeRemote(context.Background(), &fakeRunner{err: errors.New("offline")}, "offline", "")
	if got.Error == nil || got.Reachable {
		t.Fatalf("probe = %#v", got)
	}
}

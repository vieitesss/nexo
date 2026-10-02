package upgrade

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	output []byte
	err    error
	calls  []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	return f.output, f.err
}

func TestResolveLatestRelease(t *testing.T) {
	fake := &fakeRunner{output: []byte("aaa\trefs/tags/v1.9.0\nbbb\trefs/tags/v2.0.0\nccc\trefs/tags/v1.10.0\nddd\trefs/tags/v2.0.0-rc.1\n")}
	got, err := Resolve(context.Background(), fake, VersionRequest{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Release != "v2.0.0" || got.Ref != got.Release || got.Nightly {
		t.Fatalf("version = %#v", got)
	}
	if len(fake.calls) != 1 || !strings.Contains(fake.calls[0], "ls-remote --tags --refs") {
		t.Fatalf("calls = %#v", fake.calls)
	}
}

func TestResolveFailsWithoutReleaseAndPointsToNightly(t *testing.T) {
	fake := &fakeRunner{}
	_, err := Resolve(context.Background(), fake, VersionRequest{})
	if err == nil || !strings.Contains(err.Error(), "--nightly") {
		t.Fatalf("Resolve() error = %v, want --nightly guidance", err)
	}
}

func TestResolveNightlyMain(t *testing.T) {
	fake := &fakeRunner{output: []byte("abc123\trefs/heads/main\n")}
	got, err := Resolve(context.Background(), fake, VersionRequest{Nightly: true})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != (Version{Release: "main", Ref: "main", Nightly: true}) {
		t.Fatalf("version = %#v", got)
	}
}

func TestResolveRejectsMissingRequestedRelease(t *testing.T) {
	fake := &fakeRunner{output: []byte("abc\trefs/tags/v1.0.0\n")}
	_, err := Resolve(context.Background(), fake, VersionRequest{Version: "v9.0.0"})
	if err == nil || !strings.Contains(err.Error(), "--nightly") {
		t.Fatalf("Resolve() error = %v, want nightly guidance", err)
	}
}

func TestResolveReportsGitAuthenticationFailure(t *testing.T) {
	fake := &fakeRunner{err: errors.New("permission denied")}
	_, err := Resolve(context.Background(), fake, VersionRequest{})
	if err == nil || !strings.Contains(err.Error(), "resolve releases") {
		t.Fatalf("Resolve() error = %v", err)
	}
}

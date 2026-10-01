package commands

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/config"
)

type fakeRemotePaster struct {
	target string
	keys   [][]string
	texts  []string
}

func (p *fakeRemotePaster) ProxyTarget(context.Context, string) (string, error) { return p.target, nil }
func (p *fakeRemotePaster) SendPaneKeys(_ context.Context, _ string, keys []string) error {
	p.keys = append(p.keys, append([]string(nil), keys...))
	return nil
}
func (p *fakeRemotePaster) SendPaneText(_ context.Context, _ string, text string) error {
	p.texts = append(p.texts, text)
	return nil
}

type pasteInputRunner struct {
	readerOutput []byte
	uploadOutput []byte
	err          error
	input        []byte
}

func (r *pasteInputRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return r.readerOutput, r.err
}
func (r *pasteInputRunner) RunInput(_ context.Context, input []byte, _ string, _ ...string) ([]byte, error) {
	r.input = append([]byte(nil), input...)
	return r.uploadOutput, r.err
}

func TestRemotePastePassesKeyOutsideProxyWithoutReadingClipboard(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9")
	paster := &fakeRemotePaster{}
	runner := &pasteInputRunner{readerOutput: []byte("must not be read")}
	err := runRemotePaste(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   paster,
		Runner:    runner,
		Args:      []string{"paste", "C-v"},
	})
	if err != nil {
		t.Fatalf("runRemotePaste() error = %v", err)
	}
	if !reflect.DeepEqual(paster.keys, [][]string{{"C-v"}}) || len(paster.texts) != 0 {
		t.Fatalf("keys = %#v, texts = %#v", paster.keys, paster.texts)
	}
}

func TestRemotePastePassesKeyWhenClipboardHasNoImage(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9")
	paster := &fakeRemotePaster{target: "devbox/api"}
	runner := &pasteInputRunner{readerOutput: nil, err: context.Canceled}
	if err := runRemotePaste(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   paster,
		Runner:    runner,
		Args:      []string{"paste", "C-v"},
	}); err != nil {
		t.Fatalf("runRemotePaste() error = %v", err)
	}
	if !reflect.DeepEqual(paster.keys, [][]string{{"C-v"}}) {
		t.Fatalf("keys = %#v, want pass-through", paster.keys)
	}
}

func TestRemotePasteReportsMissingToolAndPassesKeyThrough(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9")
	paster := &fakeRemotePaster{target: "devbox/api"}
	runner := &pasteInputRunner{err: &missingClipboardToolError{}}
	var stderr strings.Builder
	if err := runRemotePaste(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   paster,
		Runner:    runner,
		Stderr:    &stderr,
		Args:      []string{"paste", "C-v"},
	}); err != nil {
		t.Fatalf("runRemotePaste() error = %v", err)
	}
	if len(paster.keys) != 1 || !strings.Contains(stderr.String(), "passing the key through") {
		t.Fatalf("keys = %#v, stderr = %q", paster.keys, stderr.String())
	}
}

type missingClipboardToolError struct{}

func (*missingClipboardToolError) Error() string {
	return "exec: xclip: executable file not found in $PATH"
}

func TestRemotePasteUploadsImageAndTypesRemotePath(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9")
	paster := &fakeRemotePaster{target: "devbox/api"}
	runner := &pasteInputRunner{readerOutput: []byte("png"), uploadOutput: []byte("/tmp/nexo-paste-abcd.png\n")}
	var stderr strings.Builder
	err := runRemotePaste(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{SSHConfig: "/tmp/ssh"}},
		Adapter:   paster,
		Runner:    runner,
		Stderr:    &stderr,
		Args:      []string{"paste", "C-v"},
	})
	if err != nil {
		t.Fatalf("runRemotePaste() error = %v", err)
	}
	if got, want := string(runner.input), "png"; got != want {
		t.Fatalf("uploaded input = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(paster.texts, []string{"/tmp/nexo-paste-abcd.png"}) || len(paster.keys) != 0 {
		t.Fatalf("keys = %#v, texts = %#v", paster.keys, paster.texts)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want silent success", stderr.String())
	}
}

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
	panes  []string
	keys   [][]string
	texts  []string
}

func (p *fakeRemotePaster) ProxyTarget(_ context.Context, paneID string) (string, error) {
	p.panes = append(p.panes, paneID)
	return p.target, nil
}

func (p *fakeRemotePaster) SendPaneKeys(_ context.Context, paneID string, keys []string) error {
	p.panes = append(p.panes, paneID)
	p.keys = append(p.keys, append([]string(nil), keys...))
	return nil
}

func (p *fakeRemotePaster) SendPaneText(_ context.Context, paneID, text string) error {
	p.panes = append(p.panes, paneID)
	p.texts = append(p.texts, text)
	return nil
}

type pasteInputRunner struct {
	offers []byte
	png    []byte
	upload []byte
	err    error
	input  []byte
}

func (r *pasteInputRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if r.err != nil {
		return nil, r.err
	}
	joined := name + " " + strings.Join(args, " ")
	if name == "wl-paste" && strings.Contains(joined, "--list-types") || strings.Contains(joined, "TARGETS") {
		return r.offers, nil
	}
	return r.png, nil
}

func (r *pasteInputRunner) RunInput(_ context.Context, input []byte, _ string, _ ...string) ([]byte, error) {
	r.input = append([]byte(nil), input...)
	if r.err != nil {
		return nil, r.err
	}
	return r.upload, nil
}

var pasteTestPNG = []byte("\x89PNG\r\n\x1a\npayload")

func assertPanes(t *testing.T, paster *fakeRemotePaster, want string) {
	t.Helper()
	if len(paster.panes) == 0 {
		t.Fatal("no pane was used")
	}
	for _, pane := range paster.panes {
		if pane != want {
			t.Fatalf("panes = %#v, want every call to use %q", paster.panes, want)
		}
	}
}

func TestRemotePastePassesKeyOutsideProxyWithoutReadingClipboard(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9")
	paster := &fakeRemotePaster{}
	runner := &pasteInputRunner{}
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
	assertPanes(t, paster, "%9")
}

func TestRemotePasteUsesHerdrPaneEnvironment(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "p1")
	paster := &fakeRemotePaster{}
	if err := runRemotePaste(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Herdr},
		Adapter:   paster,
		Runner:    &pasteInputRunner{},
		Args:      []string{"paste", "ctrl+v"},
	}); err != nil {
		t.Fatalf("runRemotePaste() error = %v", err)
	}
	if !reflect.DeepEqual(paster.panes, []string{"p1", "p1"}) {
		t.Fatalf("panes = %#v, want both calls to use HERDR_PANE_ID", paster.panes)
	}
}

func TestRemotePasteRequiresCurrentPane(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	err := runRemotePaste(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   &fakeRemotePaster{},
		Runner:    &pasteInputRunner{},
		Args:      []string{"paste", "C-v"},
	})
	if err == nil || !strings.Contains(err.Error(), "current pane") {
		t.Fatalf("runRemotePaste() error = %v, want current pane error", err)
	}
}

func TestRemotePastePassesKeyWhenClipboardHasNoImage(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9")
	paster := &fakeRemotePaster{target: "devbox/api"}
	runner := &pasteInputRunner{offers: []byte("text/plain\n")}
	if err := runRemotePaste(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   paster,
		Runner:    runner,
		Args:      []string{"paste", "C-v"},
	}); err != nil {
		t.Fatalf("runRemotePaste() error = %v", err)
	}
	if !reflect.DeepEqual(paster.keys, [][]string{{"C-v"}}) || runner.input != nil {
		t.Fatalf("keys = %#v, upload input = %q; want pass-through without upload", paster.keys, runner.input)
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
	runner := &pasteInputRunner{
		offers: []byte("image/png\n"),
		png:    pasteTestPNG,
		upload: []byte("/tmp/nexo-paste-abcd/nexo-paste-image.png\n"),
	}
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
	if got, want := string(runner.input), string(pasteTestPNG); got != want {
		t.Fatalf("uploaded input = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(paster.texts, []string{"/tmp/nexo-paste-abcd/nexo-paste-image.png"}) || len(paster.keys) != 0 {
		t.Fatalf("keys = %#v, texts = %#v", paster.keys, paster.texts)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want silent success", stderr.String())
	}
}

func TestRemotePasteShellQuotesRemotePath(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9")
	paster := &fakeRemotePaster{target: "devbox/api"}
	runner := &pasteInputRunner{
		offers: []byte("image/png"),
		png:    pasteTestPNG,
		upload: []byte("/tmp/nexo paste/nexo-paste-image.png\n"),
	}
	err := runRemotePaste(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   paster,
		Runner:    runner,
		Args:      []string{"paste", "C-v"},
	})
	if err != nil {
		t.Fatalf("runRemotePaste() error = %v", err)
	}
	if want := []string{"'/tmp/nexo paste/nexo-paste-image.png'"}; !reflect.DeepEqual(paster.texts, want) {
		t.Fatalf("texts = %#v, want %#v", paster.texts, want)
	}
}

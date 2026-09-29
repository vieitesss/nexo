package picker

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestThemeFromTerminalParsesOSCColorsAndBlends(t *testing.T) {
	responses := deadlineReader{Reader: strings.NewReader("\x1b]10;rgb:ffff/0000/0000\x07\x1b]11;rgb:0000/0000/ffff\x07")}
	var query strings.Builder
	got := ThemeFromTerminal(context.Background(), ThemeQuery{Reader: responses, Writer: &query}, time.Second)
	if got.Foreground != "#ff0000" || got.Background != "#0000ff" || got.SelectionBackground == "" || !got.Color {
		t.Fatalf("theme = %#v", got)
	}
	if query.String() != "\x1b]10;?\x07\x1b]11;?\x07" {
		t.Fatalf("query = %q", query.String())
	}
}

type deadlineReader struct{ io.Reader }

func (deadlineReader) SetReadDeadline(time.Time) error { return nil }

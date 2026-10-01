package shell

import (
	"strings"
	"testing"
)

func TestPlainRemovesStylingAndControls(t *testing.T) {
	if got := Plain("\x1b[41mred\x1b[0m\nnext\tline"); got != "red next line" {
		t.Fatalf("Plain = %q", got)
	}
}

func TestPromptLineKeepsCursorAtInputEnd(t *testing.T) {
	line := PromptLine(12, "⌕ ", strings.Repeat("x", 30), "▌")
	if !strings.HasSuffix(line, "▌") || StringWidth(line) != 12 {
		t.Fatalf("PromptLine = %q", line)
	}
}

func TestTruncateLeftKeepsTheTailAndHead(t *testing.T) {
	if got, want := TruncateLeft("~/work/prefapp/gitops", 10, "…"), "…pp/gitops"; got != want {
		t.Fatalf("TruncateLeft = %q, want %q", got, want)
	}
	if got, want := TruncateLeft("~/work", 20, "…"), "~/work"; got != want {
		t.Fatalf("TruncateLeft fitting value = %q, want %q", got, want)
	}
	if got := TruncateLeft("~/work", 0, "…"); got != "" {
		t.Fatalf("TruncateLeft zero width = %q", got)
	}
}

func TestPlainDropsOSCAndControlPayloads(t *testing.T) {
	value := "before\x1b]8;;https://example.test\x07link\x1b]8;;\x1b\\after\x1bPbinary\x1b\\\x00end"
	if got, want := Plain(value), "beforelinkafter end"; got != want {
		t.Fatalf("Plain = %q, want %q", got, want)
	}
}

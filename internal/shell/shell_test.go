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

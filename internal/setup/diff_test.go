package setup

import (
	"strings"
	"testing"
)

func TestFormatDiffShowsOnlyTheAddedBlockWithContext(t *testing.T) {
	before := "set -g a 1\nset -g b 2\nset -g c 3\nset -g d 4\nset -g e 5\n"
	after := before + "\n# >>> nexo >>>\nset -g focus-events on\n# <<< nexo <<<\n"
	got := FormatDiff(Change{Machine: "pi", Path: "~/.tmux.conf", Before: []byte(before), After: []byte(after)})
	want := "--- ~/.tmux.conf (pi)\n+++ ~/.tmux.conf\n" +
		"@@ line 4 @@\n" +
		" set -g d 4\n" +
		" set -g e 5\n" +
		"+\n" +
		"+# >>> nexo >>>\n" +
		"+set -g focus-events on\n" +
		"+# <<< nexo <<<\n"
	if got != want {
		t.Fatalf("diff =\n%s\nwant\n%s", got, want)
	}
}

func TestFormatDiffSplitsDistantChangesIntoHunks(t *testing.T) {
	before := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	after := "1\nTWO\n3\n4\n5\n6\n7\n8\nNINE\n10\n"
	got := FormatDiff(Change{Machine: "local", Path: "f", Before: []byte(before), After: []byte(after)})
	if strings.Count(got, "@@ line") != 2 {
		t.Fatalf("want two hunks:\n%s", got)
	}
	for _, want := range []string{"@@ line 1 @@\n 1\n-2\n+TWO\n 3\n 4\n", "@@ line 7 @@\n 7\n 8\n-9\n+NINE\n 10\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("diff lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, " 5\n") {
		t.Fatalf("diff shows lines far from any change:\n%s", got)
	}
}

func TestFormatDiffNewFileIsAllAdditions(t *testing.T) {
	got := FormatDiff(Change{Machine: "local", Path: "f", After: []byte("a\nb\n")})
	if want := "--- f (local)\n+++ f\n@@ line 1 @@\n+a\n+b\n"; got != want {
		t.Fatalf("diff = %q, want %q", got, want)
	}
	if FormatDiff(Change{Before: []byte("same"), After: []byte("same")}) != "" {
		t.Fatal("unchanged file rendered a diff")
	}
}

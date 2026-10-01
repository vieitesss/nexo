package pathpolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainsUsesLexicalAndPhysicalPolicy(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	child := filepath.Join(project, "src")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !Contains(project, child, false, OSResolver{}) {
		t.Fatal("lexical descendant was not detected")
	}
	if !Contains(project, filepath.Join(alias, "src"), false, OSResolver{}) {
		t.Fatal("physical alias descendant was not detected")
	}
	if Contains(alias, child, true, OSResolver{}) {
		t.Fatal("link target usage was treated as use of the link")
	}
	if Contains(project, filepath.Join(root, "project-other"), false, OSResolver{}) {
		t.Fatal("sibling with a shared prefix was treated as a descendant")
	}
}

package destination

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateDirectoryCreatesNestedParents(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "one", "two", "three")
	if err := CreateDirectory(target); err != nil {
		t.Fatalf("CreateDirectory() error = %v", err)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("Stat(%q) = %v, %v; want a directory", target, info, err)
	}
}

func TestCreateDirectoryRejectsFileAncestor(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreateDirectory(filepath.Join(file, "child")); err == nil {
		t.Fatal("CreateDirectory() error = nil, want a file-ancestor failure")
	}
}

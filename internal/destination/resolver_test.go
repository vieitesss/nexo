package destination

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolverRejectsExistingDestination(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "repo")
	if err := os.WriteFile(destination, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(Options{Home: root}).Resolve(destination)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Resolve() error = %v, want existing destination refusal", err)
	}
}

func TestResolverRejectsMissingParent(t *testing.T) {
	root := t.TempDir()
	_, err := New(Options{Home: root}).Resolve(filepath.Join(root, "missing", "repo"))
	if err == nil || !strings.Contains(err.Error(), "parent") || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Resolve() error = %v, want missing parent refusal", err)
	}
}

func TestResolverOffersUnconfiguredParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "projects")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := New(Options{Home: root}).Resolve(filepath.Join(parent, "repo"))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !got.AddParent || got.Parent != parent || got.Path != filepath.Join(parent, "repo") {
		t.Fatalf("Resolve() = %#v, want parent offer", got)
	}
	got, err = New(Options{Home: root, Paths: []string{parent}}).Resolve(filepath.Join(parent, "next"))
	if err != nil || got.AddParent {
		t.Fatalf("configured parent result = %#v, error %v; want no offer", got, err)
	}
}

func TestDefaultPathDerivesCompleteOperands(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
	}{
		{"https://github.com/owner/repo.git", "/work/repo"},
		{"git@github.com:owner/repo.git", "/work/repo"},
		{"alice@example.com:team/repo.git", "/work/repo"},
		{"/src/repo.git", "/work/repo"},
		{"repo.git", "/work/repo"},
	} {
		got, err := DefaultPath(test.source, "/work", "/home/tester")
		if err != nil || got != test.want {
			t.Fatalf("DefaultPath(%q) = %q, %v; want %q", test.source, got, err, test.want)
		}
	}
}

func TestResolverExpandsHome(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := New(Options{Home: root}).Resolve("~/projects/repo")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Path != filepath.Join(root, "projects", "repo") {
		t.Fatalf("Path = %q, want home-expanded destination", got.Path)
	}
}

package rex

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps backend tests from writing Rex state into the developer's
// real XDG state directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nexo-rex-test-state")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_STATE_HOME", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func writeExecutable(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func lookupWithoutPath() binaryLookup {
	return binaryLookup{
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}
}

func TestLocateBinaryPrefersPath(t *testing.T) {
	binDir := t.TempDir()
	bundled := writeExecutable(t, filepath.Join(binDir, "rex"))
	got := locateBinary(binaryLookup{
		LookPath: func(name string) (string, error) { return "/usr/local/bin/" + name, nil },
		Getenv:   func(string) string { return binDir },
		HomeDir:  func() (string, error) { return t.TempDir(), nil },
	})
	if got != "/usr/local/bin/rex" {
		t.Fatalf("locateBinary() = %q, want the PATH binary", got)
	}
	if bundled == got {
		t.Fatalf("locateBinary() = %q, want PATH to win over REX_BIN_DIR", got)
	}
}

func TestLocateBinaryUsesRexBinDirWithoutSplittingSpaces(t *testing.T) {
	binDir := t.TempDir()
	wanted := writeExecutable(t, filepath.Join(binDir, "Rex Server.app", "Contents", "MacOS", "rex"))
	lookup := lookupWithoutPath()
	lookup.Getenv = func(string) string { return filepath.Dir(wanted) }
	lookup.HomeDir = func() (string, error) { return t.TempDir(), nil }

	got := locateBinary(lookup)
	if got != wanted {
		t.Fatalf("locateBinary() = %q, want the REX_BIN_DIR binary %q", got, wanted)
	}
}

func TestLocateBinaryFallsBackToApplicationBundle(t *testing.T) {
	home := t.TempDir()
	wanted := writeExecutable(t, filepath.Join(home, appBinary))
	lookup := lookupWithoutPath()
	lookup.Getenv = func(string) string { return "" }
	lookup.HomeDir = func() (string, error) { return home, nil }

	got := locateBinary(lookup)
	if got != wanted {
		t.Fatalf("locateBinary() = %q, want the application bundle binary %q", got, wanted)
	}
}

func TestLocateBinaryKeepsCommandNameWhenNothingIsInstalled(t *testing.T) {
	lookup := lookupWithoutPath()
	lookup.Getenv = func(string) string { return "" }
	lookup.HomeDir = func() (string, error) { return t.TempDir(), nil }

	if got := locateBinary(lookup); got != "rex" {
		t.Fatalf("locateBinary() = %q, want the bare command name", got)
	}
}

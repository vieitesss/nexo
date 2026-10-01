package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vieitesss/nexo/internal/runner"
)

// MissingToolError means the local clipboard reader is not installed.
type MissingToolError struct {
	Tool string
}

func (e *MissingToolError) Error() string {
	return fmt.Sprintf("clipboard image tool %s is not installed", e.Tool)
}

// ClipboardReader reads a PNG from the platform clipboard through a command
// runner. A nil byte slice means the clipboard has no image.
type ClipboardReader struct {
	Runner runner.CommandRunner
	GOOS   string
	Env    func(string) string
}

func (r ClipboardReader) Read(ctx context.Context) ([]byte, error) {
	if r.Runner == nil {
		return nil, errors.New("clipboard reader requires a command runner")
	}
	goos := r.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	env := r.Env
	if env == nil {
		env = os.Getenv
	}
	name, args := clipboardCommand(goos, env)
	output, err := r.Runner.Run(ctx, name, args...)
	if err == nil {
		if len(output) == 0 {
			return nil, nil
		}
		return output, nil
	}
	if isMissingTool(err) {
		if goos == "darwin" && name == "pngpaste" {
			output, fallbackErr := r.Runner.Run(ctx, "osascript", "-e", "set pngData to the clipboard as «class PNGf»\nreturn pngData")
			if fallbackErr == nil {
				if len(output) == 0 {
					return nil, nil
				}
				return output, nil
			}
			if !isMissingTool(fallbackErr) {
				return nil, nil
			}
			return nil, &MissingToolError{Tool: "pngpaste or osascript"}
		}
		return nil, &MissingToolError{Tool: name}
	}
	// Clipboard reader exit failures mean that the clipboard has no PNG. This
	// is intentionally silent so a bound key remains a transparent fallback.
	return nil, nil
}

func clipboardCommand(goos string, env func(string) string) (string, []string) {
	switch goos {
	case "darwin":
		return "pngpaste", []string{"-"}
	case "linux":
		if env("WAYLAND_DISPLAY") != "" {
			return "wl-paste", []string{"--type", "image/png", "--no-newline"}
		}
		return "xclip", []string{"-selection", "clipboard", "-target", "image/png", "-out"}
	default:
		return "xclip", []string{"-selection", "clipboard", "-target", "image/png", "-out"}
	}
}

func isMissingTool(err error) bool {
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "executable file not found") || strings.Contains(message, "command not found")
}

const uploadCommand = `umask 077; path=$(mktemp "${TMPDIR:-/tmp}/nexo-paste-XXXXXXXX.png") || exit 1; cat > "$path" || { rm -f "$path"; exit 1; }; printf '%s\n' "$path"`

// UploadPNG streams PNG bytes to a securely-created remote temporary file.
func UploadPNG(ctx context.Context, commandRunner runner.CommandRunner, sshConfig, machine string, png []byte) (string, error) {
	inputRunner, ok := commandRunner.(runner.InputRunner)
	if !ok {
		return "", errors.New("remote paste requires an input-capable command runner")
	}
	if strings.TrimSpace(machine) == "" {
		return "", errors.New("remote paste requires a machine")
	}
	output, err := inputRunner.RunInput(ctx, png, "ssh", SSHCommandArgs(sshConfig, machine, uploadCommand)...)
	if err != nil {
		return "", fmt.Errorf("upload clipboard image: %w", err)
	}
	path := strings.TrimSpace(string(output))
	if path == "" || strings.ContainsAny(path, "\r\n") {
		return "", errors.New("upload clipboard image returned no remote path")
	}
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "nexo-paste-") || !strings.HasSuffix(base, ".png") {
		return "", errors.New("upload clipboard image returned an unsafe remote path")
	}
	return path, nil
}

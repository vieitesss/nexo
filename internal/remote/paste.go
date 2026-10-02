package remote

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

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

// pngSignature is the 8-byte PNG file signature. Any reader output that does
// not start with it is treated as if the clipboard had no image.
const pngSignature = "\x89PNG\r\n\x1a\n"

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
	if goos == "darwin" {
		return r.readDarwin(ctx)
	}
	tool, listArgs, readArgs := clipboardCommand(goos, env)
	offers, err := r.Runner.Run(ctx, tool, listArgs...)
	if err != nil {
		if isMissingTool(err) {
			return nil, &MissingToolError{Tool: tool}
		}
		// Enumeration failures mean no usable image is known. This is
		// intentionally silent so a bound key stays a transparent fallback.
		return nil, nil
	}
	if !offersPNG(offers) {
		return nil, nil
	}
	output, err := r.Runner.Run(ctx, tool, readArgs...)
	if err != nil {
		if isMissingTool(err) {
			return nil, &MissingToolError{Tool: tool}
		}
		return nil, nil
	}
	return clipboardPNG(output), nil
}

func (r ClipboardReader) readDarwin(ctx context.Context) ([]byte, error) {
	output, err := r.Runner.Run(ctx, "pngpaste", "-")
	if err == nil {
		return clipboardPNG(output), nil
	}
	if !isMissingTool(err) {
		return nil, nil
	}
	output, err = r.Runner.Run(ctx, "osascript", "-e", appleScriptPNG)
	if err != nil {
		if isMissingTool(err) {
			return nil, &MissingToolError{Tool: "pngpaste or osascript"}
		}
		return nil, nil
	}
	return decodeAppleScriptPNG(output), nil
}

// appleScriptPNG asks the macOS clipboard for raw PNG data. osascript prints
// the resulting AppleScript data value as a «data PNGf…» hexadecimal literal.
const appleScriptPNG = "set pngData to the clipboard as «class PNGf»\nreturn pngData"

// decodeAppleScriptPNG converts the «data PNGf…» literal printed by osascript
// back to bytes and accepts it only when it carries the PNG signature.
func decodeAppleScriptPNG(output []byte) []byte {
	text := strings.TrimSpace(string(output))
	const prefix = "«data PNGf"
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, "»") {
		return nil
	}
	digits := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text[len(prefix):len(text)-len("»")])
	raw, err := hex.DecodeString(digits)
	if err != nil {
		return nil
	}
	return clipboardPNG(raw)
}

// clipboardCommand returns the platform clipboard tool together with the
// arguments that enumerate advertised formats and read an image/png payload.
func clipboardCommand(goos string, env func(string) string) (string, []string, []string) {
	if goos == "linux" && env("WAYLAND_DISPLAY") != "" {
		return "wl-paste",
			[]string{"--list-types"},
			[]string{"--type", "image/png", "--no-newline"}
	}
	return "xclip",
		[]string{"-selection", "clipboard", "-target", "TARGETS", "-out"},
		[]string{"-selection", "clipboard", "-target", "image/png", "-out"}
}

// offersPNG reports whether a clipboard format listing advertises image/png.
func offersPNG(output []byte) bool {
	for _, field := range strings.Fields(string(output)) {
		if field == "image/png" {
			return true
		}
	}
	return false
}

// clipboardPNG accepts reader output only when it is a PNG.
func clipboardPNG(output []byte) []byte {
	if len(output) < len(pngSignature) || string(output[:len(pngSignature)]) != pngSignature {
		return nil
	}
	return output
}

func isMissingTool(err error) bool {
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "executable file not found") || strings.Contains(message, "command not found")
}

// uploadCommand creates a private random directory and writes the uploaded
// bytes to a mode-0600 file inside it. The directory template ends in Xs so
// both GNU and BSD mktemp replace them, unlike a template ending in ".png".
// It runs in the remote login shell, so it avoids zsh's special parameters:
// assigning $path there would clobber $PATH.
const uploadCommand = `umask 077; dir=$(mktemp -d "${TMPDIR:-/tmp}/nexo-paste-XXXXXXXX") || exit 1; file="$dir/nexo-paste-image.png"; cat > "$file" || { rm -rf "$dir"; exit 1; }; printf '%s\n' "$file"`

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
	return remotePastePath(output)
}

// remotePastePath validates the single absolute path printed by the upload
// command. Control and format characters are rejected outright because they
// can manipulate an attached terminal; shell metacharacters are neutralized
// by ShellQuotePath at the insertion seam.
func remotePastePath(output []byte) (string, error) {
	path := strings.TrimSuffix(string(output), "\n")
	if path == "" || strings.ContainsAny(path, "\r\n") {
		return "", errors.New("upload clipboard image returned no remote path")
	}
	if !utf8.ValidString(path) || !filepath.IsAbs(path) {
		return "", errors.New("upload clipboard image returned an unsafe remote path")
	}
	for _, r := range path {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", errors.New("upload clipboard image returned an unsafe remote path")
		}
	}
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "nexo-paste-") || !strings.HasSuffix(base, ".png") {
		return "", errors.New("upload clipboard image returned an unsafe remote path")
	}
	return path, nil
}

// ShellQuotePath makes a validated remote path safe to insert at a shell
// prompt. Plain paths are unchanged; anything else becomes one single-quoted
// shell word.
func ShellQuotePath(path string) string {
	if isShellSafe(path) {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

func isShellSafe(path string) bool {
	if path == "" {
		return false
	}
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

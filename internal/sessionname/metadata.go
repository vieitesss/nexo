package sessionname

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/runner"
)

// Resolve renders a configured name using local VCS metadata or lexical filesystem values.
func Resolve(ctx context.Context, path, template string, commandRunner runner.CommandRunner) (string, error) {
	if err := ValidateTemplate(template); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	clean := filepath.Clean(path)
	values := Values{}
	parent := filepath.Base(filepath.Dir(clean))
	repo := filepath.Base(clean)
	if repo != "." && repo != string(filepath.Separator) {
		values.Repo = repo
	}
	if parent != "." && parent != string(filepath.Separator) {
		values.Org = parent
	}
	if commandRunner != nil {
		readCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if discovered, ok := discover(readCtx, clean, commandRunner); ok {
			values = discovered
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, err := Render(template, values)
	if err != nil {
		return "", fmt.Errorf("resolve session name: %w", err)
	}
	return name, nil
}

func discover(ctx context.Context, path string, r runner.CommandRunner) (Values, bool) {
	if markerExists(path, ".jj") {
		return jjValues(ctx, path, r)
	}
	if markerExists(path, ".git") {
		return gitValues(ctx, path, nil, r)
	}
	if markerExists(path, ".hg") {
		return hgValues(ctx, path, r)
	}
	return Values{}, false
}
func markerExists(path, name string) bool {
	_, err := os.Stat(filepath.Join(path, name))
	return err == nil
}
func sameRoot(path, root string) bool {
	root = strings.TrimSpace(root)
	if !filepath.IsAbs(root) {
		return false
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	reported, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	return filepath.Clean(actual) == filepath.Clean(reported)
}
func read(ctx context.Context, r runner.CommandRunner, name string, args ...string) (string, bool) {
	if ctx.Err() != nil {
		return "", false
	}
	out, err := r.Run(ctx, name, args...)
	if err != nil || ctx.Err() != nil {
		return "", false
	}
	return string(out), true
}
func gitValues(ctx context.Context, path string, gitDir *string, r runner.CommandRunner) (Values, bool) {
	args := []string{"-C", path}
	if gitDir == nil {
		root, ok := read(ctx, r, "git", append(args, "rev-parse", "--show-toplevel")...)
		if !ok || !sameRoot(path, root) {
			return Values{}, false
		}
	} else {
		args = append(args, "--git-dir="+*gitDir)
	}
	remotes, ok := read(ctx, r, "git", append(args, "remote")...)
	if !ok {
		return Values{}, false
	}
	names := strings.Fields(remotes)
	selected := ""
	for _, name := range names {
		if name == "origin" {
			selected = "origin"
			break
		}
	}
	if selected == "" && len(names) == 1 {
		selected = names[0]
	}
	if selected == "" {
		return Values{}, false
	}
	urls, ok := read(ctx, r, "git", append(args, "remote", "get-url", "--all", "--", selected)...)
	if !ok {
		return Values{}, false
	}
	// Remove only the EOF newline so empty URL records still count as ambiguity.
	lines := strings.Split(strings.TrimSuffix(urls, "\n"), "\n")
	if len(lines) != 1 {
		return Values{}, false
	}
	return parseLocation(strings.TrimSpace(lines[0]), true)
}
func jjValues(ctx context.Context, path string, r runner.CommandRunner) (Values, bool) {
	args := []string{"--ignore-working-copy", "--color=never", "--no-pager", "-R", path}
	root, ok := read(ctx, r, "jj", append(args, "root")...)
	if !ok || !sameRoot(path, root) {
		return Values{}, false
	}
	gitDir, ok := read(ctx, r, "jj", append(args, "git", "root")...)
	gitDir = strings.TrimSpace(gitDir)
	if !ok || !filepath.IsAbs(gitDir) {
		return Values{}, false
	}
	return gitValues(ctx, path, &gitDir, r)
}
func hgValues(ctx context.Context, path string, r runner.CommandRunner) (Values, bool) {
	args := []string{"--noninteractive", "--cwd", path}
	root, ok := read(ctx, r, "hg", append(args, "root")...)
	if !ok || !sameRoot(path, root) {
		return Values{}, false
	}
	output, ok := read(ctx, r, "hg", append(args, "paths", "default", "-Tjson")...)
	if !ok {
		return Values{}, false
	}
	var paths []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(output), &paths); err != nil || len(paths) != 1 {
		return Values{}, false
	}
	return parseLocation(paths[0].URL, false)
}

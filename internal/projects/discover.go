// Package projects discovers project candidates from the user's configuration.
package projects

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/config"
)

// Candidate is a directory that can be opened as a project.
type Candidate struct {
	Path           string `json:"path"`
	Group          string `json:"group"`
	Git            bool   `json:"git"`
	LinkedWorktree bool   `json:"linked_worktree"`
}

// Discovery is the result of project candidate discovery.
type Discovery struct {
	Candidates []Candidate
	Warnings   []string
}

// Discover expands configured paths and returns project candidates in stable
// filesystem order. Lexical entries are preserved: discovery never resolves a
// selected link or traverses through a linked configured parent.
func Discover(cfg config.ProjectsConfig, home string) Discovery {
	discovery := Discovery{Candidates: []Candidate{}}
	for _, pattern := range cfg.Paths {
		expanded, ok := expandHome(pattern, home)
		if !ok {
			discovery.Warnings = append(discovery.Warnings, fmt.Sprintf("skip %q: home directory is unavailable", pattern))
			continue
		}
		if strings.HasSuffix(expanded, string(filepath.Separator)+"*") {
			base := lexicalPath(strings.TrimSuffix(expanded, string(filepath.Separator)+"*"))
			if err := validateDirectory(base); err != nil {
				discovery.warnRead(base, err)
				continue
			}
			for _, intermediate := range discovery.readDirectories(base) {
				intermediatePath := lexicalPath(filepath.Join(base, intermediate))
				info, err := os.Lstat(intermediatePath)
				if err != nil {
					discovery.warnRead(intermediatePath, err)
					continue
				}
				if info.Mode()&os.ModeSymlink != 0 {
					// Discovery may follow an alias for navigation and Git
					// association, but every emitted path remains lexical. The
					// deletion boundary performs its own no-follow check.
					if targetInfo, targetErr := os.Stat(intermediatePath); targetErr == nil && targetInfo.IsDir() {
						discovery.addChildren(intermediatePath, intermediatePath)
					}
					continue
				}
				if !info.IsDir() {
					continue
				}
				discovery.addChildren(intermediatePath, intermediatePath)
			}
			continue
		}

		group := lexicalPath(expanded)
		if err := validateDirectory(group); err != nil {
			discovery.warnRead(group, err)
			continue
		}
		discovery.addChildren(group, group)
	}

	if cfg.GitFromHome {
		if home == "" {
			discovery.Warnings = append(discovery.Warnings, "skip git-from-home: home directory is unavailable")
			return discovery
		}
		homePath := lexicalPath(home)
		if err := validateDirectory(homePath); err != nil {
			discovery.warnRead(homePath, err)
			return discovery
		}
		for _, name := range discovery.readDirectories(homePath) {
			lexical := lexicalPath(filepath.Join(homePath, name))
			info, err := os.Lstat(lexical)
			if err != nil {
				discovery.warnRead(lexical, err)
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				// Keep the lexical link entry independent of its target while
				// retaining Git association for navigation and grouping.
				git, linked := gitStatus(lexical)
				discovery.add(lexical, homePath, git, linked)
				continue
			}
			if git, linked := gitStatus(lexical); git {
				discovery.add(lexical, homePath, git, linked)
			}
		}
	}
	return discovery
}

func expandHome(pattern, home string) (string, bool) {
	if home == "" {
		if pattern == "~" || pattern == "~/" || strings.HasPrefix(pattern, "~/") || strings.HasPrefix(pattern, "$HOME") {
			return "", false
		}
		return pattern, true
	}
	switch {
	case pattern == "~" || pattern == "~/":
		return home, true
	case strings.HasPrefix(pattern, "~/"):
		return filepath.Join(home, strings.TrimPrefix(pattern, "~/")), true
	case pattern == "$HOME":
		return home, true
	case strings.HasPrefix(pattern, "$HOME/"):
		return filepath.Join(home, strings.TrimPrefix(pattern, "$HOME/")), true
	default:
		return pattern, true
	}
}

func validateDirectory(path string) error {
	if path == "" {
		return errors.New("path is empty")
	}
	path = lexicalPath(path)
	// Discovery is non-destructive and may resolve a configured alias or HOME
	// link so existing project navigation and Git association keep working.
	// Deletion separately validates every ancestor with Lstat.
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("path is not a directory")
	}
	return nil
}

func (d *Discovery) readDirectories(path string) []string {
	entries, err := os.ReadDir(path)
	if err != nil {
		d.warnRead(path, err)
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		entryPath := filepath.Join(path, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			// Lstat is enough for link candidates, including dangling links.
			names = append(names, entry.Name())
			continue
		}
		info, err := entry.Info()
		if err != nil {
			d.warnRead(entryPath, err)
			continue
		}
		if info.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names
}

func (d *Discovery) addChildren(path, group string) {
	for _, name := range d.readDirectories(path) {
		lexical := lexicalPath(filepath.Join(path, name))
		info, err := os.Lstat(lexical)
		if err != nil {
			d.warnRead(lexical, err)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// Keep the lexical link identity while retaining Git association
			// from its resolved target for non-destructive navigation.
			git, linked := gitStatus(lexical)
			d.add(lexical, group, git, linked)
			continue
		}
		if !info.IsDir() {
			continue
		}
		git, linked := gitStatus(lexical)
		d.add(lexical, group, git, linked)
	}
}

func lexicalPath(path string) string {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}

func (d *Discovery) add(path, group string, git, linked bool) {
	for _, candidate := range d.Candidates {
		if candidate.Path == path {
			return
		}
	}
	d.Candidates = append(d.Candidates, Candidate{Path: path, Group: group, Git: git, LinkedWorktree: linked})
}

func gitStatus(path string) (bool, bool) {
	info, err := os.Lstat(filepath.Join(path, ".git"))
	if err != nil {
		return false, false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// Metadata reached through a link is ambiguous; treat it as Git so
		// deletion preflight performs a conservative inspection.
		return true, false
	}
	return true, !info.IsDir()
}

func (d *Discovery) warnRead(path string, err error) {
	d.Warnings = append(d.Warnings, fmt.Sprintf("skip %q: %v", path, err))
}

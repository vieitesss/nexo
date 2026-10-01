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
// filesystem order. Warnings describe paths that could not be read; those
// paths are skipped rather than failing discovery.
func Discover(cfg config.ProjectsConfig, home string) Discovery {
	discovery := Discovery{Candidates: []Candidate{}}
	for _, pattern := range cfg.Paths {
		expanded, ok := expandHome(pattern, home)
		if !ok {
			discovery.Warnings = append(discovery.Warnings, fmt.Sprintf("skip %q: home directory is unavailable", pattern))
			continue
		}
		if strings.HasSuffix(expanded, string(filepath.Separator)+"*") {
			base := strings.TrimSuffix(expanded, string(filepath.Separator)+"*")
			group, err := canonical(base)
			if err != nil {
				discovery.warnRead(base, err)
				continue
			}
			for _, intermediate := range discovery.readDirectories(group) {
				intermediatePath, err := canonical(filepath.Join(group, intermediate))
				if err != nil {
					discovery.warnRead(filepath.Join(group, intermediate), err)
					continue
				}
				discovery.addChildren(intermediatePath, intermediatePath)
			}
			continue
		}

		group, err := canonical(expanded)
		if err != nil {
			discovery.warnRead(expanded, err)
			continue
		}
		discovery.addChildren(group, group)
	}

	if cfg.GitFromHome {
		if home == "" {
			discovery.Warnings = append(discovery.Warnings, "skip git-from-home: home directory is unavailable")
			return discovery
		}
		homePath, err := canonical(home)
		if err != nil {
			discovery.warnRead(home, err)
		} else {
			for _, name := range discovery.readDirectories(homePath) {
				lexical := lexicalPath(filepath.Join(homePath, name))
				path, err := canonical(lexical)
				if err != nil {
					discovery.warnRead(filepath.Join(homePath, name), err)
					continue
				}
				if git, linked := gitStatus(path); git {
					discovery.add(lexical, homePath, git, linked)
				}
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

func canonical(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
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
		info, err := entry.Info()
		if err != nil {
			d.warnRead(filepath.Join(path, entry.Name()), err)
			continue
		}
		if info.IsDir() {
			names = append(names, entry.Name())
			continue
		}
		// A symlink to a directory is itself a project candidate. Stat is used
		// only to discover directory shape; Candidate.Path remains lexical so
		// deletion can Lstat and remove the link rather than its target.
		if entry.Type()&os.ModeSymlink != 0 {
			target, statErr := os.Stat(filepath.Join(path, entry.Name()))
			if statErr == nil && target.IsDir() {
				names = append(names, entry.Name())
			}
		}
	}
	return names
}

func (d *Discovery) addChildren(path, group string) {
	for _, name := range d.readDirectories(path) {
		lexical := lexicalPath(filepath.Join(path, name))
		candidate, err := canonical(lexical)
		if err != nil {
			d.warnRead(filepath.Join(path, name), err)
			continue
		}
		git, linked := gitStatus(candidate)
		// Keep the directory entry's lexical spelling. In particular, a
		// candidate that is itself a symlink must reach deletion preflight as a
		// link rather than as its resolved target.
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

func (d *Discovery) add(path, group string, status ...bool) {
	for _, candidate := range d.Candidates {
		if candidate.Path == path {
			return
		}
	}
	git, linked := false, false
	if len(status) > 0 {
		git = status[0]
	}
	if len(status) > 1 {
		linked = status[1]
	}
	d.Candidates = append(d.Candidates, Candidate{Path: path, Group: group, Git: git, LinkedWorktree: linked})
}

func gitStatus(path string) (bool, bool) {
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return false, false
	}
	return true, !info.IsDir()
}

func (d *Discovery) warnRead(path string, err error) {
	d.Warnings = append(d.Warnings, fmt.Sprintf("skip %q: %v", path, err))
}

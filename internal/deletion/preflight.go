// Package deletion owns deletion policy and the final mutation seam.
package deletion

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type Severity string

const (
	Warning Severity = "warning"
	Blocker Severity = "blocker"
)

type Finding struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

type GitState struct {
	IsRepository       bool     `json:"is_repository"`
	LinkedWorktree     bool     `json:"linked_worktree"`
	PrimaryPath        string   `json:"primary_path,omitempty"`
	DependentWorktrees []string `json:"dependent_worktrees,omitempty"`
	Dirty              bool     `json:"dirty"`
	Untracked          bool     `json:"untracked"`
	Unpushed           bool     `json:"unpushed"`
	Stashes            bool     `json:"stashes"`
	RemoteChecked      bool     `json:"remote_checked"`
	RemoteCheckError   string   `json:"remote_check_error,omitempty"`
	WorktreeLocked     bool     `json:"worktree_locked"`
	HasSubmodules      bool     `json:"has_submodules"`
	GitRefusal         string   `json:"git_refusal,omitempty"`
}

type Observation struct {
	Path                  string
	Candidate             bool
	Configured            bool
	Protected             bool
	ProtectedReason       string
	Exists                bool
	Directory             bool
	Symlink               bool
	NonEmpty              bool
	Fingerprint           string
	CurrentFingerprint    string
	PathChanged           bool
	AncestorCheckError    error
	ActiveContainer       bool
	ActiveCheckError      error
	Git                   GitState
	GitError              error
	RepositoryFingerprint string
}

type Plan struct {
	Path                  string    `json:"path"`
	Action                string    `json:"action"`
	Symlink               bool      `json:"symlink"`
	Warnings              []Finding `json:"warnings,omitempty"`
	Blockers              []Finding `json:"blockers,omitempty"`
	Fingerprint           string    `json:"fingerprint,omitempty"`
	Repository            string    `json:"repository,omitempty"`
	RepositoryFingerprint string    `json:"repository_fingerprint,omitempty"`
	CanProceed            bool      `json:"can_proceed"`
}

func (p Plan) HasWarnings() bool { return len(p.Warnings) != 0 }

// EffectiveAction returns the operation a user-requested mode will perform.
// A linked worktree always uses Git's worktree operation.
func (p Plan) EffectiveAction(permanent bool) string {
	if p.Action == "worktree" {
		return "worktree"
	}
	if permanent {
		return "permanent"
	}
	return "trash"
}

func Preflight(observation Observation) Plan {
	path := filepath.Clean(observation.Path)
	plan := Plan{Path: path, Fingerprint: observation.Fingerprint, RepositoryFingerprint: observation.RepositoryFingerprint}
	block := func(code, message string) {
		plan.Blockers = append(plan.Blockers, Finding{Code: code, Severity: Blocker, Message: message})
	}
	warn := func(code, message string) {
		plan.Warnings = append(plan.Warnings, Finding{Code: code, Severity: Warning, Message: message})
	}

	if observation.Path == "" {
		block("empty-path", "project path is empty")
	}
	if !observation.Candidate {
		block("not-candidate", fmt.Sprintf("%q is not a project candidate", path))
	}
	if !observation.Configured {
		block("outside-configured-paths", fmt.Sprintf("%q is outside configured project paths", path))
	}
	if path == string(filepath.Separator) || observation.Protected {
		reason := observation.ProtectedReason
		if path == string(filepath.Separator) {
			reason = "filesystem root"
		}
		if reason == "" {
			reason = "protected infrastructure path"
		}
		block("protected-path", fmt.Sprintf("%q is a protected %s", path, reason))
	}
	if !observation.Exists {
		block("missing-path", fmt.Sprintf("project path %q does not exist", path))
	}
	if observation.AncestorCheckError != nil {
		block("ancestor-check-failed", fmt.Sprintf("path traversal is unsafe: %v", observation.AncestorCheckError))
	}
	if observation.GitError != nil {
		block("git-check-failed", fmt.Sprintf("Git check failed: %v", observation.GitError))
	}
	if observation.ActiveCheckError != nil {
		block("active-check-failed", fmt.Sprintf("active-container check failed: %v", observation.ActiveCheckError))
	}
	if observation.ActiveContainer {
		block("active-container", fmt.Sprintf("an active container uses %q", path))
	}
	if observation.PathChanged || (observation.Fingerprint != "" && observation.CurrentFingerprint != "" && observation.Fingerprint != observation.CurrentFingerprint) {
		block("path-changed", fmt.Sprintf("project path %q changed after preflight", path))
	}
	if len(observation.Git.DependentWorktrees) != 0 && !observation.Git.LinkedWorktree {
		block("dependent-worktrees", fmt.Sprintf("primary checkout has linked worktrees: %s", strings.Join(observation.Git.DependentWorktrees, ", ")))
	}
	if observation.Git.IsRepository && !observation.Symlink && !observation.Git.RemoteChecked && observation.Git.RemoteCheckError == "" {
		block("remote-check-failed", "remote verification was not completed")
	}
	if observation.Git.RemoteCheckError != "" {
		block("remote-check-failed", fmt.Sprintf("remote verification failed: %s", observation.Git.RemoteCheckError))
	}
	if observation.Git.GitRefusal != "" {
		block("git-refusal", fmt.Sprintf("Git refused deletion: %s", observation.Git.GitRefusal))
	}
	if observation.Git.LinkedWorktree {
		if observation.Git.WorktreeLocked {
			block("worktree-locked", "linked worktree is locked")
		}
		if observation.Git.Dirty || observation.Git.Untracked {
			block("worktree-changed", "linked worktree has tracked or untracked changes")
		}
		if observation.Git.HasSubmodules {
			block("worktree-submodules", "linked worktree contains submodules")
		}
	}

	switch {
	case observation.Symlink:
		plan.Symlink, plan.Action = true, "trash"
	case observation.Git.LinkedWorktree:
		plan.Action, plan.Repository = "worktree", observation.Git.PrimaryPath
	case observation.Directory:
		plan.Action = "trash"
	case observation.Exists:
		block("not-directory", fmt.Sprintf("project path %q is not a directory", path))
		plan.Action = "trash"
	}
	if observation.Directory && !observation.Symlink && observation.NonEmpty {
		warn("non-empty-directory", fmt.Sprintf("%q is a non-empty directory", path))
	}
	if observation.Git.IsRepository && !observation.Symlink && (observation.Git.Unpushed || observation.Git.Stashes || observation.Git.Untracked || observation.Git.Dirty) {
		local := make([]string, 0, 4)
		if observation.Git.Unpushed {
			local = append(local, "unpushed commits")
		}
		if observation.Git.Stashes {
			local = append(local, "stashes")
		}
		if observation.Git.Untracked {
			local = append(local, "untracked files")
		}
		if observation.Git.Dirty {
			local = append(local, "dirty files")
		}
		warn("local-git-data", fmt.Sprintf("local-only Git data: %s", strings.Join(local, ", ")))
	}
	plan.CanProceed = len(plan.Blockers) == 0
	return plan
}

type FileSystem interface {
	Lstat(string) (os.FileInfo, error)
	ReadDir(string) ([]os.DirEntry, error)
	Remove(string) error
	RemoveAll(string) error
}

type OSFileSystem struct{}

func (OSFileSystem) Lstat(path string) (os.FileInfo, error)     { return os.Lstat(path) }
func (OSFileSystem) ReadDir(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }
func (OSFileSystem) Remove(path string) error                   { return os.Remove(path) }
func (OSFileSystem) RemoveAll(path string) error                { return os.RemoveAll(path) }

func Fingerprint(fs FileSystem, path string) (string, error) {
	if fs == nil {
		fs = OSFileSystem{}
	}
	info, err := fs.Lstat(path)
	if err != nil {
		return "", err
	}
	return fingerprint(info), nil
}

func fingerprint(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	identity := []string{}
	value := reflect.ValueOf(info.Sys())
	if value.IsValid() {
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				value = reflect.Value{}
			} else {
				value = value.Elem()
			}
		}
		if value.IsValid() && value.Kind() == reflect.Struct {
			for _, name := range []string{"Dev", "Ino", "FileIndexHigh", "FileIndexLow", "VolumeSerialNumber"} {
				field := value.FieldByName(name)
				if field.IsValid() && field.CanInterface() {
					identity = append(identity, fmt.Sprintf("%s=%v", name, field.Interface()))
				}
			}
		}
	}
	return fmt.Sprintf("%s|%d|%d|%s", info.Mode().String(), info.Size(), info.ModTime().UnixNano(), strings.Join(identity, ","))
}

func InspectFilesystem(fs FileSystem, path string) (Observation, error) {
	if fs == nil {
		fs = OSFileSystem{}
	}
	path = lexicalAbs(path)
	observation := Observation{Path: path, Configured: true}
	if err := validateAncestors(fs, path); err != nil && !errors.Is(err, os.ErrNotExist) {
		observation.AncestorCheckError = err
	}
	info, err := fs.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return observation, nil
		}
		return observation, err
	}
	observation.Exists = true
	observation.Fingerprint = fingerprint(info)
	observation.CurrentFingerprint = observation.Fingerprint
	observation.Symlink = info.Mode()&os.ModeSymlink != 0
	observation.Directory = info.IsDir()
	if observation.Directory && !observation.Symlink {
		entries, readErr := fs.ReadDir(path)
		if readErr != nil {
			return observation, readErr
		}
		observation.NonEmpty = len(entries) != 0
	}
	return observation, nil
}

func validateAncestors(fs FileSystem, path string) error {
	for current := filepath.Dir(path); ; current = filepath.Dir(current) {
		info, err := fs.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ancestor %q is a symlink", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func lexicalAbs(path string) string {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

func FormatFindings(findings []Finding) string {
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, finding.Message)
	}
	return strings.Join(parts, "; ")
}

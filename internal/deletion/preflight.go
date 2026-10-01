// Package deletion owns the safety policy for removing project candidates.
// It separates read-only classification from mutation so every caller can
// present (and test) the complete preflight before anything is removed.
package deletion

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// Severity is the safety level of one preflight finding.
type Severity string

const (
	Warning Severity = "warning"
	Blocker Severity = "blocker"
)

// Finding is one exhaustive preflight result. Blockers are never bypassed;
// warnings require an explicit force/confirmation at the caller.
type Finding struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// GitState contains only facts needed by deletion policy. It is produced by a
// Git adapter and consumed by the pure Preflight function.
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

// Observation is the complete read-only input to Preflight. Callers gather
// it before presenting a confirmation UI; Preflight itself performs no I/O.
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

// Plan is the complete classification and the mutation kind chosen after a
// successful preflight. Warnings remain in the plan so a UI can show them.
type Plan struct {
	Path                  string    `json:"path"`
	Action                string    `json:"action"` // trash, permanent, or worktree
	Symlink               bool      `json:"symlink"`
	Warnings              []Finding `json:"warnings,omitempty"`
	Blockers              []Finding `json:"blockers,omitempty"`
	Fingerprint           string    `json:"fingerprint,omitempty"`
	Repository            string    `json:"repository,omitempty"`
	RepositoryFingerprint string    `json:"repository_fingerprint,omitempty"`
	CanProceed            bool      `json:"can_proceed"`
}

// HasWarnings reports whether force/explicit confirmation is required.
func (p Plan) HasWarnings() bool { return len(p.Warnings) > 0 }

// Preflight exhaustively classifies a selected project path. This function is
// deliberately pure: no path is resolved, followed, or mutated here.
func Preflight(observation Observation) Plan {
	path := filepath.Clean(observation.Path)
	plan := Plan{Path: path, Fingerprint: observation.Fingerprint, RepositoryFingerprint: observation.RepositoryFingerprint}
	addBlocker := func(code, message string) {
		plan.Blockers = append(plan.Blockers, Finding{Code: code, Severity: Blocker, Message: message})
	}
	addWarning := func(code, message string) {
		plan.Warnings = append(plan.Warnings, Finding{Code: code, Severity: Warning, Message: message})
	}

	if observation.Path == "" {
		addBlocker("empty-path", "project path is empty")
	}
	if !observation.Candidate {
		addBlocker("not-candidate", fmt.Sprintf("%q is not a project candidate", path))
	}
	if !observation.Configured {
		addBlocker("outside-configured-paths", fmt.Sprintf("%q is outside configured project paths", path))
	}
	if path == filepath.Clean(string(filepath.Separator)) || observation.Protected {
		reason := observation.ProtectedReason
		if path == filepath.Clean(string(filepath.Separator)) {
			reason = "filesystem root"
		}
		if reason == "" {
			reason = "protected infrastructure path"
		}
		addBlocker("protected-path", fmt.Sprintf("%q is a protected %s", path, reason))
	}
	if !observation.Exists {
		addBlocker("missing-path", fmt.Sprintf("project path %q does not exist", path))
	}
	if observation.AncestorCheckError != nil {
		addBlocker("ancestor-check-failed", fmt.Sprintf("path traversal is unsafe: %v", observation.AncestorCheckError))
	}
	if observation.GitError != nil {
		addBlocker("git-check-failed", fmt.Sprintf("Git check failed: %v", observation.GitError))
	}
	if observation.ActiveCheckError != nil {
		addBlocker("active-check-failed", fmt.Sprintf("active-container check failed: %v", observation.ActiveCheckError))
	}
	if observation.ActiveContainer {
		addBlocker("active-container", fmt.Sprintf("an active container uses %q", path))
	}
	if observation.PathChanged || (observation.Fingerprint != "" && observation.CurrentFingerprint != "" && observation.Fingerprint != observation.CurrentFingerprint) {
		addBlocker("path-changed", fmt.Sprintf("project path %q changed after preflight", path))
	}
	if len(observation.Git.DependentWorktrees) > 0 && !observation.Git.LinkedWorktree {
		addBlocker("dependent-worktrees", fmt.Sprintf("primary checkout has linked worktrees: %s", strings.Join(observation.Git.DependentWorktrees, ", ")))
	}
	if observation.Git.IsRepository && !observation.Symlink && !observation.Git.RemoteChecked && observation.Git.RemoteCheckError == "" {
		addBlocker("remote-check-failed", "remote verification was not completed")
	}
	if observation.Git.RemoteCheckError != "" {
		addBlocker("remote-check-failed", fmt.Sprintf("remote verification failed: %s", observation.Git.RemoteCheckError))
	}
	if observation.Git.GitRefusal != "" {
		addBlocker("git-refusal", fmt.Sprintf("Git refused deletion: %s", observation.Git.GitRefusal))
	}
	if observation.Git.LinkedWorktree {
		if observation.Git.WorktreeLocked {
			addBlocker("worktree-locked", "linked worktree is locked")
		}
		if observation.Git.Dirty || observation.Git.Untracked {
			addBlocker("worktree-changed", "linked worktree has tracked or untracked changes")
		}
		if observation.Git.HasSubmodules {
			addBlocker("worktree-submodules", "linked worktree contains submodules")
		}
	}

	if observation.Symlink {
		plan.Symlink = true
		plan.Action = "trash"
	} else if observation.Git.LinkedWorktree {
		plan.Action = "worktree"
		plan.Repository = observation.Git.PrimaryPath
	} else if observation.Directory {
		plan.Action = "trash"
	} else if observation.Exists {
		addBlocker("not-directory", fmt.Sprintf("project path %q is not a directory", path))
		plan.Action = "trash"
	}
	if observation.Directory && !observation.Symlink && observation.NonEmpty {
		addWarning("non-empty-directory", fmt.Sprintf("%q is a non-empty directory", path))
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
		addWarning("local-git-data", fmt.Sprintf("local-only Git data: %s", strings.Join(local, ", ")))
	}
	plan.CanProceed = len(plan.Blockers) == 0
	return plan
}

// FileSystem is the mutation/read seam used by deletion. Implementations in
// tests can be rooted temporary filesystems; the production adapter uses os.
type FileSystem interface {
	Lstat(string) (os.FileInfo, error)
	ReadDir(string) ([]os.DirEntry, error)
	Remove(string) error
	RemoveAll(string) error
}

// IdentityStager binds a mutation to the inode observed during preflight. A
// successful stage moves the exact lexical entry out of the user-visible
// pathname before trashing or removing it, so a later pathname replacement
// cannot redirect the destructive operation.
type IdentityStager interface {
	Stage(string, string) (string, error)
}

type identityRestorer interface {
	Restore(string, string) error
}

type OSFileSystem struct{}

func (OSFileSystem) Lstat(path string) (os.FileInfo, error)     { return os.Lstat(path) }
func (OSFileSystem) ReadDir(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }
func (OSFileSystem) Remove(path string) error                   { return os.Remove(path) }
func (OSFileSystem) RemoveAll(path string) error                { return os.RemoveAll(path) }

// Stage atomically moves a path to a private sibling after checking the
// expected Lstat identity. The stage directory keeps the original basename so
// an OS trash adapter sees the user's logical name rather than a random one.
// The second identity check catches a replacement racing with the rename before
// any destructive operation is attempted.
func (OSFileSystem) Stage(path, expected string) (string, error) {
	if expected == "" {
		return "", errors.New("cannot stage a path without an identity")
	}
	if err := validateAncestors(OSFileSystem{}, path); err != nil {
		return "", fmt.Errorf("validate deletion path: %w", err)
	}
	current, err := Fingerprint(OSFileSystem{}, path)
	if err != nil {
		return "", err
	}
	if current != expected {
		return "", fmt.Errorf("path changed since preflight: %q", path)
	}
	parent := filepath.Dir(path)
	stageDir, err := os.MkdirTemp(parent, ".nexo-delete-*")
	if err != nil {
		return "", fmt.Errorf("prepare deletion stage: %w", err)
	}
	staged := filepath.Join(stageDir, filepath.Base(path))
	if err := validateAncestors(OSFileSystem{}, path); err != nil {
		_ = os.Remove(stageDir)
		return "", fmt.Errorf("validate deletion path before stage: %w", err)
	}
	current, err = Fingerprint(OSFileSystem{}, path)
	if err != nil {
		_ = os.Remove(stageDir)
		return "", err
	}
	if current != expected {
		_ = os.Remove(stageDir)
		return "", fmt.Errorf("path changed while staging: %q", path)
	}
	if err := os.Rename(path, staged); err != nil {
		_ = os.Remove(stageDir)
		return "", fmt.Errorf("stage %q: %w", path, err)
	}
	stagedFingerprint, err := Fingerprint(OSFileSystem{}, staged)
	if err != nil {
		return staged, fmt.Errorf("verify staged %q: %w", path, err)
	}
	if stagedFingerprint != expected {
		return staged, fmt.Errorf("path changed while staging: %q", path)
	}
	return staged, nil
}

// Restore puts a staged path back only when the original lexical name is
// currently absent. It never overwrites a replacement installed by another
// process. The private stage directory is removed only after the inode has
// returned to its original pathname.
func (OSFileSystem) Restore(staged, path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("cannot restore %q: pathname has been replaced", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staged, path); err != nil {
		return err
	}
	return removeStageDir(staged)
}

// CleanupStaged removes the now-empty private stage directory after a
// successful trash or permanent delete. It never touches the original parent.
func (OSFileSystem) CleanupStaged(staged string) error { return removeStageDir(staged) }

func removeStageDir(staged string) error {
	dir := filepath.Dir(staged)
	if !strings.HasPrefix(filepath.Base(dir), ".nexo-delete-") {
		return nil
	}
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove deletion stage %q: %w", dir, err)
	}
	return nil
}

// Fingerprint returns a lexical-path identity used to detect replacement
// between preflight and execution. Lstat intentionally never follows links.
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
	// Sys() contains access timestamps on Unix, which can change merely by
	// reading a directory during preflight. Keep stable inode/device fields
	// while excluding those observation-only timestamps.
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

// InspectFilesystem captures the path facts needed by Preflight. It uses
// Lstat and ReadDir, so a selected symlink remains a symlink identity.
func InspectFilesystem(fs FileSystem, path string) (Observation, error) {
	if fs == nil {
		fs = OSFileSystem{}
	}
	path = lexicalAbs(path)
	observation := Observation{Path: path, Configured: true}
	if ancestorErr := validateAncestors(fs, path); ancestorErr != nil && !errors.Is(ancestorErr, os.ErrNotExist) {
		observation.AncestorCheckError = ancestorErr
	}
	info, err := fs.Lstat(path)
	if err != nil {
		observation.GitError = nil
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
	if absolute, err := filepath.Abs(filepath.Clean(path)); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

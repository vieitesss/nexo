// Package upgrade contains version selection and machine-result policy for the
// upgrade command. Installation mechanics live in internal/install so setup
// can share them without importing command dispatch.
package upgrade

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/vieitesss/nexo/internal/runner"
)

const DefaultRepository = "git@github.com:vieitesss/nexo.git"

// VersionRequest describes a requested release or the main branch.
type VersionRequest struct {
	Version    string
	Nightly    bool
	Repository string
}

// Version is the resolved source reference. Release is the user-visible tag;
// Ref is the source ref: a release tag, or main's resolved commit for nightly.
type Version struct {
	Release string
	Ref     string
	Nightly bool
}

// Label renders the resolved version for dry runs and errors.
func (v Version) Label() string {
	if v.Nightly {
		return "main@" + shortCommit(v.Ref)
	}
	return v.Release
}

func (r VersionRequest) repository() string {
	if strings.TrimSpace(r.Repository) == "" {
		return DefaultRepository
	}
	return strings.TrimSpace(r.Repository)
}

// releasePattern accepts the documented stable vX.Y.Z form. Prereleases and
// build metadata are rejected rather than ordered by ad-hoc string rules.
var releasePattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

// Resolve authenticates through git's configured credentials and resolves a
// request without falling back from an absent release to main.
func Resolve(ctx context.Context, commandRunner runner.CommandRunner, request VersionRequest) (Version, error) {
	if commandRunner == nil {
		return Version{}, errors.New("version resolution requires a command runner")
	}
	if request.Nightly {
		output, err := commandRunner.Run(ctx, "git", "ls-remote", request.repository(), "refs/heads/main")
		if err != nil {
			return Version{}, fmt.Errorf("resolve nightly main: %w", runner.Detail(err))
		}
		commit := strings.TrimSpace(outputField(string(output), 0))
		if commit == "" {
			return Version{}, errors.New("main has no commit; check authenticated Git access")
		}
		return Version{Release: "main", Ref: commit, Nightly: true}, nil
	}

	output, err := commandRunner.Run(ctx, "git", "ls-remote", "--tags", "--refs", request.repository())
	if err != nil {
		return Version{}, fmt.Errorf("resolve releases: %w", runner.Detail(err))
	}
	tags := releaseTags(string(output))
	if request.Version != "" {
		if !releasePattern.MatchString(request.Version) {
			return Version{}, fmt.Errorf("invalid release version %q (want vX.Y.Z)", request.Version)
		}
		if !tags[request.Version] {
			return Version{}, fmt.Errorf("release %s was not found; use --nightly for main", request.Version)
		}
		return Version{Release: request.Version, Ref: request.Version}, nil
	}
	if len(tags) == 0 {
		return Version{}, errors.New("no tagged release is published; use --nightly to install main")
	}
	latest := make([]string, 0, len(tags))
	for tag := range tags {
		latest = append(latest, tag)
	}
	sort.Slice(latest, func(i, j int) bool { return compareRelease(latest[i], latest[j]) > 0 })
	return Version{Release: latest[0], Ref: latest[0]}, nil
}

func releaseTags(output string) map[string]bool {
	tags := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		const prefix = "refs/tags/"
		if !strings.HasPrefix(fields[1], prefix) {
			continue
		}
		tag := strings.TrimPrefix(fields[1], prefix)
		if releasePattern.MatchString(tag) {
			tags[tag] = true
		}
	}
	return tags
}

func compareRelease(a, b string) int {
	left := releasePattern.FindStringSubmatch(a)
	right := releasePattern.FindStringSubmatch(b)
	for i := 1; i <= 3; i++ {
		lv, _ := strconv.Atoi(left[i])
		rv, _ := strconv.Atoi(right[i])
		if lv != rv {
			if lv > rv {
				return 1
			}
			return -1
		}
	}
	return 0
}

func shortCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func outputField(value string, index int) string {
	fields := strings.Fields(value)
	if index < 0 || index >= len(fields) {
		return ""
	}
	return fields[index]
}

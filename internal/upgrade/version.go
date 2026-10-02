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
// Ref is the source ref passed to go install or a local build.
type Version struct {
	Release string
	Ref     string
	Nightly bool
}

func (r VersionRequest) repository() string {
	if strings.TrimSpace(r.Repository) == "" {
		return DefaultRepository
	}
	return strings.TrimSpace(r.Repository)
}

var releasePattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)([-+][0-9A-Za-z.-]+)?$`)

// Resolve authenticates through git's configured credentials and resolves a
// request without falling back from an absent release to main.
func Resolve(ctx context.Context, commandRunner runner.CommandRunner, request VersionRequest) (Version, error) {
	if commandRunner == nil {
		return Version{}, errors.New("version resolution requires a command runner")
	}
	if request.Nightly {
		output, err := commandRunner.Run(ctx, "git", "ls-remote", request.repository(), "refs/heads/main")
		if err != nil {
			return Version{}, fmt.Errorf("resolve nightly main: %w", err)
		}
		if strings.TrimSpace(outputField(string(output), 0)) == "" {
			return Version{}, errors.New("main has no commit; check authenticated Git access")
		}
		return Version{Release: "main", Ref: "main", Nightly: true}, nil
	}

	output, err := commandRunner.Run(ctx, "git", "ls-remote", "--tags", "--refs", request.repository())
	if err != nil {
		return Version{}, fmt.Errorf("resolve releases: %w", err)
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
	// Stable releases sort after prereleases with the same numeric version.
	if left[4] == "" && right[4] != "" {
		return 1
	}
	if left[4] != "" && right[4] == "" {
		return -1
	}
	// A lexical tie-breaker keeps sorting deterministic for prereleases.
	return strings.Compare(a, b)
}

func outputField(value string, index int) string {
	fields := strings.Fields(value)
	if index < 0 || index >= len(fields) {
		return ""
	}
	return fields[index]
}

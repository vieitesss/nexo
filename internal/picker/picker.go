// Package picker contains backend-neutral place and fuzzy-search domain helpers.
package picker

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/projects"
)

// PlaceKind identifies the kind of place represented by a picker item. New
// place kinds can be added without changing the row model.
type PlaceKind string

const (
	// ProjectKind is a configured project candidate.
	ProjectKind PlaceKind = "project"
	// ContainerKind is an open local (or future remote) container.
	ContainerKind PlaceKind = "container"
)

// OpenAction is an optional action owned by a place source. Project places
// leave it nil so the active backend can open them; future place kinds can
// carry their own action without changing the picker model.
type OpenAction func(context.Context, Place) error

type ContainerRef struct {
	Machine string `json:"machine"`
	ID      string `json:"id"`
}

// RemoteBatch is one asynchronous machine listing. Error keeps an unreachable
// machine visible without turning a partial picker result into a failure.
// Warning reports a non-fatal gap such as unavailable remote agent status.
type RemoteBatch struct {
	Machine string
	Backend string
	Places  []Place
	Agents  []capabilities.Agent
	Warning string
	Error   string
}

// Place is the additive item model used by the picker. SearchText and
// DisplayRemainder keep ranking and rendering independent of filesystem paths;
// Path and Group remain populated for project places.
type Place struct {
	Kind             PlaceKind  `json:"kind"`
	Path             string     `json:"path,omitempty"`
	Group            string     `json:"group,omitempty"`
	Machine          string     `json:"machine,omitempty"`
	SearchText       string     `json:"search_text,omitempty"`
	DisplayRemainder string     `json:"display_remainder,omitempty"`
	ContainerID      string     `json:"container_id,omitempty"`
	ContainerName    string     `json:"container_name,omitempty"`
	Preview          string     `json:"preview,omitempty"`
	Activity         *time.Time `json:"activity,omitempty"`
	Proxy            bool       `json:"proxy,omitempty"`
	OpenAction       OpenAction `json:"-"`
	Git              bool       `json:"git,omitempty"`
	LinkedWorktree   bool       `json:"linked_worktree,omitempty"`
	// GitContext is populated asynchronously for Git project candidates.
	GitContext *GitContext `json:"-"`
	// PullRequest is populated asynchronously from the checkout's upstream;
	// nil hides the badge.
	PullRequest *PullRequest `json:"-"`
	// Agents is the latest asynchronous agent snapshot for a container.
	Agents []capabilities.Agent `json:"-"`
	// ParentPath is the primary checkout path for a linked worktree.
	ParentPath string `json:"-"`
}

// ProjectPlaces converts project discovery results into picker places while
// preserving discovery order (which is the tie-breaker for equal scores).
func ProjectPlaces(candidates []projects.Candidate) []Place {
	places := make([]Place, 0, len(candidates))
	for _, candidate := range candidates {
		places = append(places, Place{
			Kind:             ProjectKind,
			Path:             candidate.Path,
			Group:            candidate.Group,
			SearchText:       candidate.Path,
			DisplayRemainder: projectRemainder(candidate.Path, candidate.Group),
			Git:              candidate.Git,
			LinkedWorktree:   candidate.LinkedWorktree,
		})
	}
	return places
}

// ContainerPlaces converts backend containers to machine-grouped picker
// places. The machine field is deliberately separate from the group so remote
// sources can add more machines without changing the row model.
func ContainerPlaces(containers []capabilities.Container) []Place {
	places := make([]Place, 0, len(containers))
	for _, container := range containers {
		machine := container.Machine
		if machine == "" {
			machine = containerstate.LocalMachine()
		}
		places = append(places, Place{
			Kind:             ContainerKind,
			Group:            machine,
			Machine:          machine,
			SearchText:       container.Name,
			DisplayRemainder: container.Name,
			ContainerID:      container.ID,
			ContainerName:    container.Name,
			Preview:          container.Preview,
			Activity:         container.Activity,
			Proxy:            container.Proxy,
		})
	}
	return places
}

// MergeContainerPlaces folds a later container snapshot into base rows with
// the same key. Proxy rows and remote container rows have distinct keys, so a
// proxy never absorbs remote metadata; only the first open action fills in.
func MergeContainerPlaces(base, extra []Place) []Place {
	merged := append([]Place(nil), base...)
	indexes := make(map[string]int)
	for index, place := range merged {
		if place.Kind != ContainerKind {
			continue
		}
		key := containerPlaceKey(place)
		if _, ok := indexes[key]; !ok {
			indexes[key] = index
		}
	}
	for _, place := range extra {
		if place.Kind != ContainerKind {
			merged = append(merged, place)
			continue
		}
		key := containerPlaceKey(place)
		index, exists := indexes[key]
		if !exists {
			indexes[key] = len(merged)
			merged = append(merged, place)
			continue
		}
		if merged[index].OpenAction == nil {
			merged[index].OpenAction = place.OpenAction
		}
	}
	return merged
}

func containerPlaceKey(place Place) string {
	if place.Proxy {
		// A machine proxy is named for its machine, while the remote session it
		// currently shows may legitimately have the same name. Keep proxy rows
		// keyed by their local identity so closing one can never target the
		// remote row that happens to share its display label.
		return "proxy\x00" + strings.ToLower(strings.TrimSpace(place.Machine)) + "\x00" + place.ContainerID
	}
	return strings.ToLower(strings.TrimSpace(place.Machine)) + "\x00" + place.ContainerName
}

// Match is the result of matching a query against text.
type Match struct {
	Score     int
	Positions []int // rune positions in the matched text
}

// Rank performs nexo's own fuzzy ranking. A query matches when its runes are
// a case-insensitive subsequence of text. Contiguous and prefix matches rank
// ahead of sparse matches; the original order remains the final tie-breaker.
func Rank(query, text string) (Match, bool) {
	queryRunes := []rune(strings.ToLower(query))
	textRunes := []rune(strings.ToLower(text))
	if len(queryRunes) == 0 {
		return Match{Score: 0}, true
	}
	positions := make([]int, 0, len(queryRunes))
	cursor := 0
	for _, wanted := range queryRunes {
		found := -1
		for cursor < len(textRunes) {
			if textRunes[cursor] == wanted {
				found = cursor
				cursor++
				break
			}
			cursor++
		}
		if found == -1 {
			return Match{}, false
		}
		positions = append(positions, found)
	}

	score := len(queryRunes) * 10
	if positions[0] == 0 {
		score += 30
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] == positions[i-1]+1 {
			score += 15
		} else {
			score -= positions[i] - positions[i-1] - 1
		}
	}
	score -= positions[0]
	return Match{Score: score, Positions: positions}, true
}

// GroupKey identifies a project parent or container machine.
type GroupKey struct {
	Kind PlaceKind
	Name string
}

// PlaceGroupKey returns the grouping identity used by scripted listings.
func PlaceGroupKey(place Place) GroupKey {
	if place.Kind == ContainerKind {
		machine := place.Machine
		if machine == "" {
			machine = place.Group
		}
		return GroupKey{Kind: ContainerKind, Name: machine}
	}
	return GroupKey{Kind: ProjectKind, Name: place.Group}
}

func projectRemainder(path, group string) string {
	if group == "" {
		return filepath.ToSlash(path)
	}
	remainder, err := filepath.Rel(group, path)
	if err == nil && remainder != "." && remainder != ".." && !strings.HasPrefix(remainder, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(remainder)
	}
	return filepath.ToSlash(path)
}

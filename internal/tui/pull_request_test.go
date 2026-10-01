package tui

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

func pullRequestPlace(branch string, request *picker.PullRequest) picker.Place {
	return picker.Place{
		Kind: picker.ProjectKind, Path: "/work/" + branch, Group: "/work", DisplayRemainder: branch,
		Git: true, GitContext: &picker.GitContext{Identity: branch}, PullRequest: request,
	}
}

func pullRequestPlaces(branches ...string) []picker.Place {
	places := make([]picker.Place, 0, len(branches))
	for _, branch := range branches {
		places = append(places, pullRequestPlace(branch, nil))
	}
	return places
}

// referenceANSIValue maps the ANSI 256 indices the badge uses to the
// conventional xterm values, so the contrast fixture is concrete instead of
// derived from the renderer.
var referenceANSIValue = map[string]string{
	"22":  "#005f00",
	"90":  "#870087",
	"88":  "#870000",
	"46":  "#00ff00",
	"219": "#ffafff",
	"217": "#ffafaf",
}

// pullRequestMinimumContrast is the WCAG AA ratio the badge colour keeps
// against the reference backgrounds. A terminal palette replaces these
// reference values, so this is a fixture, not a universal guarantee.
const pullRequestMinimumContrast = 4.5

func TestProjectsRenderPullRequestBadgeByState(t *testing.T) {
	previousProfile, previousDark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		lipgloss.SetHasDarkBackground(previousDark)
	})
	lipgloss.SetColorProfile(termenv.ANSI256)
	for _, test := range []struct {
		name        string
		number      int
		state       picker.PullRequestState
		light, dark string
	}{
		{"open", 12, picker.PullRequestOpen, "22", "46"},
		{"merged", 34, picker.PullRequestMerged, "90", "219"},
		{"closed", 56, picker.PullRequestClosed, "88", "217"},
	} {
		for _, variant := range []struct {
			name  string
			dark  bool
			index string
		}{{"light", false, test.light}, {"dark", true, test.dark}} {
			t.Run(test.name+"/"+variant.name, func(t *testing.T) {
				lipgloss.SetHasDarkBackground(variant.dark)
				request := &picker.PullRequest{Number: test.number, State: test.state}
				model := NewModel(context.Background(), Deps{Projects: []picker.Place{pullRequestPlace("main", request)}}, ProjectsTab)
				model.SetSize(120, 8)
				model = updateModel(t, model, keyMsg(tea.KeyRight))
				model.input.cursor = "group:/work"
				view := model.View()
				want := "\x1b[38;5;" + variant.index + "m#" + itoa(test.number) + "\x1b[0m"
				if !strings.Contains(view, want) {
					t.Fatalf("badge %q missing from view: %q", want, view)
				}
				for _, line := range strings.Split(view, "\n") {
					if !strings.Contains(shell.Plain(line), "#"+itoa(test.number)) {
						continue
					}
					if covered, _ := backgroundCoverage(line); covered != 0 {
						t.Fatalf("badge painted %d cells of background: %q", covered, line)
					}
				}
				if plain := shell.Plain(view); !strings.Contains(plain, "⎇ main  #"+itoa(test.number)) {
					t.Fatalf("badge does not follow the branch: %q", plain)
				}
			})
		}
	}
}

func TestSelectedProjectKeepsThePullRequestStateColour(t *testing.T) {
	previousProfile, previousDark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		lipgloss.SetHasDarkBackground(previousDark)
	})
	lipgloss.SetColorProfile(termenv.ANSI256)
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			lipgloss.SetHasDarkBackground(dark)
			seen := map[string]picker.PullRequestState{}
			for _, state := range []picker.PullRequestState{picker.PullRequestOpen, picker.PullRequestMerged, picker.PullRequestClosed} {
				request := &picker.PullRequest{Number: 12, State: state}
				model := NewModel(context.Background(), Deps{Projects: []picker.Place{pullRequestPlace("main", request)}}, ProjectsTab)
				model.SetSize(120, 8)
				model = updateModel(t, model, keyMsg(tea.KeyRight))
				model = updateModel(t, model, keyMsg(tea.KeyDown))
				line := ""
				for _, candidate := range strings.Split(model.View(), "\n") {
					if strings.Contains(shell.Plain(candidate), "#12") {
						line = candidate
						break
					}
				}
				if line == "" {
					t.Fatalf("state %s: selected row has no badge: %q", state, model.View())
				}
				want := pullRequestStyle(state).Render("#12")
				if !strings.Contains(line, want) {
					t.Fatalf("state %s lost its colour when selected: %q", state, line)
				}
				if strings.Contains(want, "\x1b[48") {
					t.Fatalf("state %s badge paints its own background", state)
				}
				if covered, total := backgroundCoverage(line); total == 0 || covered != total {
					t.Fatalf("selected row background = %d/%d cells; want the whole row", covered, total)
				}
				if other, ok := seen[want]; ok {
					t.Fatalf("states %s and %s share the selected colour", state, other)
				}
				seen[want] = state
			}
		})
	}
}

func TestPullRequestBadgeMeetsTheReferenceContrastTarget(t *testing.T) {
	painted := selectionBackground()
	if painted.Light != "#d9eaf7" || painted.Dark != "#31516f" {
		t.Fatalf("selection background = %s/%s, want the fixture #d9eaf7/#31516f", painted.Light, painted.Dark)
	}
	for _, test := range []struct {
		name       string
		background string
		dark       bool
	}{
		{"light terminal", "#ffffff", false},
		{"light selection", "#d9eaf7", false},
		{"dark terminal", "#0f1115", true},
		{"dark selection", "#31516f", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, state := range []picker.PullRequestState{picker.PullRequestOpen, picker.PullRequestMerged, picker.PullRequestClosed} {
				color := pullRequestPalette[state]
				index := color.Light
				if test.dark {
					index = color.Dark
				}
				value, ok := referenceANSIValue[index]
				if !ok {
					t.Fatalf("state %s uses ANSI index %s with no reference value", state, index)
				}
				if ratio := referenceContrast(t, value, test.background); ratio < pullRequestMinimumContrast {
					t.Fatalf("state %s on %s = %.2f:1, want at least %.1f:1", state, test.background, ratio, pullRequestMinimumContrast)
				}
			}
		})
	}
}

func TestPullRequestPaletteDistinguishesStates(t *testing.T) {
	seen := map[string]picker.PullRequestState{}
	for _, state := range []picker.PullRequestState{picker.PullRequestOpen, picker.PullRequestMerged, picker.PullRequestClosed} {
		for _, index := range []string{pullRequestPalette[state].Light, pullRequestPalette[state].Dark} {
			if other, ok := seen[index]; ok {
				t.Fatalf("states %s and %s share ANSI index %s", state, other, index)
			}
			seen[index] = state
		}
	}
}

func referenceContrast(t *testing.T, foreground, background string) float64 {
	t.Helper()
	a, b := referenceLuminance(t, foreground), referenceLuminance(t, background)
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

func referenceLuminance(t *testing.T, value string) float64 {
	t.Helper()
	if len(value) != 7 || value[0] != '#' {
		t.Fatalf("invalid reference colour %q", value)
	}
	parsed, err := strconv.ParseUint(value[1:], 16, 32)
	if err != nil {
		t.Fatalf("invalid reference colour %q: %v", value, err)
	}
	channel := func(raw uint64) float64 {
		normalized := float64(raw) / 255
		if normalized <= 0.04045 {
			return normalized / 12.92
		}
		return math.Pow((normalized+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(parsed>>16&0xff) + 0.7152*channel(parsed>>8&0xff) + 0.0722*channel(parsed&0xff)
}

func TestProjectsHideMissingPullRequest(t *testing.T) {
	for _, request := range []*picker.PullRequest{nil, {Number: 0, State: picker.PullRequestOpen}} {
		model := NewModel(context.Background(), Deps{Projects: []picker.Place{pullRequestPlace("main", request)}}, ProjectsTab)
		model.SetSize(120, 8)
		model = updateModel(t, model, keyMsg(tea.KeyRight))
		if plain := shell.Plain(model.View()); strings.Contains(plain, "#") {
			t.Fatalf("missing pull request rendered a badge: %q", plain)
		}
	}
}

func TestProjectPullRequestJoinsTheGitColumnWidth(t *testing.T) {
	place := pullRequestPlace("main", &picker.PullRequest{Number: 12, State: picker.PullRequestOpen})
	place.GitContext.Adds = 1
	model := NewModel(context.Background(), Deps{Projects: []picker.Place{place}}, ProjectsTab)
	model.SetSize(200, 8)
	model = updateModel(t, model, keyMsg(tea.KeyRight))
	if got, want := model.frame().projectLayout().git, shell.StringWidth("⎇ main  #12  +1"); got != want {
		t.Fatalf("Git column = %d, want %d to fit the pull request badge", got, want)
	}
}

func TestPullRequestResultsRespectTheProjectGeneration(t *testing.T) {
	model := NewModel(context.Background(), Deps{Projects: []picker.Place{pullRequestPlace("main", nil)}}, ProjectsTab)
	source := model.source
	source.generations.projects = 3
	stale := pullRequestMsg{generation: 2, path: "/work/main", pullRequest: picker.PullRequest{Number: 12, State: picker.PullRequestOpen}}
	if _, ok := source.Accept(stale); ok {
		t.Fatal("stale pull request result was accepted")
	}
	fresh := pullRequestMsg{generation: 3, path: "/work/main", pullRequest: picker.PullRequest{Number: 12, State: picker.PullRequestOpen}}
	update, ok := source.Accept(fresh)
	if !ok || !update.changed || update.pullRequest == nil || update.pullRequest.pullRequest.Number != 12 {
		t.Fatalf("fresh pull request acceptance = %#v, %t", update, ok)
	}
}

func TestPullRequestMessageDecoratesTheProjectPlace(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	model := NewModel(context.Background(), Deps{Projects: []picker.Place{pullRequestPlace("main", nil)}}, ProjectsTab)
	model.SetSize(120, 8)
	updated, _ := model.Update(pullRequestMsg{path: "/work/main", pullRequest: picker.PullRequest{Number: 42, State: picker.PullRequestMerged}})
	model = updated.(Model)
	if request := model.deps.Projects[0].PullRequest; request == nil || request.Number != 42 {
		t.Fatalf("place was not decorated: %#v", request)
	}
	model = updateModel(t, model, keyMsg(tea.KeyRight))
	model.input.cursor = "group:/work"
	if view := model.View(); !strings.Contains(view, pullRequestStyle(picker.PullRequestMerged).Render("#42")) {
		t.Fatalf("badge missing after acceptance: %q", view)
	}
}

func TestPullRequestAbsenceAndFailureClearTheBadgeSilently(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	request := &picker.PullRequest{Number: 42, State: picker.PullRequestOpen}
	for _, test := range []struct {
		name string
		msg  pullRequestMsg
	}{
		{"absent", pullRequestMsg{path: "/work/main"}},
		{"failed", pullRequestMsg{path: "/work/main", err: errors.New("gh: authentication required")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(context.Background(), Deps{Projects: []picker.Place{pullRequestPlace("main", request)}}, ProjectsTab)
			model.SetSize(120, 8)
			updated, _ := model.Update(test.msg)
			model = updated.(Model)
			model = updateModel(t, model, keyMsg(tea.KeyRight))
			if plain := shell.Plain(model.View()); strings.Contains(plain, "#42") || strings.Contains(plain, "authentication") {
				t.Fatalf("failure was not silent: %q", plain)
			}
			if model.deps.Projects[0].PullRequest != nil {
				t.Fatalf("stale badge survived: %#v", model.deps.Projects[0].PullRequest)
			}
		})
	}
}

// runUntilGitContext executes a Bubble Tea command tree until the Git
// context result arrives, expanding nested batches the way the runtime does.
func runUntilGitContext(t *testing.T, initial tea.Cmd) picker.GitContextMsg {
	t.Helper()
	messages := make(chan tea.Msg, 8)
	go func(command tea.Cmd) { messages <- command() }(initial)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case message := <-messages:
			switch value := message.(type) {
			case picker.GitContextMsg:
				return value
			case tea.BatchMsg:
				for _, command := range value {
					go func(command tea.Cmd) { messages <- command() }(command)
				}
			}
		case <-deadline:
			t.Fatal("Git context did not load")
			return picker.GitContextMsg{}
		}
	}
}

type fakeGitContextLoader struct {
	calls atomic.Int32
}

func (l *fakeGitContextLoader) Load(context.Context, string) (picker.GitContext, error) {
	l.calls.Add(1)
	return picker.GitContext{Identity: "main"}, nil
}

type blockedPullRequestLoader struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (l *blockedPullRequestLoader) Load(ctx context.Context, _ string) (picker.PullRequest, error) {
	l.calls.Add(1)
	l.started <- struct{}{}
	select {
	case <-l.release:
	case <-ctx.Done():
		return picker.PullRequest{}, ctx.Err()
	}
	return picker.PullRequest{Number: 9, State: picker.PullRequestOpen}, nil
}

func TestPullRequestLoadsAfterGitContextWithoutBlockingInput(t *testing.T) {
	git := &fakeGitContextLoader{}
	requests := &blockedPullRequestLoader{started: make(chan struct{}, 1), release: make(chan struct{})}
	place := picker.Place{Kind: picker.ProjectKind, Path: "/work/one", Group: "/work", DisplayRemainder: "one", Git: true}
	model := NewModel(context.Background(), Deps{Projects: []picker.Place{place}, GitLoader: git, PullRequestLoader: requests}, ProjectsTab)

	initial := model.Init()
	if initial == nil {
		t.Fatal("initial command batch is empty")
	}
	gitMessage := runUntilGitContext(t, initial)
	if got := requests.calls.Load(); got != 0 {
		t.Fatalf("pull request ran before the Git context: %d calls", got)
	}

	updated, command := model.Update(gitMessage)
	model = updated.(Model)
	if command == nil {
		t.Fatal("no pull request command after the Git context")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- command() }()
	select {
	case <-requests.started:
	case <-time.After(2 * time.Second):
		t.Fatal("pull request load did not start")
	}

	// Input must not wait for the slow loader.
	updated, _ = model.Update(keyMsg(tea.KeyDown))
	model = updated.(Model)
	if got := requests.calls.Load(); got != 1 {
		t.Fatalf("pull request calls = %d, want 1", got)
	}
	select {
	case <-done:
		t.Fatal("slow pull request command returned before release")
	default:
	}

	close(requests.release)
	message := <-done
	value, ok := message.(pullRequestMsg)
	if !ok || value.pullRequest.Number != 9 || value.path != place.Path {
		t.Fatalf("pull request message = %#v", message)
	}
	updated, _ = model.Update(value)
	model = updated.(Model)
	model.SetSize(120, 8)
	model = updateModel(t, model, keyMsg(tea.KeyRight))
	if plain := shell.Plain(model.View()); !strings.Contains(plain, "#9") {
		t.Fatalf("accepted pull request did not render: %q", plain)
	}
}

func TestPullRequestLoadIsSkippedWhenGitContextFails(t *testing.T) {
	model := NewModel(context.Background(), Deps{
		Projects:          []picker.Place{pullRequestPlace("main", nil)},
		PullRequestLoader: &blockedPullRequestLoader{started: make(chan struct{}, 1), release: make(chan struct{})},
		GitLoader:         &fakeGitContextLoader{},
	}, ProjectsTab)
	_, command := model.Update(picker.GitContextMsg{Path: "/work/main", Err: errors.New("git failed")})
	if command != nil {
		t.Fatal("pull request command scheduled after a failed Git context")
	}
}

type boundedPullRequestLoader struct {
	blockedPullRequestLoader
}

func (l *boundedPullRequestLoader) PullRequestConcurrency() int { return 1 }

func TestPullRequestSchedulingUsesConfiguredBound(t *testing.T) {
	model := NewModel(context.Background(), Deps{
		Projects:          []picker.Place{pullRequestPlace("main", nil)},
		PullRequestLoader: &boundedPullRequestLoader{blockedPullRequestLoader: blockedPullRequestLoader{started: make(chan struct{}, 1), release: make(chan struct{})}},
	}, ProjectsTab)
	if capacity := cap(model.source.prSlots); capacity != 1 {
		t.Fatalf("pull request slots = %d, want configured bound 1", capacity)
	}
	defaultModel := NewModel(context.Background(), Deps{Projects: []picker.Place{pullRequestPlace("main", nil)}}, ProjectsTab)
	if capacity := cap(defaultModel.source.prSlots); capacity != picker.DefaultPullRequestConcurrency {
		t.Fatalf("default pull request slots = %d, want %d", capacity, picker.DefaultPullRequestConcurrency)
	}
	if fresh := model.source.prFreshness; fresh != picker.DefaultPullRequestFreshness {
		t.Fatalf("default freshness = %v, want %v", fresh, picker.DefaultPullRequestFreshness)
	}
	if interval := model.source.prRefreshInterval; interval != defaultPullRequestRefreshInterval {
		t.Fatalf("default refresh interval = %v, want %v", interval, defaultPullRequestRefreshInterval)
	}
}

// scriptedPullRequestLoader answers each checkout from a mutable table and
// counts calls, so refresh tests can change an answer between loads.
type scriptedPullRequestLoader struct {
	mu     sync.Mutex
	values map[string]picker.PullRequest
	calls  int
}

func (l *scriptedPullRequestLoader) Load(_ context.Context, path string) (picker.PullRequest, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return l.values[path], nil
}

func (l *scriptedPullRequestLoader) set(path string, value picker.PullRequest) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.values == nil {
		l.values = map[string]picker.PullRequest{}
	}
	l.values[path] = value
}

func (l *scriptedPullRequestLoader) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// collectPullRequestMessages executes a Bubble Tea command tree until it has
// seen want pull request results, expanding nested batches.
func collectPullRequestMessages(t *testing.T, command tea.Cmd, want int) []pullRequestMsg {
	t.Helper()
	messages := make(chan tea.Msg, 16)
	launch := func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		go func() { messages <- cmd() }()
	}
	launch(command)
	results := make([]pullRequestMsg, 0, want)
	deadline := time.After(2 * time.Second)
	for len(results) < want {
		select {
		case message := <-messages:
			switch value := message.(type) {
			case pullRequestMsg:
				results = append(results, value)
			case tea.BatchMsg:
				for _, nested := range value {
					launch(nested)
				}
			}
		case <-deadline:
			t.Fatalf("received %d pull request results, want %d", len(results), want)
		}
	}
	return results
}

func assertPullRequestBadge(t *testing.T, model Model, request picker.PullRequest) {
	t.Helper()
	view := model.View()
	if request.Number == 0 {
		if plain := shell.Plain(view); strings.Contains(plain, "#") {
			t.Fatalf("absence rendered a badge: %q", plain)
		}
		return
	}
	want := pullRequestStyle(request.State).Render("#" + itoa(request.Number))
	if !strings.Contains(view, want) {
		t.Fatalf("badge %q missing from view: %q", want, view)
	}
}

func TestPullRequestRefreshRevalidatesStateAndAbsence(t *testing.T) {
	previousProfile, previousDark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		lipgloss.SetHasDarkBackground(previousDark)
	})
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	for _, test := range []struct {
		name   string
		before picker.PullRequest
		after  picker.PullRequest
	}{
		{"state change", picker.PullRequest{Number: 12, State: picker.PullRequestOpen}, picker.PullRequest{Number: 12, State: picker.PullRequestMerged}},
		{"absence to present", picker.PullRequest{}, picker.PullRequest{Number: 34, State: picker.PullRequestOpen}},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
			loader := &scriptedPullRequestLoader{values: map[string]picker.PullRequest{"/work/main": test.before}}
			model := NewModel(context.Background(), Deps{
				Projects: []picker.Place{pullRequestPlace("main", nil)}, PullRequestLoader: loader,
				PullRequestRefreshInterval: time.Millisecond, Now: func() time.Time { return now },
			}, ProjectsTab)
			model.SetSize(120, 8)
			model = updateModel(t, model, keyMsg(tea.KeyRight))
			model = updateModel(t, model, keyMsg(tea.KeyDown))

			_, command := model.Update(picker.GitContextMsg{Path: "/work/main", Context: picker.GitContext{Identity: "main"}})
			model = updateModel(t, model, collectPullRequestMessages(t, command, 1)[0])
			assertPullRequestBadge(t, model, test.before)

			loader.set("/work/main", test.after)
			if _, tick := model.Update(pullRequestRefreshMsg{}); tick == nil {
				t.Fatal("refresh tick did not re-arm")
			}
			if got := loader.count(); got != 1 {
				t.Fatalf("tick inside the freshness window ran %d loads, want 1", got)
			}

			now = now.Add(picker.DefaultPullRequestFreshness)
			_, command = model.Update(pullRequestRefreshMsg{})
			model = updateModel(t, model, collectPullRequestMessages(t, command, 1)[0])
			assertPullRequestBadge(t, model, test.after)
			if got := loader.count(); got != 2 {
				t.Fatalf("loads after freshness = %d, want 2", got)
			}
			stored := model.deps.Projects[0].PullRequest
			if test.after.Number == 0 {
				if stored != nil {
					t.Fatalf("absence was not cleared: %#v", stored)
				}
				return
			}
			if stored == nil || *stored != test.after {
				t.Fatalf("stored pull request = %#v, want %#v", stored, test.after)
			}
		})
	}
}

func TestPullRequestTicksDoNotQueryPerTickPerRow(t *testing.T) {
	branches := []string{"one", "two", "three"}
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	loader := &scriptedPullRequestLoader{values: map[string]picker.PullRequest{}}
	model := NewModel(context.Background(), Deps{
		Projects: pullRequestPlaces(branches...), PullRequestLoader: loader,
		PullRequestRefreshInterval: time.Millisecond, Now: func() time.Time { return now },
	}, ProjectsTab)
	model.SetSize(120, 20)
	model = updateModel(t, model, keyMsg(tea.KeyRight))

	for _, branch := range branches {
		loader.set("/work/"+branch, picker.PullRequest{Number: 5, State: picker.PullRequestOpen})
		_, command := model.Update(picker.GitContextMsg{Path: "/work/" + branch, Context: picker.GitContext{Identity: branch}})
		model = updateModel(t, model, collectPullRequestMessages(t, command, 1)[0])
	}
	if got := loader.count(); got != len(branches) {
		t.Fatalf("initial loads = %d, want %d", got, len(branches))
	}

	// Every tick inside the window checks freshness without asking the loader.
	for i := 0; i < 10; i++ {
		now = now.Add(picker.DefaultPullRequestFreshness / 11)
		if _, command := model.Update(pullRequestRefreshMsg{}); command == nil {
			t.Fatal("refresh tick did not re-arm")
		}
	}
	if got := loader.count(); got != len(branches) {
		t.Fatalf("ticks inside the window ran %d loads, want %d", got, len(branches))
	}

	// Once the window expires each visible row is scheduled exactly once.
	now = now.Add(picker.DefaultPullRequestFreshness)
	_, command := model.Update(pullRequestRefreshMsg{})
	collectPullRequestMessages(t, command, len(branches))
	if got := loader.count(); got != 2*len(branches) {
		t.Fatalf("loads after expiry = %d, want %d", got, 2*len(branches))
	}
}

// gatedPullRequestLoader blocks every load until released or cancelled and
// records how many ran at once.
type gatedPullRequestLoader struct {
	mu      sync.Mutex
	calls   int
	active  int
	peak    int
	started chan struct{}
	release chan struct{}
}

func newGatedPullRequestLoader() *gatedPullRequestLoader {
	return &gatedPullRequestLoader{started: make(chan struct{}, 16), release: make(chan struct{})}
}

func (l *gatedPullRequestLoader) Load(ctx context.Context, _ string) (picker.PullRequest, error) {
	l.mu.Lock()
	l.calls++
	l.active++
	if l.active > l.peak {
		l.peak = l.active
	}
	l.mu.Unlock()
	l.started <- struct{}{}
	select {
	case <-l.release:
	case <-ctx.Done():
		l.done()
		return picker.PullRequest{}, ctx.Err()
	}
	l.done()
	return picker.PullRequest{Number: 9, State: picker.PullRequestOpen}, nil
}

func (l *gatedPullRequestLoader) done() {
	l.mu.Lock()
	l.active--
	l.mu.Unlock()
}

func (l *gatedPullRequestLoader) stats() (calls, peak int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls, l.peak
}

// schedulePullRequests runs one Git context result per checkout and returns
// the pull request commands it was not allowed to start yet.
func schedulePullRequests(t *testing.T, model *Model, branches []string) []tea.Cmd {
	t.Helper()
	commands := make([]tea.Cmd, 0, len(branches))
	for _, branch := range branches {
		_, command := model.Update(picker.GitContextMsg{Path: "/work/" + branch, Context: picker.GitContext{Identity: branch}})
		if command == nil {
			t.Fatalf("no pull request command for %s", branch)
		}
		commands = append(commands, command)
	}
	return commands
}

func TestPullRequestLoadsRespectTheConcurrencyBound(t *testing.T) {
	branches := []string{"one", "two", "three", "four"}
	loader := newGatedPullRequestLoader()
	model := NewModel(context.Background(), Deps{Projects: pullRequestPlaces(branches...), PullRequestLoader: loader}, ProjectsTab)
	model.SetSize(120, 20)
	commands := schedulePullRequests(t, &model, branches)
	results := make(chan tea.Msg, len(commands))
	for _, command := range commands {
		go func(command tea.Cmd) { results <- command() }(command)
	}
	for i := 0; i < picker.DefaultPullRequestConcurrency; i++ {
		select {
		case <-loader.started:
		case <-time.After(2 * time.Second):
			t.Fatal("a pull request load did not start")
		}
	}
	select {
	case <-loader.started:
		t.Fatal("a pull request load started beyond the configured bound")
	case <-time.After(100 * time.Millisecond):
	}
	close(loader.release)
	for range commands {
		select {
		case <-results:
		case <-time.After(2 * time.Second):
			t.Fatal("pull request load did not finish")
		}
	}
	calls, peak := loader.stats()
	if calls != len(commands) || peak != picker.DefaultPullRequestConcurrency {
		t.Fatalf("calls, peak = %d, %d; want %d, %d", calls, peak, len(commands), picker.DefaultPullRequestConcurrency)
	}
}

func TestPullRequestLoadsStopWhenTheTUIExits(t *testing.T) {
	branches := []string{"one", "two", "three", "four"}
	loader := newGatedPullRequestLoader()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := NewModel(ctx, Deps{Projects: pullRequestPlaces(branches...), PullRequestLoader: loader}, ProjectsTab)
	model.SetSize(120, 20)
	commands := schedulePullRequests(t, &model, branches)
	results := make(chan tea.Msg, len(commands))
	for _, command := range commands {
		go func(command tea.Cmd) { results <- command() }(command)
	}
	for i := 0; i < picker.DefaultPullRequestConcurrency; i++ {
		select {
		case <-loader.started:
		case <-time.After(2 * time.Second):
			t.Fatal("a pull request load did not start")
		}
	}
	cancel()
	for range commands {
		select {
		case <-results:
		case <-time.After(2 * time.Second):
			t.Fatal("pull request command did not return after cancellation")
		}
	}
	if calls, _ := loader.stats(); calls != picker.DefaultPullRequestConcurrency {
		t.Fatalf("calls after cancellation = %d, want %d; queued loads must not run", calls, picker.DefaultPullRequestConcurrency)
	}
}

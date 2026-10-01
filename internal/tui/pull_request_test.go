package tui

import (
	"context"
	"errors"
	"strings"
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

func TestProjectsRenderPullRequestBadgeByState(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	for _, test := range []struct {
		name   string
		number int
		state  picker.PullRequestState
		want   lipgloss.Style
	}{
		{"open", 12, picker.PullRequestOpen, pullRequestStyle(picker.PullRequestOpen)},
		{"merged", 34, picker.PullRequestMerged, pullRequestStyle(picker.PullRequestMerged)},
		{"closed", 56, picker.PullRequestClosed, pullRequestStyle(picker.PullRequestClosed)},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := &picker.PullRequest{Number: test.number, State: test.state}
			model := NewModel(context.Background(), Deps{Projects: []picker.Place{pullRequestPlace("main", request)}}, ProjectsTab)
			model.SetSize(120, 8)
			model = updateModel(t, model, keyMsg(tea.KeyRight))
			model.input.cursor = "group:/work"
			view := model.View()
			want := test.want.Render("#" + itoa(test.number))
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
			// Every state is distinct.
			for _, other := range []picker.PullRequestState{picker.PullRequestOpen, picker.PullRequestMerged, picker.PullRequestClosed} {
				if other == test.state {
					continue
				}
				if pullRequestStyle(other).Render("#"+itoa(test.number)) == want {
					t.Fatalf("state %s reuses the colour of %s", test.state, other)
				}
			}
			if plain := shell.Plain(view); !strings.Contains(plain, "⎇ main  #"+itoa(test.number)) {
				t.Fatalf("badge does not follow the branch: %q", plain)
			}
		})
	}
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
}

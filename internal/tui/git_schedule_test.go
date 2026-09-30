package tui

import (
	"context"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/picker"
)

type configuredGitLoader struct{}

func (configuredGitLoader) Load(context.Context, string) (picker.GitContext, error) {
	return picker.GitContext{}, nil
}
func (configuredGitLoader) GitConcurrency() int { return 2 }

func TestGitSchedulingUsesConfiguredBound(t *testing.T) {
	model := NewModel(context.Background(), Deps{GitLoader: configuredGitLoader{}, Projects: []picker.Place{{Kind: picker.ProjectKind, Path: "/one", Git: true}}}, ProjectsTab)
	if cap(model.gitSlots) != 2 {
		t.Fatalf("git slots = %d, want configured bound", cap(model.gitSlots))
	}
}

type boundedGitLoader struct {
	calls   atomic.Int32
	current atomic.Int32
	maximum atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (l *boundedGitLoader) GitConcurrency() int { return 2 }
func (l *boundedGitLoader) Load(ctx context.Context, _ string) (picker.GitContext, error) {
	l.calls.Add(1)
	current := l.current.Add(1)
	for {
		maximum := l.maximum.Load()
		if current <= maximum || l.maximum.CompareAndSwap(maximum, current) {
			break
		}
	}
	l.started <- struct{}{}
	select {
	case <-l.release:
	case <-ctx.Done():
	}
	l.current.Add(-1)
	return picker.GitContext{}, nil
}

func TestGitSchedulingExecutesWithinConfiguredBound(t *testing.T) {
	loader := &boundedGitLoader{started: make(chan struct{}, 8), release: make(chan struct{})}
	projects := make([]picker.Place, 5)
	for i := range projects {
		projects[i] = picker.Place{Kind: picker.ProjectKind, Path: "/repo-" + itoa(i), Git: true}
	}
	model := NewModel(context.Background(), Deps{GitLoader: loader, Projects: projects}, ProjectsTab)
	batch, ok := model.Init()().(tea.BatchMsg)
	if !ok || len(batch) != len(projects) {
		t.Fatalf("initial Git batch = %T (%d commands)", model.Init()(), len(batch))
	}
	done := make(chan struct{})
	for _, command := range batch {
		go func(command tea.Cmd) { command(); done <- struct{}{} }(command)
	}
	for i := 0; i < 2; i++ {
		<-loader.started
	}
	if got := loader.maximum.Load(); got > 2 {
		t.Fatalf("maximum concurrent loads = %d, want <= 2", got)
	}
	close(loader.release)
	for range batch {
		<-done
	}
	if got := loader.maximum.Load(); got != 2 {
		t.Fatalf("maximum concurrent loads = %d, want 2", got)
	}
}

func TestGitSchedulingCancelsQueuedAndRunningLoads(t *testing.T) {
	loader := &boundedGitLoader{started: make(chan struct{}, 2), release: make(chan struct{})}
	model := NewModel(context.Background(), Deps{GitLoader: loader, Projects: []picker.Place{{Kind: picker.ProjectKind, Path: "/one", Git: true}, {Kind: picker.ProjectKind, Path: "/two", Git: true}}}, ProjectsTab)
	batch := model.Init()().(tea.BatchMsg)
	done := make(chan struct{})
	go func() { batch[0](); close(done) }()
	<-loader.started
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updated.(Model)
	select {
	case <-model.ctx.Done():
	default:
		t.Fatal("Git context was not canceled on quit")
	}
	batch[1]()
	if got := loader.calls.Load(); got != 1 {
		t.Fatalf("queued load started after cancellation: %d calls", got)
	}
	close(loader.release)
	<-done
}

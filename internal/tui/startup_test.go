package tui

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
)

type blockingStartupSource struct{ release <-chan struct{} }

func (s blockingStartupSource) ListContainers(context.Context) ([]capabilities.Container, error) {
	<-s.release
	return nil, nil
}

func (s blockingStartupSource) ListAgents(context.Context) ([]capabilities.Agent, error) {
	<-s.release
	return nil, nil
}

func TestFirstFrameDoesNotWaitForSlowSources(t *testing.T) {
	release := make(chan struct{})
	model := NewModel(context.Background(), Deps{
		ProjectSource: func(context.Context) ([]picker.Place, error) {
			<-release
			return nil, nil
		},
		ContainerSource: blockingStartupSource{release: release},
		AgentSource:     blockingStartupSource{release: release},
		RemoteCommands:  nil,
	}, ContainersTab)
	model.SetSize(80, 12)
	started := time.Now()
	_ = model.View()
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("first frame waited %s for background sources", elapsed)
	}
	close(release)
}

type recordingStartupSource struct {
	release <-chan struct{}
	started *atomic.Int32
}

func (s recordingStartupSource) wait(ctx context.Context) {
	s.started.Add(1)
	select {
	case <-s.release:
	case <-ctx.Done():
	}
}

func (s recordingStartupSource) ListContainers(ctx context.Context) ([]capabilities.Container, error) {
	s.wait(ctx)
	return nil, nil
}

func (s recordingStartupSource) ListAgents(ctx context.Context) ([]capabilities.Agent, error) {
	s.wait(ctx)
	return nil, nil
}

type recordingOutput struct {
	mu    sync.Mutex
	data  bytes.Buffer
	first chan struct{}
	once  sync.Once
}

func (w *recordingOutput) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(value) > 0 {
		w.once.Do(func() { close(w.first) })
	}
	return w.data.Write(value)
}

func TestStartupWritesFirstFrameBeforeSourcesRelease(t *testing.T) {
	release := make(chan struct{})
	var started atomic.Int32
	source := recordingStartupSource{release: release, started: &started}
	model := NewModel(context.Background(), Deps{
		ProjectSource: func(context.Context) ([]picker.Place, error) {
			started.Add(1)
			<-release
			return nil, nil
		},
		ContainerSource: source,
		AgentSource:     source,
		RemoteCommands: []tea.Cmd{func() tea.Msg {
			started.Add(1)
			<-release
			return nil
		}},
	}, ContainersTab)
	model.SetSize(80, 12)
	output := &recordingOutput{first: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(output))
	done := make(chan struct{})
	go func() {
		_, _ = program.Run()
		close(done)
	}()
	select {
	case <-output.first:
	case <-time.After(time.Second):
		t.Fatal("first frame was not written before deadline")
	}
	close(release)
	deadline := time.After(time.Second)
	for started.Load() < 4 {
		select {
		case <-deadline:
			t.Fatalf("sources started = %d, want all four", started.Load())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("program did not stop")
	}
}

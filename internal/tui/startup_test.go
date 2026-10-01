package tui

import (
	"context"
	"testing"
	"time"

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

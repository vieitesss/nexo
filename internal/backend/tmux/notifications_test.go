package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/config"
)

type recordingNotifier struct {
	messages []string
	err      error
}

func (n *recordingNotifier) Notify(_ context.Context, message string) error {
	if n.err != nil {
		return n.err
	}
	n.messages = append(n.messages, message)
	return nil
}

func TestSourceNotifiesOncePerWorkingToStoppedTransitionAcrossRestarts(t *testing.T) {
	clock := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	screen := "⠋ working"
	runner := runnerForNotificationTest(&screen)
	stateDir := t.TempDir()
	recording := &recordingNotifier{}

	source := NewAgentSource(runner)
	source.SetStateDir(stateDir)
	source.SetClock(func() time.Time { return clock })
	source.SetNotifier(recording)
	enabled := true
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 3, Notify: &enabled})
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}

	screen = "prompt> "
	clock = clock.Add(3 * time.Second)
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(recording.messages) != 0 {
		t.Fatalf("notifications = %#v, want plain listing not to notify", recording.messages)
	}
	if err := source.deliverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(recording.messages) != 1 {
		t.Fatalf("notifications = %#v, want one stopped transition", recording.messages)
	}

	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := NewAgentSource(runner)
	restarted.SetStateDir(stateDir)
	restarted.SetClock(func() time.Time { return clock.Add(time.Minute) })
	restarted.SetNotifier(recording)
	restarted.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 3, Notify: &enabled})
	if _, err := restarted.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.deliverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(recording.messages) != 1 {
		t.Fatalf("notifications after restart = %#v, want no duplicate", recording.messages)
	}

	screen = "⠋ working"
	clock = clock.Add(time.Minute)
	if _, err := restarted.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	screen = "prompt> "
	clock = clock.Add(3 * time.Second)
	if _, err := restarted.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.deliverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(recording.messages) != 2 {
		t.Fatalf("notifications after second transition = %#v, want two transitions", recording.messages)
	}
}

func TestSourceRetriesPendingNotificationAfterNotifierFailure(t *testing.T) {
	clock := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	screen := "⠋ working"
	notifier := &recordingNotifier{err: errors.New("desktop unavailable")}
	source := NewAgentSource(runnerForNotificationTest(&screen))
	source.SetStateDir(t.TempDir())
	source.SetClock(func() time.Time { return clock })
	source.SetNotifier(notifier)
	enabled := true
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 3, Notify: &enabled})
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	screen = "prompt> "
	clock = clock.Add(3 * time.Second)
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := source.deliverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	notifier.err = nil
	clock = clock.Add(3 * time.Second)
	if err := source.deliverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("messages = %#v, want retained pending notification retried", notifier.messages)
	}
}

func TestSourceDropsPendingNotificationWhenPaneWorksAgain(t *testing.T) {
	clock := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	screen := "⠋ working"
	notifier := &recordingNotifier{}
	source := NewAgentSource(runnerForNotificationTest(&screen))
	source.SetStateDir(t.TempDir())
	source.SetClock(func() time.Time { return clock })
	source.SetNotifier(notifier)
	enabled := true
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 3, Notify: &enabled})
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	screen = "prompt> "
	clock = clock.Add(3 * time.Second)
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	screen = "⠋ working"
	clock = clock.Add(time.Second)
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := source.deliverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.messages) != 0 {
		t.Fatalf("messages = %#v, want pending stop dropped after working transition", notifier.messages)
	}
}

func TestSourceDropsPendingNotificationWhenPaneDisappears(t *testing.T) {
	clock := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	screen := "⠋ working"
	paneList := "%1\t99\tpi\twork\t1\t2\t/tmp\n"
	runner := runnerForNotificationTest(&screen)
	runner.paneList = &paneList
	notifier := &recordingNotifier{}
	source := NewAgentSource(runner)
	source.SetStateDir(t.TempDir())
	source.SetClock(func() time.Time { return clock })
	source.SetNotifier(notifier)
	enabled := true
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 3, Notify: &enabled})
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	screen = "prompt> "
	clock = clock.Add(3 * time.Second)
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	paneList = ""
	clock = clock.Add(time.Second)
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := source.deliverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.messages) != 0 {
		t.Fatalf("messages = %#v, want pending stop dropped after pane disappears", notifier.messages)
	}
}

func TestSourceCoalescesPendingStopsIntoOneTrailingNotification(t *testing.T) {
	clock := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	runner := &multiNotificationSourceRunner{screens: map[string]string{"%1": "⠋ working", "%2": "⠋ working"}}
	notifier := &recordingNotifier{}
	source := NewAgentSource(runner)
	source.SetStateDir(t.TempDir())
	source.SetClock(func() time.Time { return clock })
	source.SetNotifier(notifier)
	enabled := true
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi", "claude"}, IdleSeconds: 3, Notify: &enabled})
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.screens["%1"], runner.screens["%2"] = "prompt> ", "prompt> "
	clock = clock.Add(3 * time.Second)
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := source.deliverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.messages) != 1 || notifier.messages[0] != "pi · work:1.1 and 1 more agents stopped" {
		t.Fatalf("messages = %#v, want one coalesced trailing notification", notifier.messages)
	}
}

type multiNotificationSourceRunner struct {
	screens map[string]string
}

func (r *multiNotificationSourceRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "tmux" {
		joined := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "list-panes"):
			return []byte("%1\t99\tpi\twork\t1\t1\t/tmp\n%2\t100\tclaude\twork\t1\t2\t/tmp\n"), nil
		case strings.HasPrefix(joined, "display-message"):
			return []byte("/tmp/tmux-test\n"), nil
		case strings.HasPrefix(joined, "capture-pane"):
			for paneID, screen := range r.screens {
				if strings.Contains(joined, paneID) {
					return []byte(screen), nil
				}
			}
		}
	}
	return nil, nil
}

func runnerForNotificationTest(screen *string) *notificationSourceRunner {
	return &notificationSourceRunner{screen: screen}
}

type notificationSourceRunner struct {
	screen   *string
	paneList *string
}

func (r *notificationSourceRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "tmux" {
		joined := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "list-panes"):
			if r.paneList != nil {
				return []byte(*r.paneList), nil
			}
			return []byte("%1\t99\tpi\twork\t1\t2\t/tmp\n"), nil
		case strings.HasPrefix(joined, "display-message"):
			return []byte("/tmp/tmux-test\n"), nil
		case strings.HasPrefix(joined, "capture-pane"):
			return []byte(*r.screen), nil
		}
	}
	return nil, nil
}

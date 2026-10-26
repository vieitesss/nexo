package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// clientTimeoutMS bounds one app action. Rex does not cancel a timed-out
// action on the app side, so nexo never retries a focus blindly.
const clientTimeoutMS = 3000

// selectAction is the app action that switches the app to a session. Rex
// session focus is only the session-local typing target; the app needs this
// separate selection.
const selectAction = "session.select"

// client is one connection to this Rex server. Only app clients can select a
// session; the probing CLI appears here too and must never be treated as one.
type client struct {
	ID   string `json:"client_id"`
	Info struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
		Name  string `json:"name"`
	} `json:"info"`
	ConnectedAt     string `json:"connected_at"`
	ConnectionState string `json:"connection_state"`
}

type clientList struct {
	Clients []client `json:"clients"`
}

// displayName is the human name a user recognises for this client.
func (c client) displayName() string {
	if label := strings.TrimSpace(c.Info.Label); label != "" {
		return label
	}
	return strings.TrimSpace(c.Info.Name)
}

// appClients filters client.list down to connected desktop apps.
func appClients(clients []client) []client {
	apps := make([]client, 0, len(clients))
	for _, current := range clients {
		if !strings.EqualFold(strings.TrimSpace(current.Info.Kind), "app") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(current.ConnectionState), "lost") {
			continue
		}
		apps = append(apps, current)
	}
	return apps
}

type performInput struct {
	ClientID  string      `json:"client_id"`
	Action    string      `json:"action"`
	Args      performArgs `json:"args"`
	Origin    string      `json:"origin"`
	TimeoutMS int         `json:"timeout_ms"`
}

type performArgs struct {
	SessionID string `json:"session_id"`
}

// selectSession switches the connected app to one session. It reports whether
// an app was actually selected: with no app the session is still created, but
// the user is told that nothing moved their view.
func (b *Backend) selectSession(ctx context.Context, sessionID string) (bool, error) {
	clientID, err := b.appClient(ctx)
	if err != nil {
		return false, err
	}
	if clientID == "" {
		b.notice(fmt.Sprintf("rex: no Rex app is connected; session %s was not selected", sessionID))
		return false, nil
	}
	_, err = b.api(ctx, "client.perform", performInput{
		ClientID:  clientID,
		Action:    selectAction,
		Args:      performArgs{SessionID: sessionID},
		Origin:    "cli",
		TimeoutMS: clientTimeoutMS,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// appClient chooses the app that should receive the selection. REX_CLIENT wins
// and is diagnosed when it no longer names a connected app; otherwise the most
// recently connected app is used. Never broadcasting and never silently
// substituting another app keeps focus predictable.
func (b *Backend) appClient(ctx context.Context) (string, error) {
	clients, err := b.clients(ctx)
	if err != nil {
		return "", err
	}
	apps := appClients(clients)
	if wanted := strings.TrimSpace(os.Getenv("REX_CLIENT")); wanted != "" {
		return matchClient(apps, wanted)
	}
	if len(apps) == 0 {
		return "", nil
	}
	newest := apps[0]
	for _, current := range apps[1:] {
		if connectedAfter(current, newest) {
			newest = current
		}
	}
	return newest.ID, nil
}

func (b *Backend) clients(ctx context.Context) ([]client, error) {
	output, err := b.api(ctx, "client.list", nil)
	if err != nil {
		return nil, err
	}
	var listed clientList
	if err := json.Unmarshal(output, &listed); err != nil {
		return nil, fmt.Errorf("decode rex client.list: %w", err)
	}
	return listed.Clients, nil
}

// matchClient resolves an explicit client selection by full id or by label.
// An ambiguous label is an error: the user asked for one app.
func matchClient(apps []client, wanted string) (string, error) {
	for _, app := range apps {
		if app.ID == wanted {
			return app.ID, nil
		}
	}
	var matches []client
	for _, app := range apps {
		if strings.EqualFold(app.displayName(), wanted) {
			matches = append(matches, app)
		}
	}
	if len(matches) == 1 {
		return matches[0].ID, nil
	}
	available := make([]string, 0, len(apps))
	for _, app := range apps {
		available = append(available, app.ID+" ("+app.displayName()+")")
	}
	sort.Strings(available)
	if len(matches) > 1 {
		return "", fmt.Errorf("REX_CLIENT %q is ambiguous; connected apps: %s", wanted, strings.Join(available, ", "))
	}
	if len(available) == 0 {
		return "", errors.New("REX_CLIENT is set but no Rex app is connected")
	}
	return "", fmt.Errorf("REX_CLIENT %q does not match a connected Rex app; connected apps: %s", wanted, strings.Join(available, ", "))
}

// connectedAfter reports whether a connected to the server more recently than
// b. Unreadable timestamps keep the server's own order.
func connectedAfter(a, b client) bool {
	left, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(a.ConnectedAt))
	if err != nil {
		return false
	}
	right, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(b.ConnectedAt))
	if err != nil {
		return true
	}
	return left.After(right)
}

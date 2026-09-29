# Agent listing

`nexo agents --json` emits one JSON array for the selected backend. Each item
has this shape:

```json
{
  "pane_id": "%4",
  "target": "work:1.2",
  "container": "work",
  "window": "editor",
  "harness": "pi",
  "state": "stopped",
  "seen": false,
  "stopped_since": "2025-01-01T12:00:03Z",
  "branch": "feature/example"
}
```

`state` is `working` or `stopped`. `seen` is meaningful for stopped agents;
on tmux it is true only after the exact pane has received focus. `stopped_since`
is an RFC3339 timestamp and is omitted while an agent is working. `branch` is
omitted when the pane is not in a named branch. `container` and `window` are optional
backend-neutral display labels; they are omitted when the backend cannot provide them.
`target` remains opaque and is the value passed to the backend when focusing an agent.

The text form prints target, harness, state, seen/unseen, and branch as
tab-separated columns. On Herdr, agent status and seen state come from
Herdr's native agent awareness: `idle` is a seen stopped agent and `done` is
an unseen stopped agent; a focused pane is always seen. Herdr status is read
through `herdr agent list`, never by scraping pane output. Herdr's target
column uses the opaque pane ID because that is the native target accepted by
Herdr. Workspace and tab labels are provided separately in `container` and `window`;
IDs remain available in Herdr's native response. Herdr's
`stopped_since` is persisted under the XDG state directory per Herdr server;
when Herdr provides no timestamp, nexo records the first stopped observation
and resets it on a new `state_change_seq` or working transition. Branches are
read with `git branch --show-current` from Herdr's native foreground or pane
working directory.

On tmux, `nexo agents watch` runs the poller; only one watcher is active for a
server at a time. The watcher enables tmux `focus-events` before installing
its focus hooks; without that server option, tmux does not emit pane focus
transitions. This lets a focus transition mark that exact pane seen.

## Notifications

On tmux, only `nexo agents watch` sends desktop notifications; plain agent
listing and focus hooks never send them. Each working-to-stopped transition is
queued durably under the per-server state lock. Notifications use `notify-send`
on Linux and `osascript` on macOS; when the selected tool is unavailable the
notification is skipped. The agent settings default to `notify = true` and
`sound = false`; setting `[agents] sound = true` plays a short sound with the
notification. A single three-second rate limit is shared by all panes: stops
inside that window are coalesced into one trailing notification (for example,
`claude · work:1.2 and 2 more agents stopped`), so no transition is lost across
rate limiting or watcher restarts. Pending entries are discarded if their pane
works again or disappears before delivery.

Herdr owns this capability natively, so nexo does not send notifications when
Herdr is selected. This was verified read-only with `herdr --default-config`
and `herdr api schema --json`: the default config exposes `[ui.toast]` delivery
(`off`, `herdr`, `terminal`, or `system`) and `[ui.sound] enabled`, while the
API schema exposes `notification.show` with a title, optional body and a sound
of `none`, `done`, or `request`. Herdr's own agent state and notification
settings therefore remain authoritative and nexo sends no duplicate toast.

# Agent listing

`nexo agents --json` emits one JSON array for the selected backend. Each item
has this shape:

```json
{
  "pane_id": "%4",
  "target": "work:1.2",
  "container_id": "$1",
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
omitted when the pane is not in a named branch. `container_id` is the
stable backend-native identity of the owning container and is omitted when the
backend cannot provide it. `container` and `window` are
optional backend-neutral display labels; they are omitted when the backend
cannot provide them. `target` remains opaque and is the value passed to the
backend when focusing an agent.

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
server at a time. The watcher installs its focus hooks without changing tmux
server options. If `focus-events` is off, it shows a message explaining that
agent seen-marking needs `set -g focus-events on`; users may enable that option
themselves when they want focus transitions to mark the exact pane seen.

## Unseen window highlight

The tmux watcher keeps a window highlighted in the window list while it holds
an unseen stopped agent. The highlight is the `[agents] highlight-color`
setting, `red` by default, and it never touches `status-left`, a key binding
or `window-status-format`: nexo sets the `window-status-style` and
`window-status-current-style` window options for that window and records a
private `@nexo-unseen` marker on it.

Seen state is per exact pane, so highlighting a window is not the same as
focusing it. Focusing one of several panes that share a window marks only that
pane seen; the window stays highlighted until the last unseen pane in it
receives focus. Marking a pane in another window never clears this one. When a
window clears, nexo unsets the two style options so the window returns to your
global window list style; a per-window `window-status-style` or
`window-status-current-style` you set yourself on that window is discarded, not
restored. The colour is read once when the watcher starts, so rerun
`nexo tmux init` after changing `highlight-color`; init replaces the existing
watcher. A working agent does not
contribute to the highlight, but another unseen stopped agent in the same
window does. The highlight clears through the same focus hooks as seen-marking,
so it needs `focus-events on` as described above. An orderly watcher exit clears
the highlights it owns; after a crash the marker survives, and the next watcher
start reconciles it. Herdr owns this natively, so nexo never highlights Herdr
windows.

## Glance row

`nexo tmux glance on|off|toggle` shows an always-visible row listing every
agent pane. It is tmux-only and user-bound: nexo installs no key, so bind the
command yourself, for example
`bind -n M-g run-shell 'nexo --backend=tmux tmux glance toggle'`.

The row starts with the label `nexo │` and then one bracketed entry per agent
pane, for example `nexo │ [● zeta:1.0]`. Each entry renders the enabled
`[glance] fields` in the fixed order `dot, target, harness, session, window,
branch, age`; the defaults are `dot` and `target`. The dot is one `●` whose
colour carries the state: `[glance] colors` sets `working` (default `yellow`),
`unseen` stopped (default `red`, like the window highlight) and `seen` stopped
(default `green`). Each colour is a tmux colour or a
theme palette option such as `@thm_yellow`. For terminals without colour,
`[glance] dots = "symbols"` draws an uncoloured `◐` for a working agent, `●`
for an unseen stopped agent and `○` for a seen stopped agent instead. Entries
are ordered unseen stopped, working, then seen stopped. When the row is
narrower than its entries, whole entries are dropped and an ellipsis marks the
omission; the label itself is never truncated. Agent text is escaped so a name
cannot inject a tmux format or command.

The row is one extra status line. `on` scans the whole `status-format` array
and every session's local options, then takes the first row beyond the rows in
use whose slot is neither a custom global row nor a custom session row. A slot
that still holds tmux's built-in default is free; a slot the user set, even one
beyond the displayed rows, is preserved. It then raises `status` to show the
row and keeps `status-left` untouched. Sessions whose own `status` or
`status-format` would shadow the row are adjusted, and their exact originals
are recorded. If no row is free, `on` refuses without changing anything.

`off` restores the original `status` value and the original `status-format`
value, byte for byte, including intentional padding and explicitly empty rows,
and restores every session override it touched. It then removes the private
`@nexo-glance-state` record that stores the originals. `toggle` flips between
the two. The record is one server option, so the state is per tmux server and
a single write publishes it atomically.

The watcher keeps the row current on every poll, so agent state changes appear
without another command, and only writes when the rendered row or an option
actually changed. Sessions created while the row is on are picked up on the
next poll. Rerun `nexo tmux init` for `[glance]` changes to take effect, like the
window highlight; init replaces the existing watcher. A configured `[glance] tint`
paints the row background, either with a tmux colour or with a theme palette option
such as
`@thm_surface_0`; with no tint the row keeps the normal status background.
The command is tmux-only because Herdr renders its own agent sidebar, which
already lists agent panes natively, so nexo adds nothing for it. It needs a
running watcher before `on` to keep updating after the first render.

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

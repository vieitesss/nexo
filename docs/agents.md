# Agent listing

`nexo agents --json` emits one JSON array for the selected backend. Each item
has this shape:

```json
{
  "pane_id": "%4",
  "target": "work:1.2",
  "harness": "pi",
  "state": "stopped",
  "seen": false,
  "stopped_since": "2025-01-01T12:00:03Z",
  "branch": "feature/example"
}
```

`state` is `working` or `stopped`. `seen` is meaningful for stopped agents and
is true only after the exact pane has received focus. `stopped_since` is an
RFC3339 timestamp and is omitted while an agent is working. `branch` is omitted
when the pane is not in a named branch.

The text form prints target, harness, state, seen/unseen, and branch as
tab-separated columns. `nexo agents watch` runs the tmux poller; only one
watcher is active for a server at a time. The watcher enables tmux
`focus-events` before installing its focus hooks; without that server option,
tmux does not emit pane focus transitions. This lets a focus transition mark
that exact pane seen.

# Glance row

The glance row is an extra tmux status line listing agent panes, each with a
state dot (yellow working, red unseen stopped, green seen stopped) and its
target. The user toggles it with a key bound to `nexo tmux glance toggle`;
the watcher keeps it current.

## Sub-features

- `glance-on` / `glance-off` / `glance-toggle` show and hide the row, restoring the previous status settings.
- `glance-dots` dot colour follows the agent state (`[glance] colors`).

## How to get to it (user POV)

- `nexo --backend=tmux tmux glance on|off|toggle`, normally through a key binding.

## Driving it with nexo-verify

Preconditions:

- Baseline run with an agent: `$V agent add api`, `$V agent work api`.

- **Show.** Run `$V cli glance-on --backend=tmux tmux glance on`. Exit `0`. `$V tmux show -gv status` prints `2`.
- **Working dot.** Run `$V tmux show -gv 'status-format[1]'`. Output `nexo │ [#[fg=yellow]●#[fg=default] api:0.0]`.
- **Unseen dot.** Run `$V agent stop api`, wait ~3s, re-read `status-format[1]`. The dot colour is `fg=red`.
- **Seen dot.** Run `$V tmux switch-client -t api`, wait ~2s, re-read `status-format[1]`. The dot colour is `fg=green`.
- **Hide.** Run `$V cli glance-off --backend=tmux tmux glance off`. `$V tmux show -gv status` returns the previous value (`on`) and `status-format[1]` is the tmux default again.
- **Toggle.** Run `$V cli glance-toggle --backend=tmux tmux glance toggle` twice. `status` reads `2`, then `on`.

## Gotchas

- The row is drawn on the attached client's status line, which `capture-pane` never includes; the option values are the proof.
- `[glance]` settings are read when the watcher starts; change them with `$V down && $V up`.
- tmux 3.7+ reuses built-in pane/session rows; on older tmux (3.3a here) nexo writes its own `status-format[1]`.

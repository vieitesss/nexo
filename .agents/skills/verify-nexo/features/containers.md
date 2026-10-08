# Containers tab

The Containers tab lists open tmux sessions on this machine (one row per
container, one state dot per agent) and lets the user switch to, rename,
mark, and kill them without leaving the TUI. `nexo previous` jumps back to the
last other local container.

## Sub-features

- `containers-list` sessions under the machine header with a count; agent dots after the name.
- `containers-switch` Enter switches the client to the selected session.
- `containers-rename` `Ctrl-R` opens `rename:` in the footer; Enter saves, toast `container renamed`.
- `containers-kill` `Ctrl-X` asks `kill selected containers? y confirm  n cancel`; `y` kills, toast `containers killed`.
- `containers-mark` `Ctrl-T` marks several rows so one `Ctrl-X` kills them all.
- `containers-previous` `nexo previous` opens the last non-proxy local container.

## How to get to it (user POV)

- `nexo tui containers`, `F2`, or `nexo tui` with no unseen stopped agent.
- `nexo previous` (usually bound to a key).

## Driving it with nexo-verify

Preconditions:

- Baseline run. Create targets: `$V cli open-alpha --json open "$H/src/alpha" --no-focus` and the same for `gamma` (`H` = sandbox home).

- **List.** Run `$V tui containers`. The machine header shows a count and rows `home`, `src_alpha`, `src_gamma`. Run `$V snap containers-list`.
- **Rename.** Run `$V type src_gamma`, `$V keys C-r`, `$V wait "rename:"`, `$V keys C-u`, `$V type scratch`, `$V keys Enter`, `$V wait "container renamed"`. `$V tmux ls -F '#S'` lists `scratch`, not `src_gamma`.
- **Kill.** In the same TUI, clear the search and target the renamed container: `$V keys C-u`, `$V type scratch`, `$V keys C-x`, `$V wait "kill selected containers?"`, `$V snap kill-confirm`, `$V keys y`, `$V wait "containers killed"`. `$V state containers-killed` has no `scratch` session.
- **Cancel kill.** `$V keys C-x`, then `n`. Nothing is killed.
- **Mark several.** Open two more with `$V nx --json open "$H/src/gamma" --no-focus` and the same for `delta`. In the TUI: `$V keys C-u`, `$V type src_gamma`, `$V keys C-t`, `$V keys C-u`, `$V type src_delta`, `$V keys C-t`, `$V keys C-u`, `$V snap containers-marked`. Both rows sit at the top with `◆`. Then `$V keys C-x`, `$V wait "kill selected"`, `$V keys y`, `$V wait "containers killed"`. `$V tmux ls -F '#S'` lists neither.
- **Switch.** Run `$V keys C-u`, `$V type src_alpha`, `$V keys Enter`, `$V state containers-switch`. Client `-> src_alpha:…`.
- **Previous.** Run `$V cli previous previous`, then `$V state containers-previous`. The client is back on the session it left (`home`).

## Gotchas

- `$V tui` opens its own window in the client's session; that window is a pane of the session and shows up in `state`.
- Killing the session the client is on moves the client; kill a session it is not on.

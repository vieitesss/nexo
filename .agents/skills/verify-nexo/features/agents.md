# Agent state and the Agents tab

nexo detects panes running an allowlisted coding agent, tracks them as
working or stopped from the on-screen working indicator, and keeps a stopped
agent unseen — red dot, red window-list highlight — until its exact pane is
focused. The Agents tab lists agents unseen-first and Enter jumps to one.

## Sub-features

- `agents-detect` a pane whose foreground process is allowlisted becomes an agent.
- `agents-state` working while the indicator shows; stopped after `idle-seconds` without it.
- `agents-highlight` the watcher sets `@nexo-unseen=1` and `fg=red` on the window of an unseen stopped agent.
- `agents-tab` the TUI opens on Agents when an unseen stopped agent exists; rows lead with the container name.
- `agents-focus` Enter focuses the exact pane; the focus hook marks it seen and clears the highlight.
- `agents-cli` `nexo agents [--json]`, `nexo agents seen PANE_ID`.

## How to get to it (user POV)

- `nexo tui agents` (or `nexo tui` with an unseen stopped agent), `F1`.
- `nexo agents --json`.
- Focusing the pane any other way (tmux navigation) also marks it seen.

## Driving it with nexo-verify

Preconditions:

- Baseline run; the watcher check in `$V doctor` is `ok`.

- **Start an agent.** Run `$V agent add api` then `$V agent work api`. After ~2s `$V cli agents-working --json agents` shows one item, `container` `api`, `harness` `fakeagent`, `state` `working`.
- **Stop it.** Run `$V agent stop api`, wait ~3s, run `$V cli agents-stopped --json agents`. `state` `stopped`, `seen` `false`, `stopped_since` set.
- **Highlight.** Run `$V state agents-unseen`. Line `api:0 @nexo-unseen=1 style=fg=red`.
- **Agents tab.** Run `$V tui`, then `$V wait stopped`. The tab bar reads `Agents 1`; under the machine header (`<hostname>  1`) the row `api  fakeagent` has a second line `● stopped Ns` with target `api:0.0` at the right. Run `$V snap agents-tab`; the `.ansi` has the dot in red (`ESC[31m●`).
- **Focus.** Run `$V keys Enter`, `$V wait "exited 0"`, wait ~2s, `$V state agents-focused`. The client reads `-> api:0.0 (%N)` and `api:0` now has an empty `@nexo-unseen` and `style=default`.
- **Seen.** Run `$V cli agents-seen --json agents`. `seen` is `true`.
- **CLI seen.** Move the client off the agent with `$V tmux switch-client -t home`, then recycle: `$V agent work api`, wait ~2s, `$V agent stop api`, wait ~3s, confirm `seen` `false`, then `$V cli seen-cli agents seen %N` with the pane id from `--json agents`; a following `--json agents` shows `seen` `true`.

## Gotchas

- A seen-mark needs the focus to land on the pane: the client must be attached (doctor checks it) and `focus-events` on (the sandbox config sets it).
- A stopped agent focused at the moment it stops is marked seen immediately; keep the client elsewhere (the `home` session) while stopping it.
- Focus clears the window style at once, but `@nexo-unseen` clears on the watcher's next tick; a `state` taken right after focus still shows `@nexo-unseen=1 style=default`.
- Opening the TUI within a second or two of a state change can show the previous counts on the first frame; wait out `idle-seconds + poll-interval`.
- The real agent allowlist is not exercised; `fakeagent` stands in via the sandbox config's `processes` and `working-pattern`.

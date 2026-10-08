---
name: verify-nexo
description: Verify nexo's tmux TUI and CLI by driving a freshly built binary in a private tmux server with a sandboxed HOME. Use to prove a nexo change works for a user — TUI tabs, project open, agent state and seen-marking, container rename/kill, project forms, glance row, --json commands — beyond unit tests.
---

# Verify nexo

nexo is a Go CLI plus a Bubble Tea TUI that drives the multiplexer owning the
current pane. A user touches it two ways: `nexo tui` (usually from a tmux
`display-popup` binding) and `nexo <command> [--json]`. Verification drives
both against **tmux** — the primary backend. Herdr and remote machines are
secondary surfaces with no harness here (see `features/README.md`).

Everything goes through one helper, run from the checkout under test:

```sh
V=.agents/skills/verify-nexo/scripts/nexo-verify
$V help
```

Isolation is the point of the helper. Each run gets its own tmux server
(`tmux -L nexo-verify-<run>`), its own `HOME`/`XDG_*` tree with a generated
config and fixture repos, its own agent watcher, and a real attached client in
a private pty. The user's tmux server, `~/.config/nexo`, and
`~/.local/state/nexo` are never read or written, so a run is safe while the
user works in their own tmux, and two runs with different names coexist.

## Launch

```sh
$V up [RUN]        # RUN defaults to a timestamp; it becomes the "current" run
```

`up` seeds the sandbox, runs `go build ./cmd/nexo` from the checkout into it,
starts the private server with session `home`, attaches a 160x45 client, and
runs `nexo --backend=tmux tmux init` (focus hooks + watcher), as the README's
init line does. Ready when it prints `run:`, `socket:`, `sandbox:`,
`evidence:` and exits 0.

Runs live under `$NEXO_VERIFY_ROOT` (default `${TMPDIR:-/tmp}/nexo-verify`);
set it to your scratchpad when you have one. Other commands act on the current
run; set `NEXO_VERIFY_RUN=<run>` to address another.

Fixture state after `up` (sandbox `~` is `$NEXO_VERIFY_ROOT/<run>/sandbox/home`):

- `~/src/alpha` — Git, branch `main`, clean.
- `~/src/beta` — Git, branch `feature/login`, one modified file (`+1 *`).
- `~/src/gamma` — plain directory.
- `~/src/delta` — Git, clean, on `feature/api` one commit ahead of `main`.
- Config: `backend = "tmux"`, `paths = ["~/src"]`, agent process `fakeagent`
  with working pattern `⠋ WORKING`, `idle-seconds = 1`, `poll-interval = 1`,
  `notify = false` (no desktop notifications), `theme = "dark"`.

Code changed after `up`? `$V rebuild`, then `$V down && $V up` so the watcher
and hooks run the new binary too.

## Doctor

```sh
$V doctor
```

Read-only. Exits 1 on any `FAIL`: sandbox present, private server up, client
attached, server `HOME` is the sandbox, nexo selects tmux, watcher running from
this sandbox. `WARN checkout changed since build` means the binary is stale.
Run it first, and again whenever a drive behaves oddly.

## Drive

- **CLI:** `$V cli NAME ARGS...` runs the sandbox nexo as if typed in the
  attached client's active pane (`TMUX`, `TMUX_PANE`, `HOME` all point at the
  run) and records command, exit code, stdout and stderr as evidence. `$V nx
  ARGS...` is the same without recording.
- **TUI:** `$V tui [agents|containers|projects]` runs `nexo tui` in a new
  window of the client's current session and waits for the tab bar
  (`Agents N   Containers N   Projects N`). Then:
  - `$V keys KEY...` — tmux key names: `Enter`, `Escape`, `Tab`, `BTab`,
    `F1`–`F3`, `Up`/`Down`, `Left`/`Right`, `C-o`, `C-d`, `C-e`, `C-y`, `C-x`,
    `C-r`, `C-t`, `C-u`.
  - `$V type TEXT` — literal text into the search row or a form field.
  - `$V wait TEXT [SECONDS]` — poll the screen for TEXT; use instead of sleeps.
  - `$V screen` — print the screen.
  When the TUI acts (open, focus) it switches the client and exits; its pane
  then shows `[nexo tui exited N]`. The next `$V tui` replaces that window.
- **Agents:** `$V agent add NAME` starts container `NAME` (cwd `~/work/NAME`)
  running `fakeagent`, a renamed bash that tmux reports as
  `pane_current_command=fakeagent`. `$V agent work NAME` paints `⠋ WORKING`;
  `$V agent stop NAME` clears it. Allow `idle-seconds + poll-interval` (~2-3s)
  for the watcher to record the transition.
- **Raw tmux:** `$V tmux ARGS...` targets the private server (inspect options
  such as `status-format[1]`, list panes).

The screen handles to wait on are the tab bar, the `⌕` search row with
`N match(es)`, group headers (`▸`/`▾  ~/src  N`, machine name with count), the
footer prompts (`rename: … Enter save  Esc cancel`, `kill selected
containers? y confirm  n cancel`), form titles inside `╭…╮` boxes
(`› directory`), and toasts (`container renamed`, `containers killed`).

## Evidence

Evidence lands in `$NEXO_VERIFY_ROOT/<run>/evidence/`, numbered in capture
order, and survives `down`:

- `$V snap NAME [PANE]` → `NNN-NAME.screen.txt` plus `.ansi` (colours: state
  dots, selection bar, highlight).
- `$V state NAME` → `NNN-NAME.state.txt`: which session/pane the client is
  focused on, sessions with paths, panes with their commands, and each
  window's `@nexo-unseen` marker and `window-status-style`.
- `$V cli NAME ARGS...` → `NNN-NAME.cli.txt`.

Proof standards:

- Drive the user path: keys into `nexo tui`, commands through `$V cli`. Write
  state through nexo itself, never by editing the ledger under
  `~/.local/state/nexo` or setting tmux options by hand.
- Capture the action and the result: a `snap` before the decisive key and a
  `snap`/`state` after it.
- Verify side effects with a second view: `state` for focus, session
  creation, rename, kill and window highlight; `cli … --json agents` for seen
  state; `ls` of the sandbox for directories, clones, worktrees and deletions.
- `nexo delete --dry-run` performs no mutation by design; prove that by
  comparing `ls` before and after, not by trusting the flag.
- The glance row and window highlight live in the client's status line, which
  `capture-pane` cannot see: prove them with `$V tmux show -gv
  'status-format[1]'` and `state` (window styles).

## Cleanup

```sh
$V down
```

Detaches the client, kills the private server, kills any leftover process
whose command line contains this run's sandbox path (watcher, hook jobs,
fake agents), and deletes the sandbox and socket. It prints and keeps the
evidence directory. It touches nothing it did not start. Run it after every
failed attempt too; `up` refuses to reuse a run name whose sandbox still
exists.

## Helpers

`scripts/nexo-verify` is the only helper; `$V help` prints every subcommand.
Its environment knobs:

- `NEXO_VERIFY_ROOT` — parent of all runs (default `${TMPDIR:-/tmp}/nexo-verify`).
- `NEXO_VERIFY_RUN` — run to act on (default: the one recorded by the last `up`).
- `NEXO_VERIFY_REPO` — checkout to build (default: the git toplevel of the cwd).

`PANE` arguments to `wait`, `screen` and `snap` default to the last `$V tui`
pane; pass a tmux pane id (`%3`) to read an agent or project pane instead.

## Feature map

`features/README.md` indexes one recipe per user-facing feature. Read the
matching file before driving a feature; a proof that drives only one entry
point of a feature with several is incomplete.

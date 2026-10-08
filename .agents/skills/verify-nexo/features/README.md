# nexo verification map

The maintained source for verifying nexo's user-facing behaviour on the tmux
backend. Read this index, then drive the matching feature file as the recipe.
`V` below is `.agents/skills/verify-nexo/scripts/nexo-verify`.

## Baseline preconditions

- A run started with `$V up` from the checkout under test, and `$V doctor`
  exiting 0 with `binary matches the checkout`.
- The seeded fixtures from `SKILL.md` (`~/src/alpha`, `beta`, `gamma`,
  `delta`) unchanged, unless a recipe's preconditions say otherwise. A recipe
  that mutates fixtures is the last one in its run, or the run is recycled
  with `$V down && $V up`.
- Only drive a server started by this run (`tmux -L nexo-verify-<run>`).

## Driving conventions

- TUI input goes through `$V keys` / `$V type`; CLI calls through `$V cli`.
- Wait for a screen handle with `$V wait TEXT` instead of sleeping.
- `~` in recipes is the sandbox home; nexo also displays it as `~`. Pass
  absolute paths (`$NEXO_VERIFY_ROOT/<run>/sandbox/home/...`) to CLI commands.
- A TUI that opens or focuses something exits; start a new one with `$V tui`.

## Proof and skip reporting

- TUI proof: a `snap` before the decisive key and a `snap` plus `state` after.
- CLI proof: the `cli` record (command, exit, stdout, stderr).
- Mutation proof: a second, read-only view — `state`, `--json` listing, or
  `ls` of the sandbox.
- Name the feature ID and entry point with every artifact you report.
- Report an unreachable entry point with the command tried and the unmet
  precondition; never count it verified through another entry point.

## Feature entry contract

Each feature file has an H1 title, one paragraph of user-visible behaviour,
then exactly four H2s in order: `Sub-features`, `How to get to it (user
POV)`, `Driving it with nexo-verify` (starting with `Preconditions:`, then
labelled bullets pairing a user action, the exact command, and the observable
result), and `Gotchas`. Keep implementation details out.

## Features

- [Projects tab and open](./projects.md) — fold, search, Git context, open a project container from the TUI and CLI.
- [Agent state and the Agents tab](./agents.md) — working/stopped detection, unseen highlight, focusing an agent marks it seen.
- [Containers tab](./containers.md) — switch, rename, mark and kill containers; `nexo previous`.
- [Project actions](./project-actions.md) — clone, new directory, branch extraction, and safe delete, from forms and CLI.
- [Glance row](./glance.md) — the tmux status row listing agent panes.

## Not mapped (no harness here)

- Remote machines (`nexo remote …`, remote rows, `+ new container…`): need a
  real SSH alias running nexo; the sandbox has none.
- Herdr backend: needs a Herdr server and the plugin; this harness is tmux-only.
- Pull request badges: need `gh` auth and a github.com upstream; the sandbox
  `HOME` has neither, so badges stay absent by design.
- Mouse input and live theme reload.

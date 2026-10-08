# Project actions

From the Projects tab a user can clone a repository (`Ctrl-O`), create a
directory (`Ctrl-D`), extract the current branch into a worktree (`Ctrl-E`),
and delete a candidate after a preflight (`Ctrl-Y`). Each runs in a staged,
bordered form; the CLI offers `nexo clone`, `nexo worktree extract|create`,
and `nexo delete`.

## Sub-features

- `actions-clone` form `› source` (protocol, repository) then `○ destination`; CLI `nexo clone SOURCE DEST`.
- `actions-dir` form `› directory` with `path:` and `add parent (yes/no)`; creates missing parents, never opens a container.
- `actions-extract` form `› destination` (sibling path suggestion) and `○ return branch`; moves the branch into a new worktree and opens it.
- `actions-delete` form `delete project — path:` with `› preflight` warnings and `○ confirm deletion`; trash by default; CLI `nexo delete [--dry-run] [--permanent] [--force] PATH`.

## How to get to it (user POV)

- Projects tab (`F3`), select a row, press `Ctrl-O` / `Ctrl-D` / `Ctrl-E` / `Ctrl-Y`.
- `nexo clone`, `nexo worktree extract`, `nexo worktree create`, `nexo delete`.

## Driving it with nexo-verify

Preconditions:

- Baseline fixtures. `H` is the sandbox home. Run this recipe last in a run; it mutates `~/src`.

- **New directory.** Run `$V tui projects`, `$V keys C-d`, `$V wait "› directory"`, `$V type '~/src/epsilon'`, `$V snap dir-form`, `$V keys Enter`. The TUI stays open with `▾  ~/src  5` and an `epsilon` row; `ls "$H/src"` lists `epsilon`; `$V tmux ls` shows no `src_epsilon`.
- **Clone (CLI).** Run `$V cli clone --json clone "$H/src/alpha" "$H/src/alpha-clone"`. Exit `0`, JSON `source` and `destination`; `git -C "$H/src/alpha-clone" log --oneline` shows `alpha: init`.
- **Clone (form).** Make a source whose name is free under `~/src`: `git clone -q --bare "$H/src/alpha" "$H/remote/widget.git"` (after `mkdir -p "$H/remote"`). Run `$V tui projects`, `$V keys C-o` (any Projects row, including the folded group header), `$V wait "Clone a repository  step 1 of 2"`. The repository field is focused: `$V type '~/remote/widget.git'`, `$V keys Enter`. The header reads `step 2 of 2`, `✓ Repository   ~/remote/widget.git`, and `path:` is pre-filled with `~/src/widget` (the selected folder, or the selected project's folder, plus the repository name). Run `$V snap clone-form`, `$V keys Enter`. The TUI stays open with a `widget` row selected; `git -C "$H/src/widget" remote get-url origin` prints `$H/remote/widget.git`. To clone elsewhere, clear the pre-filled path with `$V keys C-u` before typing.
- **Extract (form).** Run `$V tui projects`, `$V type delta`, `$V wait "1 match"`, `$V keys C-e`, `$V wait destination`. `path:` suggests `$H/src/delta-feature-api`. Run `$V snap extract-form`, `$V keys Enter` (stage `› return branch`, `branch: main`), `$V keys Enter`, wait ~3s. `git -C "$H/src/delta" branch --show-current` prints `main`; `git -C "$H/src/delta" worktree list` shows `delta-feature-api [feature/api]`; `$V state extract` shows the client on `src_delta-feature-api`.
- **Extract refusal.** On `beta` (dirty), `$V cli extract-dirty worktree extract --no-focus --return-branch main "$H/src/beta" "$H/src/beta-login"` exits `1` with `has uncommitted changes`; nothing new under `$H/src`.
- **Delete preflight.** Run `ls "$H/src"`, `$V cli delete-dry --json delete --dry-run "$H/src/gamma"`, `ls "$H/src"`. JSON has `action` `trash`, `can_proceed` `true`; both listings match.
- **Delete refusal.** Run `$V cli delete-noforce --json delete "$H/src/alpha"`. Exit `1`, stderr `delete requires --force: … is a non-empty directory; local-only Git data: unpushed commits`; `alpha` still exists.
- **Delete (form).** Run `$V tui projects`, `$V type gamma`, `$V wait "1 match"`, `$V keys C-y`, `$V wait "› preflight"`. `gamma` shows `no warnings or blockers`. Run `$V snap delete-preflight`, `$V keys Enter`, `$V wait "confirm deletion"`. The stage has `mode (trash/permanent): trash`, `accept warnings (yes/no): no`, `type yes to delete: no`. Fields append typed text, so clear before typing: `$V keys Down Down BSpace BSpace`, `$V type yes`, `$V snap delete-confirm`, `$V keys Enter`. `ls "$H/src"` no longer lists `gamma`; `ls "$H/.local/share/Trash/files"` lists it. On a candidate with warnings, also set `accept warnings` to `yes` (one `Down`, clear, type) before the confirm field.

## Gotchas

- Trash on Linux runs `gio trash`, falling back to `trash-put`; with neither, delete fails rather than removing permanently. Under the sandboxed `HOME` the item lands in `$H/.local/share/Trash`, never the user's trash.
- `Ctrl-O` and `Ctrl-D` work from any Projects row; `Ctrl-E` acts on a Git project row, and with a folded group header selected it does nothing and later typing lands in the search row (where `Enter` would open the match).
- Fixture repos have no upstream, so their preflight warns about local-only Git data; the form then requires `accept warnings: yes` and the CLI `--force`.
- The preflight stage refuses Enter until its whole text has been displayed; on a short terminal scroll it first.
- Clone sources that are not local paths go through the `[clone]` ssh/https prefix and hit the network; use an absolute local path offline, in the form and the CLI.
- The `--add-parent` / `add parent: yes` paths rewrite the sandbox config; check `$CFG` afterwards, never the user's.

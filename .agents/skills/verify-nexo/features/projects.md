# Projects tab and open

The Projects tab lists project candidates grouped by folder with their Git
context, lets the user fold, search, and open one; opening creates or reuses
the project's tmux session and switches the client to it. `nexo list` and
`nexo open` expose the same catalog from the CLI.

## Sub-features

- `projects-fold` folders start folded on an empty query; Right unfolds, Left folds.
- `projects-git` rows show branch, `+adds −dels *` dirty marker.
- `projects-search` fuzzy search shows `~/src/<name>` paths and a match count.
- `projects-open` Enter creates or reuses the session and focuses it.
- `projects-cli` `nexo list --json` and `nexo open PATH [--no-focus]`.

## How to get to it (user POV)

- `nexo tui projects`, or `F3` / `Tab` from another tab.
- `nexo --json list`, `nexo list --places`.
- `nexo open PATH`, `nexo --json open PATH --no-focus`.

## Driving it with nexo-verify

Preconditions:

- Baseline fixtures; no session named `src_alpha` (`$V tmux ls`).

- **Open the tab.** Run `$V tui projects`. The screen shows `▸  ~/src  4` folded.
- **Unfold.** Run `$V keys Right` then `$V wait alpha`. Rows `alpha  ⎇ main`, `beta   ⎇ feature/login  +1 *`, `delta  ⎇ feature/api`, `gamma` (no branch) appear under `▾  ~/src  4`.
- **Fold.** Run `$V keys Left`. The header returns to `▸`.
- **Search.** Run `$V type alp` then `$V wait "1 match"`. The only row is `~/src/alpha  ⎇ main`. Run `$V snap projects-search`.
- **Open.** Run `$V keys Enter`, then `$V wait "exited 0"`, then `$V state projects-open`. The client line reads `-> src_alpha:0.0` and session `src_alpha` has `path=…/home/src/alpha`.
- **Reuse.** Run `$V tui projects`, `$V type alp`, `$V keys Enter`, `$V state projects-reuse`. Still exactly one `src_alpha` session.
- **CLI list.** Run `$V cli list --json list`. Exit `0`; four objects; `git` is `false` only for `gamma`.
- **CLI open without focus.** Run `$V cli open-nofocus --json open "$H/src/gamma" --no-focus` (`H` = sandbox home). Exit `0`; `$V state open-nofocus` shows a `src_gamma` session while the client stays where it was.

## Gotchas

- Session names are tmux-canonical: `~/src/alpha` opens as `src_alpha`.
- Search does not change saved fold state; clear with `C-u` before testing folds.
- Branch-only Git context loads after the first frame; wait for `⎇` before snapping.
- `$V type` before the TUI renders drops keys; `$V tui` already waits for the tab bar.

# Git worktrees

`nexo worktree` creates Git worktrees on the shared lifecycle seam. Native
worktree creation, opening, focus, and rollback stay in the selected backend
adapter; destination preflight, result shape, and cleanup sequencing are
shared. Both subcommands run on the one backend selected for the invocation.

## Create

```console
nexo worktree create [--new-branch] [--add-parent] [--no-focus] REPO BRANCH DESTINATION
```

`create` asks Git to add a worktree for `BRANCH` at `DESTINATION`, then opens
its container. `--new-branch` creates the branch from the current `HEAD`;
without it, the branch must already exist. `--add-parent` adds the
destination's parent directory to configured project paths. `--no-focus`
creates and opens the container without moving the user's view to it.

The destination must not exist. The command fails before touching Git when it
does, and a failed container open rolls the worktree back.

## Extract

```console
nexo worktree extract [--return-branch BRANCH] [--add-parent] [--no-focus] REPO DESTINATION
```

`extract` moves the branch currently checked out in `REPO` into a new worktree
at `DESTINATION`, leaving a return branch checked out in `REPO`. This is the
inverse of `create`: the branch keeps its commits and its identity while its
working tree moves, and the original checkout is freed for another branch.

The whole preflight runs before anything is mutated:

- `REPO` is clean, with no staged, unstaged, or untracked changes.
- `DESTINATION` does not exist.
- The branch being extracted is not checked out in another worktree.
- A return branch is eligible. The explicit `--return-branch` wins when given;
  otherwise nexo prefers the previous named branch, then the repository
  default branch. An occupied candidate is skipped unless it was requested
  explicitly.

The return branch must exist locally and must not be checked out in another
worktree. When no eligible return branch can be found, pass `--return-branch`
to choose one.

After preflight, nexo switches `REPO` to the return branch, creates the
worktree for the extracted branch, and opens it with the same focus and
`--no-focus` policy as `create`. If creation or opening fails, the worktree is
removed and `REPO` is switched back to the extracted branch.

## JSON output

Both subcommands accept the global `--json` flag. The output object has the
same shape as `nexo open --json` for the opened container, plus `path` and
`branch`. `extract` also reports the `return_branch` it left behind:

```json
{
  "path": "/home/me/worktrees/example",
  "branch": "feature/example",
  "return_branch": "main",
  "container": {
    "backend": "tmux",
    "id": "$12",
    "name": "worktrees_example",
    "path": "/home/me/worktrees/example",
    "created": true
  }
}
```

# Usage and commands

Running `nexo` with no command prints usage. `nexo --help` lists every
command and global flag. Use `nexo <command> --help`, `nexo <command> -h`, or
`nexo help <command> [subcommand]` for that command's subcommands, flags, and
examples; nested paths such as `nexo remote paste --help` work the same way.
The TUI entry point accepts an optional starting tab. Global flags can appear
before or after a command, so `nexo --backend=herdr tui projects` and
`nexo tui projects --backend=herdr` are equivalent.

## Commands

| Command | Purpose |
| --- | --- |
| `nexo backend` | Print the selected backend and its capabilities; add `--json` for structured output. |
| `nexo list` | List configured project candidates; use `--places` to include local and configured remote containers, each local container with a screen preview. |
| `nexo tui [agents\|containers\|projects]` | Render the TUI in the terminal supplied by your binding. With no tab, open Agents for unseen-stopped agents, otherwise Containers. |
| `nexo agents` | List agent panes; add `watch`. |
| `nexo previous` | Open the last non-proxy container on this machine other than the current one; it skips proxies, stale containers, and the current container, and is a no-op when nothing else is live. |
| `nexo open PATH` | Open a discovered project candidate; add `--no-focus` to create or reuse its container without switching to it. Herdr also accepts `--workspace-id ID`. |
| `nexo clone [--add-parent] SOURCE [DESTINATION]` | Clone a repository with Git; the optional flag adds the destination parent to project paths. Add `--json` for structured output. |
| `nexo delete [--dry-run] [--permanent] [--force] PATH` | Preflight and delete one project candidate. Trash is the default; `--permanent` is explicit, `--force` accepts warnings, and `--dry-run` performs no mutation. Add `--json` for structured preflight output. |
| `nexo remote` | Run `list`, `open MACHINE/CONTAINER`, `new MACHINE [NAME]`, `close [MACHINE/CONTAINER]`, `previous`, `paste [KEY...]`, or `passthrough KEY...`. |
| `nexo focus` | Record the container named by a backend focus event; it is the command declared by the Herdr plugin's `[[events]]` hook. |
| `nexo worktree create REPO BRANCH DESTINATION` | Create and open a Git worktree; add `--new-branch`, `--add-parent`, or `--no-focus`. |
| `nexo worktree extract REPO DESTINATION` | Move the branch checked out in `REPO` into a new worktree and leave a return branch behind; add `--return-branch`, `--add-parent`, or `--no-focus`. |
| `nexo tmux init` | Install tmux focus hooks and start the agent watcher (tmux backend only). |
| `nexo tmux glance on\|off\|toggle` | Show, hide or toggle the glance row that lists agent panes (tmux backend only). Bind it yourself; nexo installs no key. |
| `nexo upgrade` / `nexo update` | Atomically install a release or `--nightly` on this machine and configured remotes; `--machine`, `--local-only`, and `--dry-run` limit the operation. |

Help lists `focus`, `agents watch`, and the tmux integration commands because
users invoke them from backend hooks or bindings and may need to inspect them.
The internal `remote attach` proxy entry point remains hidden from the
subcommand table, but `nexo remote attach --help` still documents it.

## Examples

```console
nexo
nexo tui
nexo tui agents
nexo --backend=herdr -c ~/.config/nexo/config.toml tui projects
nexo --backend=tmux --json backend
nexo --version
nexo --help

# Discover projects as a script-friendly JSON array.
nexo --backend=tmux --json list

# Include open local and remote containers.
nexo --backend=tmux list --places --json

# Inspect agents, or open the full-screen navigator.
nexo --backend=herdr agents --json
nexo --backend=tmux tui agents
nexo --backend=tmux agents watch

# Upgrade every configured machine, or inspect a nightly without changing files.
nexo upgrade --dry-run
nexo upgrade --nightly --machine devbox

# Open a project already found by `list`.
nexo --backend=tmux open ~/src/api

# Create or reuse the container without moving the user's view.
nexo --backend=tmux --json open ~/src/api --no-focus

# Clone a repository without opening its container.
nexo --json clone git@github.com:owner/repo.git ~/src/repo

# Inspect a candidate without changing it, then explicitly trash it.
nexo --json delete --dry-run ~/src/repo
nexo delete --force ~/src/repo

# List and open configured remote containers.
nexo --backend=tmux remote list --json
nexo --backend=tmux remote open devbox/api
nexo --backend=herdr remote new devbox feature-api
nexo --backend=tmux remote close devbox/api
nexo --backend=tmux previous
nexo --backend=tmux remote previous

# Create a worktree and add its parent to project discovery.
nexo --backend=tmux worktree create --new-branch --add-parent \
  ~/src/api feature/api ~/worktrees/api

# Move a checkout's current branch into its own worktree.
nexo --backend=tmux worktree extract --return-branch main \
  ~/src/api ~/worktrees/api
```

## Key bindings

nexo installs no key bindings. The local setup in the README has copy-paste
tmux bindings for the TUI, the glance toggle, `previous`, `remote previous`,
`remote paste`, and root-key passthrough. The complete binding reference and
the paste and passthrough details are in [remote.md](remote.md). The TUI's picker
keys are in [tui.md](tui.md).

## Deletion safety

Deletion always preflights the exact selected lexical path with `Lstat`:
symlinks are removed as links, linked worktrees use non-force Git removal, and
blockers (including active containers, path changes, dependent worktrees,
remote failures, and Git refusals) cannot be overridden. Non-empty directories
and local-only Git data require `--force`; trash failures never fall back to
permanent deletion.

## JSON shapes and recipes

See [list-json.md](list-json.md) for JSON shapes and [worktree.md](worktree.md)
for worktree creation and extraction. Automation recipes, exit statuses, and
safe verification are in [skills/nexo/SKILL.md](../skills/nexo/SKILL.md).

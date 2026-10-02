---
name: nexo
description: Use when an agent needs nexo's structured CLI for backend selection, project/place discovery, agent panes, remote containers, or Git worktree creation; reach for this when scripting or parsing any nexo --json output.
---

# nexo structured CLI

Use nexo's JSON mode when another program or agent needs a stable result instead
of terminal-oriented text. The [Global flags and errors](#global-flags-and-errors)
section defines backend selection, `--json`, config resolution, and exit
statuses.

## Recipe

1. Pick the operation below and put global flags before the command. Use
   `--backend=tmux` or `--backend=herdr` when the invocation must be
   deterministic; use `--backend=auto` only when detecting the inner
   multiplexer is the point of the call.
2. Add `--json` and parse one complete JSON value from stdout. Keep stderr
   separate: discovery warnings and diagnostics never belong to the JSON
   value.
3. Treat absent optional fields as absent. Do not infer a field from a
   display label or from a field belonging to another JSON command.
4. Check the process exit status before consuming a result. Exit status `0`
   means the command completed; `1` means the selected command failed; `2`
   means argument parsing, config loading, backend selection, or subcommand
   dispatch failed.

Completion means the selected operation returned status `0`, stdout decoded as
the shape documented below, and stderr was handled separately.

## JSON operations

The following command paths emit JSON; other subcommands keep their normal
text/action behavior.

### Backend selection

Use this to inspect which backend nexo selected and why, or to discover the
capabilities available before choosing a backend-specific operation.

```console
nexo --backend=tmux --json backend
```

The output is one object. `backend` is the selected backend: `tmux`, `herdr`,
or `none`. `none` is a successful auto-detection result when no multiplexer
markers are present; it carries no capabilities, so no backend-specific
operation can follow. `source` is `flag`, `config`, or `auto-detect`, `reason`
explains that selection, and `capabilities` is the sorted list of capability
interfaces on that backend (empty for `none`).

```json
{
  "backend": "tmux",
  "source": "flag",
  "reason": "selected by --backend=tmux",
  "capabilities": [
    "AgentSource",
    "ContainerKiller",
    "ContainerLister",
    "ContainerOpener",
    "ContainerRenamer",
    "Notifier",
    "PaneFocuser",
    "ProjectOpener",
    "RemoteProxy",
    "ScreenReader",
    "WorktreeCreator",
    "WorktreeExtractor"
  ]
}
```

When no multiplexer marker exists, auto-detection succeeds with `none`:

```json
{
  "backend": "none",
  "source": "auto-detect",
  "reason": "no supported multiplexer detected",
  "capabilities": []
}
```

### Project candidates: `list`

Use this for configured-path project discovery without querying a multiplexer.
Candidates are emitted once in configured path order, then filesystem name
order; each uses its canonical path and the first matching configured group.
Warnings go to stderr and do not change the result.

```console
nexo --backend=tmux --json list
```

The output is one array of candidate objects. `git` and `linked_worktree` are
always present because they are non-optional fields in the candidate type.

```json
[
  {
    "path": "/home/me/work/api",
    "group": "/home/me/work",
    "git": true,
    "linked_worktree": false
  }
]
```

An empty discovery result is `[]`.

### Places: `list --places`

Use this when a script needs the place-picker's combined project and open
container rows. It lists local containers through the selected backend and
queries configured remote machines through that same backend. The array is
project discovery followed by local and remote container sources.

```console
nexo --backend=tmux --json list --places
```

Each object has `kind` equal to `project` or `container`. Project rows carry
`path`, `group`, and their non-empty Git flags. Container rows carry
`machine`, `group`, `container_id`, and `container_name`; `preview` is present
when a screen preview was read. Picker-facing `search_text` and
`display_remainder` may be present. Empty-string fields and false `omitempty`
fields are omitted by the encoder.

```json
[
  {
    "kind": "project",
    "path": "/home/me/work/api",
    "group": "/home/me/work",
    "search_text": "/home/me/work/api",
    "display_remainder": "api",
    "git": true
  },
  {
    "kind": "container",
    "group": "devbox",
    "machine": "devbox",
    "search_text": "api",
    "display_remainder": "api",
    "container_id": "$1",
    "container_name": "api",
    "preview": "shell$"
  }
]
```

### Agent panes: `agents`

Use this to rank agent panes by urgency and inspect their backend-neutral
labels before focusing one. The selected backend supplies the agent source;
Herdr's native agent state remains authoritative when Herdr is selected.

```console
nexo --backend=tmux --json agents
```

The output is one array of agent objects. `state` is `working` or `stopped`.
Stopped agents sort before working agents, and unseen stopped agents sort before
seen stopped agents. `target` is opaque and is the value passed back to the
selected backend for focus. `container_id`, `container`, `window`,
`stopped_since`, and `branch` are optional. `stopped_since`, when present, is
RFC3339; it is omitted while an agent is working.

```json
[
  {
    "pane_id": "%4",
    "target": "work:1.2",
    "container_id": "$1",
    "container": "work",
    "window": "editor",
    "harness": "pi",
    "state": "stopped",
    "seen": false,
    "stopped_since": "2025-01-01T12:00:03Z",
    "branch": "feature/example"
  },
  {
    "pane_id": "%5",
    "target": "work:1.3",
    "harness": "codex",
    "state": "working",
    "seen": false
  }
]
```

### Remote containers: `remote list`

Use this to list containers on every configured machine without opening a
proxy. Listing always uses SSH, while the selected backend determines how the
remote container data is decoded. Results retain configured machine order and
one unreachable machine does not discard other results.

```console
nexo --backend=tmux --json remote list
```

The output is one array of per-machine objects, each with `machine` and
`kind` (`tmux` or `herdr`) when the listing succeeds. A machine whose listing
succeeded has `containers` — omitted when that list is empty — with entries
carrying `id`, `name`, and `machine`. A machine whose listing failed carries
`error` (a normalized single-line message) and no `containers`.
Because `containers` is `omitempty`, `{"machine":"empty"}` is a successful
empty listing, not a failure; only `error` signals failure.

Per-machine `error` values live in this stdout JSON: the process still exits
`0` and stderr stays empty, so automation must inspect each `error` field
rather than the process status.

```json
[
  {
    "machine": "devbox",
    "kind": "tmux",
    "containers": [
      {
        "id": "$1",
        "name": "api",
        "machine": "devbox"
      }
    ]
  }
]
```

An empty successful listing omits `containers`; a failed machine reports
`error` instead:

```json
[
  {
    "machine": "empty",
    "kind": "tmux"
  },
  {
    "machine": "offline",
    "error": "timed out after 5s"
  }
]
```

### Open a project: `nexo open`

Use this when an agent launcher must create or reuse a project's container
without moving the user's view, and needs the container id back to open windows
or tabs inside it. `--no-focus` creates the tmux session or Herdr workspace
when it is missing, reuses it when it exists, and never switches the client or
focuses the workspace. Herdr also accepts `--workspace-id ID`, which with
`--no-focus` only verifies that the workspace exists.

```console
nexo --backend=tmux --json open /home/me/src/api --no-focus
```

With `--json` the command writes one object. `backend` is the selected backend,
`id` is the tmux session id or the Herdr workspace id, `name` is the session
name or workspace label, `path` is the project path, and `created` reports
whether this call created the container. Without `--json`, a `--no-focus` open
prints one line and a plain open prints nothing.

```json
{
  "backend": "tmux",
  "id": "$12",
  "name": "src_api",
  "path": "/home/me/src/api",
  "created": true
}
```

### Git worktrees: `worktree create` and `worktree extract`

Use this when creating a worktree and handing its canonical result to the next
step. The positional arguments are `<repo> <branch> <destination>`;
`--new-branch` asks Git to create the branch and `--add-parent` also adds the
destination's parent to the configured project paths.

```console
nexo --backend=tmux --json worktree create --new-branch \
  /home/me/repo feature/example /home/me/worktrees/example
```

`--no-focus` creates and opens the worktree container without moving the
user's view: on tmux it skips the switch or attach, and on Herdr it omits
`--focus`. Without `--no-focus`, creation still opens the container and focuses
it. On tmux, a client inside tmux switches to the new session; outside tmux,
the command attaches to it interactively and inherits the process stdout, so
run it from a real terminal. A piped invocation outside tmux fails the attach,
rolls back the worktree, and returns status `1` with no JSON.

The output is one object with `path`, `branch`, and the `container` the creator
opened. The container uses the same shape as `nexo open --json`: `backend`,
`id`, `name`, `path`, and `created`. The encoder deliberately omits the input
repository, the `--new-branch` choice, and the old top-level container ID and
session name fields that the container now carries.

```json
{
  "path": "/home/me/worktrees/example",
  "branch": "feature/example",
  "container": {
    "backend": "tmux",
    "id": "$12",
    "name": "worktrees_example",
    "path": "/home/me/worktrees/example",
    "created": true
  }
}
```

`worktree extract <repo> <destination>` is the inverse: it moves the branch
currently checked out in `<repo>` into a new worktree at `<destination>` and
leaves a return branch checked out in `<repo>`. It preflights a clean
`<repo>`, a non-existing destination outside every registered worktree,
branch ownership, and an eligible return branch before mutating anything. The
clean check includes all untracked files and submodule changes regardless of
Git status configuration. `--return-branch` chooses the return branch
explicitly; otherwise nexo searches checkout history for the nearest distinct,
existing named branch, then existing remote-default, configured, `main`, and
`master` candidates. The output adds `return_branch`:

```console
nexo --backend=tmux --json worktree extract --return-branch main \
  /home/me/repo /home/me/worktrees/example
```

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

Branch switches refuse to overwrite ignored files. If a switch hook fails
after Git changes `HEAD`, extraction verifies and restores the original branch.
Native creation and opening failures clean up partial worktrees before
restoring that branch, and output failures perform the same extraction-aware
rollback. Recovery uses a bounded context independent of caller cancellation;
cleanup errors are returned rather than hidden.

## Upgrade and update

`nexo upgrade` and `nexo update` are identical text commands. They resolve the
latest authenticated `v*` tag, install it on the local machine and every
configured remote, and print one result line per machine. Until a first tag is
published, the default fails with guidance to use `--nightly`:

```console
nexo upgrade --nightly
nexo upgrade --version v1.2.3 --machine devbox
nexo update --dry-run --local-only
```

`--machine` may be repeated; `--local-only` and `--dry-run` limit scope or
avoid writes. Remote work runs in parallel, and a failed machine makes the
process exit non-zero after all result lines are printed. The revision is built
on this machine and the replacement is atomic, so a launch symlink and the
tmux watcher keep working; a Homebrew-owned nexo is refused and must use
`brew upgrade`. Rerun `nexo tmux init` only after changing watcher
configuration such as `[agents] highlight-color` or `[glance]` keys. No JSON
shape is emitted by this command.

## Global flags and errors

- `--backend auto|tmux|herdr` selects the one backend for this invocation.
  `--backend` has precedence over the `backend` config key, which has
  precedence over auto-detection of the inner multiplexer.
- `-c PATH` and `--config-file PATH` select the one TOML config file. Without
  either flag, nexo reads the optional `XDG_CONFIG_HOME/nexo/config.toml`, or
  `$HOME/.config/nexo/config.toml` when `XDG_CONFIG_HOME` is unset. An explicit
  path must exist and parse successfully. For example:

  ```console
  nexo --backend=tmux --config-file=/tmp/nexo.toml --json list
  ```
- `--json` selects the structured output mode described above. It can appear
  before or after the command, but it is a global flag rather than a positional
  argument.
- `--version` prints the version and exits `0`; `--help` prints command and
  flag help and exits `0`. Use `nexo <command> --help` or
  `nexo help <command> [subcommand]` for nested command help.

stderr is plain text, not a JSON error envelope and not a per-line framing
protocol. Most CLI diagnostics are one line prefixed with `nexo:`, but
interactive children (such as a tmux attach) write their own stderr directly
before the CLI also prefixes the returned error, and a joined error can span
multiple lines. Do not parse stderr; treat it as human diagnostics. A remote
machine listing failure is different: it is a per-machine `error` field in the
stdout JSON, not a stderr diagnostic. For example, an unknown global option is
a parse failure:

```text
$ nexo --unknown
nexo: unknown option "--unknown"
$ echo $?
2
```

A command-level failure uses status `1`, for example an unsupported list
option:

```text
$ nexo --backend=tmux --json list --unknown
nexo: unknown list option "--unknown"
$ echo $?
1
```

## Safe verification

When experimenting with nexo, use fake or throwaway state. Unset
`TMUX`, `TMUX_PANE`, `HERDR_ENV`, `HERDR_SOCKET_PATH`, and `HERDR_PANE_ID`
that point at a user's live multiplexer; use a temporary `XDG_STATE_HOME` and
config file. If a tmux integration is needed, start a separate server with
`tmux -L nexo-skill-$$ -f /dev/null`, direct every test operation to that
server, and kill it when the test completes. Never attach to, stop, or reload a
user's server.

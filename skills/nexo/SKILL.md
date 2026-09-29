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
    "Popup",
    "ProjectOpener",
    "RemoteProxy",
    "ScreenReader",
    "WorktreeCreator"
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

### Git worktrees: `worktree create`

Use this when creating a worktree and handing its canonical result to the next
step. The positional arguments are `<repo> <branch> <destination>`;
`--new-branch` asks Git to create the branch and `--add-parent` also adds the
destination's parent to the configured project paths.

```console
nexo --backend=tmux --json worktree create --new-branch \
  /home/me/repo feature/example /home/me/worktrees/example
```

`--json` changes only the output: creation still opens the new worktree as a
container and focuses it. On Herdr, Herdr creates the worktree workspace and
focuses it. On tmux, a client inside tmux switches to the new session; outside
tmux, the command attaches to it interactively and inherits the process stdout,
so run it from a real terminal. A piped invocation outside tmux fails the
attach, rolls back the worktree, and returns status `1` with no JSON.

The output is one object with only `path` and `branch`. The encoder deliberately
omits the input repository, the `--new-branch` choice, backend container ID,
and session name.

```json
{
  "path": "/home/me/worktrees/example",
  "branch": "feature/example"
}
```

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
  flag help and exits `0`.

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

# Remote containers

Remote machines are listed through one SSH round trip. By default the remote
shell checks whether a Herdr server is running with the read-only
`herdr status server --json` command, then emits that machine's kind beside
its container listing. An entry in `[remote.backends]` selects `tmux` or
`herdr` directly and skips this probe. The kind is cached in that listing
result and reused when a place is opened; no second probe is needed.

A machine whose server is not running Herdr is treated as a tmux machine. On a
tmux backend, opening one of its containers creates one local machine proxy,
labeled from `MACHINE` (possibly `MACHINE~remote`). Its only pane resolves the
most recently attached remote
tmux session at attach time. The local prefix and status bar are disabled for
that proxy, so input and the status bar belong to the remote multiplexer.
Explicit `remote open MACHINE/CONTAINER` creates the proxy with one initial
pinned attach, then switches the proxy's existing remote client for later
opens. M-o/`remote previous` only switches to the existing machine proxy and
never retargets it.

Only the session-scoped `@nexo-proxy` tag identifies a proxy; the local name is
a display label. If a local non-proxy session already uses `MACHINE`, nexo uses
a suffix such as `MACHINE~remote` rather than adopting it. Legacy tagged
`MACHINE/CONTAINER` sessions are left untouched and remain readable as their
machine; they are not migrated or adopted.

```toml
[remote]
machines = ["devbox"]
ssh-config = "~/.ssh/config"
transport = "ssh"              # ssh (default), mosh, or et
reconnect-initial = "1s"
reconnect-max = "30s"

[remote.backends]
devbox = "tmux"                 # auto (default), tmux, or herdr
```

`ssh-config` expands a leading `~`, `~/...` or `$HOME` to the home
directory; any other value is passed to `ssh -F` unchanged; empty uses the
normal SSH lookup.

## Main and remote machines

The main machine runs nexo. Every remote machine runs its own multiplexer and
a copy of nexo that the main machine calls over SSH.

### Main machine

- nexo on `PATH`, plus the adapter for the local backend: the tmux plugin
  (`nexo.tmux`) or the Herdr plugin (`herdr-plugin.toml`) and its key
  bindings.
- `paths` for project discovery.
- An SSH alias for every remote in `~/.ssh/config`, listed in
  `[remote].machines`.
- `[remote.backends] MACHINE = "tmux"` or `"herdr"` when the remote runs both
  a Herdr server and tmux. The auto probe picks Herdr whenever a Herdr server
  is running, so agents living in tmux sessions would be missed. Set it
  explicitly to list the multiplexer you actually use there.
- The proxy attachment transport, `remote.transport` (SSH by default).

```toml
paths = ["~/src"]

[remote]
machines = ["devbox", "gpu1"]

[remote.backends]
devbox = "tmux"                 # remote runs Herdr and tmux; list tmux
gpu1 = "herdr"                  # remote runs a Herdr server
```

### Remote machine

- nexo must be on `PATH` for **non-interactive** SSH shells. Put the export in
  a file those shells read, such as `~/.zshenv`, not `~/.zshrc`. Verify with
  `ssh MACHINE 'command -v nexo'`: a working non-interactive shell prints the
  path. Use a UTF-8 locale for those shells too, as belt and braces.
- Remote invocations read the remote user's `$XDG_CONFIG_HOME/nexo/config.toml`,
  or `~/.config/nexo/config.toml` when `XDG_CONFIG_HOME` is unset. The main
  machine's `--config-file` is not forwarded.
- Its own multiplexer: a tmux server, a Herdr server, or both.
- Its own config only needs `[agents]` when the defaults do not fit. Agent
  processes and working patterns are evaluated on the remote, so change them
  there. `paths` is needed only if nexo is also used on that machine directly.
  A remote config never needs a `[remote]` section.
- For a tmux remote, enable focus events in `~/.tmux.conf`:

  ```tmux
  set -g focus-events on
  ```

  Upgrade all configured machines with `nexo upgrade`; limit it with
  `--machine MACHINE` or `--local-only`, and use `--nightly` before the first
  release is published. The binary is built on the machine running the command
  and copied to each remote, so a remote does not need its own Go toolchain or
  Git credentials. The watcher re-executes itself after an atomic binary
  replacement, so upgrade does not require a stop/start cycle. Changes to the
  watcher configuration still require `nexo tmux init --backend=tmux` on that
  machine.

  On a remote without the plugin, add this to `~/.tmux.conf` instead:

  ```tmux
  if-shell 'command -v nexo >/dev/null 2>&1' 'run-shell "nexo tmux init --backend=tmux"'
  ```

  This sets up the focus hooks and watcher. It is required for seen-marking:
  the local listing reads the `seen` flag that the remote focus hook writes.

```toml
# On the remote, only when the defaults do not fit.
[agents]
processes = ["pi", "claude"]
idle-seconds = 3
poll-interval = 2
working-pattern = { pi = "⠋" }

[tui]
theme = "auto"
```

Agent state is computed on demand: `nexo agents --json` on the remote already
reports each agent as working or stopped without any background process. The
tmux plugin is what installs the focus hook that marks stopped agents seen
(and starts the watcher). Without it, a remote listing still shows the
working/stopped state, but `seen` never advances. Notifications are delivered
by the watcher where it runs, so a remote watcher notifies on the remote
machine, not on the main machine.

## Herdr machines

Herdr is native when the remote machine runs a Herdr server. On the Herdr
backend, `nexo remote open MACHINE/CONTAINER` does not create a local proxy or
nest a Herdr client in a pane. If `MACHINE` is not already in Herdr's saved
machine list, nexo explicitly runs:

```sh
herdr machine add MACHINE --label MACHINE
```

It reports that the machine was saved, then reports that the selected
container is available under that machine in Herdr's sidebar. `nexo remote
new MACHINE NAME` creates the remote workspace with Herdr over SSH and follows
the same native-open flow. Closing a Herdr remote container reports that there
is no local proxy to close; it never closes the remote workspace.

A Herdr remote cannot be opened from the tmux backend. nexo returns an
unsupported-backend message directing the user to open it from Herdr instead.

## tmux remotes from Herdr

When the local backend is Herdr and the remote kind is tmux, nexo fills the
missing capability with one Herdr workspace proxy per machine. The workspace
label and `nexo-proxy` token are the machine name only, and its root pane runs
the same unpinned hidden `nexo remote attach` command and reconnect loop used
by tmux proxies. Herdr's `NEXO_PROXY_MACHINE` environment is machine-only;
there is no fixed `NEXO_PROXY_CONTAINER`.

Only the metadata token identifies a proxy. A local workspace label matching a
machine is never adopted as a machine proxy. Legacy machine/container tokens
remain readable as their machine and are left untouched.

## Attachment and commands

The attachment resolves the remote target with
`tmux list-sessions -F '#{session_last_attached} #{session_id}'`, sorted by
`session_last_attached`; it deliberately does not use `session_activity`,
which a chatty pane can win. The command runs through `sh -c` for SSH, mosh,
and ET and records its remote tmux client tty under the runtime directory.
The rendezvous key includes the backend, so tmux and Herdr proxies for one
machine cannot collide. An explicit open resolves that tty with
`tmux list-clients -F '#{client_tty} #{client_name}'` and switches the same
remote client without restarting the local pane. If the target is missing it
reports `no session CONTAINER on MACHINE` and leaves the healthy proxy alone.
If the client is stale or the link is unavailable, tmux respawns the pane;
Herdr closes and recreates its workspace with the requested initial target.

Dropped attachments retry with exponential backoff up to `reconnect-max` and
all reconnects are unpinned. SSH enables `ServerAliveInterval=15` and
`ServerAliveCountMax=3`. mosh and et still provide their own transport-level
reconnection; `RunReconnect` additionally reruns the attachment resolver after
any transport process exits, so a removed remote session moves to the next
most-recent session instead of ending the local proxy. When there are no
remote sessions, the pane remains alive and prints `no sessions on MACHINE —
press r to retry, q to close`; it never creates a remote session. Closing a
proxy only removes the local container; it never closes the remote container.

Commands:

- `nexo remote open MACHINE/CONTAINER` opens a native Herdr container or the
  appropriate local proxy.
- `nexo remote new MACHINE [NAME]` creates a remote Herdr workspace or tmux
  session and opens it using the same rules as `remote open`.
- `nexo remote close [MACHINE|MACHINE/CONTAINER]` closes only the local proxy. With no
  target inside a machine proxy, it closes by machine; outside a proxy a target
  is required.
- `nexo remote list` lists remote containers and includes each machine's
  detected kind in `--json` output. Listing always uses SSH.
- `nexo previous` opens the last non-proxy container on this machine that is
  not the current one. It skips proxies, containers that no longer exist, and
  the container that currently owns the pane, and does nothing when nothing
  else is live.
- `nexo remote previous`/M-o toggles by machine: it opens the most recently
  used configured machine other than the current one. The current machine is
  the proxy's machine inside a proxy and the local machine otherwise. It
  switches to an existing machine proxy as-is or creates one unpinned; it
  never lists remote sessions or retargets an existing proxy. Legacy
  `MACHINE/CONTAINER` history entries count as their machine. Local entries are
  considered only from a proxy and only while live.

Both commands read the same durable history that focus hooks and events write:
tmux records `pane-focus-in` and `client-session-changed` through `nexo tmux
init`, and the Herdr plugin declares `[[events]]` hooks for
`workspace.focused` and `pane.focused` that run `nexo --backend=herdr focus`.

### Root-key passthrough

`nexo remote passthrough KEY...` is a user-bound helper for root-table keys.
Inside a proxy it sends the key sequence through the attached pane to the
remote multiplexer. Outside a proxy it injects nothing and exits with a
handled non-zero status, so the binding's own fallback runs the original local
action. It uses the backend's native key names and safe send-keys argument
handling. Nexo never installs this binding, and `none` means simply that the
user does not bind the command.

In tmux, `run-shell` does not inherit `TMUX_PANE`, so capture the current pane
explicitly and give the binding its local fallback after `||`:

```tmux
bind-key -T root C-h run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux remote passthrough C-h || tmux select-pane -L'
bind-key -T root C-j run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux remote passthrough C-j || tmux select-pane -D'
bind-key -T root C-k run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux remote passthrough C-k || tmux select-pane -U'
bind-key -T root C-l run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux remote passthrough C-l || tmux select-pane -R'
```

In Herdr, add a key action in the user's key/action configuration whose shell
command falls back to the native action:

```sh
nexo --backend=herdr remote passthrough ctrl+h || herdr pane focus --direction left
```

### Clipboard image paste

`nexo remote paste [KEY...]` is a user-bound helper for pasting a clipboard PNG
into a proxy. It reads the local clipboard, uploads the image over SSH into a
mode-0700 `mktemp -d` directory as a mode-0600 `nexo-paste-*.png` file, and
types the shell-quoted remote path into the attached pane. Outside a proxy, or
when the clipboard has no image, it sends the original `KEY...` through
unchanged. A missing local clipboard tool prints one notice before the key is
passed through.

nexo never installs this binding. In tmux, `run-shell` does not inherit
`TMUX_PANE`, so the binding captures the current pane explicitly:

```tmux
bind-key -T root C-v run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux remote paste C-v'
```

In Herdr, add a key action in the user's Herdr key/action configuration that
runs:

```sh
nexo --backend=herdr remote paste ctrl+v
```

Pass the key using the syntax of the binding that invoked the command. tmux
key names such as `C-v`, `M-v`, and `Enter` are forwarded to `tmux send-keys`;
Herdr key names such as `ctrl+v`, `alt+v`, and `enter` are forwarded to
`herdr pane send-keys`. The platform clipboard readers are `pngpaste` (or the
macOS AppleScript fallback), `wl-paste` on Wayland, and `xclip` on X11.
End-to-end verification with a real SSH proxy and a real macOS clipboard reader
is still needed.

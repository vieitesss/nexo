<p align="center">
  <img src="assets/logo.svg" alt="nexo" width="120">
</p>
<h1 align="center">nexo</h1>
<p align="center"><strong>Find your projects, containers, and coding agents in one fast terminal UI.</strong></p>

<p align="center">
  <a href="https://github.com/vieitesss/nexo/releases"><img src="https://img.shields.io/github/v/release/vieitesss/nexo?style=flat-square&label=latest%20release" alt="Latest release"></a>
  <a href="https://github.com/vieitesss/nexo/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/vieitesss/nexo/ci.yml?style=flat-square&label=tests" alt="Tests"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/vieitesss/nexo?style=flat-square" alt="MIT license"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/vieitesss/nexo?style=flat-square" alt="Go version"></a>
  <img src="https://img.shields.io/badge/backend-tmux%20%7C%20herdr-5b6472?style=flat-square" alt="tmux and Herdr backends">
  <a href="https://github.com/vieitesss/nexo/commits/main"><img src="https://img.shields.io/github/last-commit/vieitesss/nexo?style=flat-square" alt="Last commit"></a>
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#local-setup">Local setup</a> ·
  <a href="#remote-machines">Remote machines</a> ·
  <a href="#upgrading">Upgrading</a> ·
  <a href="#configuration">Configuration</a> ·
  <a href="#usage">Usage</a>
</p>

nexo is a Go CLI and terminal UI that takes you to your projects, the
containers open on this and other machines, and the coding agents that need
you. It drives exactly one backend per invocation: tmux or Herdr in v1. The
place picker answers "where do I go?"; the agent navigator answers "who needs
me?". Both are full-screen Bubble Tea programs with asynchronous discovery and
no `fzf` dependency.

- **Place picker.** Search project candidates and open local or remote
  containers from one grouped picker.
- **Agent navigator.** Find every detected agent pane, with unseen stopped
  agents first, then focus the exact pane.
- **Remote machines.** List configured machines over SSH and open their
  containers through a local tmux or Herdr proxy.
- **Notifications.** The tmux watcher sends durable working-to-stopped desktop
  notifications; Herdr keeps its native agent state and notifications.
- **Git context.** Project rows show branch or short SHA, changes, cached
  ahead/behind, and a colour-coded pull request number.
- **Two backends.** tmux and Herdr behind one CLI and one TOML configuration.

Agent state is one dot:

| Dot | State | Meaning |
| --- | --- | --- |
| 🟡 yellow | `working` | The agent's working indicator is live. |
| 🔴 red | `stopped` / unseen | The agent stopped and its exact pane has not been focused yet. |
| 🟢 green | `stopped` / seen | The stopped agent has been seen. |

The tmux glance row and the TUI both use these colours by default;
`[glance] colors` restyles the glance row only. See
[docs/tui.md](docs/tui.md) for the row behaviour and
[docs/agents.md](docs/agents.md) for the watcher, notifications, and the
unseen window highlight. For the domain model see [CONTEXT.md](CONTEXT.md),
for the settled decisions [docs/adr](docs/adr), and for startup measurements
[docs/startup.md](docs/startup.md).

## Install

> The repository is private and no release has been published yet. While it is
> private, a Go install needs `GOPRIVATE` and authenticated Git access (for
> example an SSH key or `gh auth login`). Once the repository is public, the
> plain `go install` and the other methods below apply; see
> [docs/release.md](docs/release.md).

Homebrew, after the first release:

```sh
brew install vieitesss/tap/nexo
```

Go install, with the private-repository prefix (drop it once public):

```sh
GOPRIVATE=github.com/vieitesss/nexo go install github.com/vieitesss/nexo/cmd/nexo@latest
```

Release binary, after the first release: download the darwin or linux archive
for amd64 or arm64 from the
[releases page](https://github.com/vieitesss/nexo/releases), unpack `nexo`, and
put it on `PATH`. Tagged releases include checksums and a Homebrew formula.

Whichever method you use, confirm the binary resolves before continuing:

```sh
command -v nexo
```

A Go install writes to `$(go env GOBIN)`, or `$(go env GOPATH)/bin` when
`GOBIN` is unset; add that directory to `PATH` if the check prints nothing.

## Local setup

nexo drives the multiplexer that owns the current pane. Install the adapter for
your backend. Key bindings stay yours: nexo installs none; a tmux user also
needs the init line and agent watcher below, while Herdr keeps its native agent
state and notifications and needs no nexo watcher.

### tmux

Enable focus events, add the init step, and reload the file after every edit.
Seen-marking needs focus events:

```tmux
set -g focus-events on
```

`nexo tmux init` installs the tmux focus hooks and starts the agent watcher.
Pick one of these two ways to run it at startup.

With [TPM](https://github.com/tmux-plugins/tpm), add the plugin line and press
`prefix` + `I`:

```tmux
set -g @plugin 'vieitesss/nexo'
```

Without TPM, add the init line directly. From a checkout, the committed
`nexo.tmux` adapter does the same and can be run once with
`tmux run-shell /path/to/nexo/nexo.tmux`:

```tmux
if-shell 'command -v nexo >/dev/null 2>&1' 'run-shell "nexo tmux init --backend=tmux"'
```

Add the bindings you want to `~/.tmux.conf`. `display-popup` needs tmux 3.2 or
newer. Change the keys to suit your setup:

```tmux
# TUI: all tabs on Ctrl-S, the agent navigator on prefix a.
bind -n C-s display-popup -E -B -w 100% -h 100% "nexo tui"
bind a display-popup -E -B -w 100% -h 100% "nexo tui agents"

# Toggle the glance row that lists agent panes.
bind -n M-g run-shell 'nexo --backend=tmux tmux glance toggle'

# Last local container, and toggle between local and remote machines.
bind-key -n M-o run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux previous'
bind-key -n M-p run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux remote previous'

# Paste a clipboard image into a remote proxy. run-shell does not inherit
# TMUX_PANE, so the binding passes it explicitly.
bind-key -T root C-v run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux remote paste C-v'

# Forward root-table navigation keys while a proxy is attached. Outside a
# proxy nexo exits without injecting keys, so the binding's `||` fallback runs
# the original local action.
bind-key -T root C-h run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux remote passthrough C-h || tmux select-pane -L'
bind-key -T root C-j run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux remote passthrough C-j || tmux select-pane -D'
bind-key -T root C-k run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux remote passthrough C-k || tmux select-pane -U'
bind-key -T root C-l run-shell 'TMUX_PANE=#{pane_id} nexo --backend=tmux remote passthrough C-l || tmux select-pane -R'
```

Apply every edit to the running server:

```sh
tmux source-file ~/.tmux.conf
```

See [docs/agents.md](docs/agents.md) for the watcher, the unseen window
highlight, the glance row, and notification details. The full root-key
passthrough and clipboard-paste reference is in
[docs/remote.md](docs/remote.md).

### Herdr

Install the binary first, then the plugin (Herdr 0.9.0 or newer):

```sh
herdr plugin install vieitesss/nexo
```

For a checkout under development, link the manifest instead:

```sh
herdr plugin link /path/to/nexo
```

The manifest provides four TUI actions and popup panes, plus focus hooks for
`nexo previous`. Herdr's native agent sidebar already lists agent panes, so the
glance row is tmux-only. To bind the actions, append or merge `[[keys.command]]`
blocks into the existing `[keys]` configuration in
`~/.config/herdr/config.toml`. For example:

```toml
[[keys.command]]
key = "ctrl+s"
type = "plugin_action"
command = "nexo.tui"
description = "open nexo TUI"

[[keys.command]]
key = "prefix+a"
type = "plugin_action"
command = "nexo.tui-agents"
description = "open nexo agents"
```

The manifest also declares `nexo.tui-containers` and `nexo.tui-projects`; see
[docs/herdr.md](docs/herdr.md) for the full list.

Reload the configuration after editing it:

```sh
herdr server reload-config
```

See [docs/herdr.md](docs/herdr.md) for the complete adapter setup and
[docs/remote.md](docs/remote.md) for native Herdr remote containers.

## Remote machines

Each remote machine runs its own multiplexer and a copy of nexo that the main
machine calls over SSH. Set up both sides. The full reference, including Herdr
remotes and proxy behaviour, is in [docs/remote.md](docs/remote.md).

### On the local machine

- Install nexo locally, start your own multiplexer, and add an SSH alias for
  every remote in `~/.ssh/config`.
- List those aliases in `[remote].machines`. The minimal config in
  [Configuration](#configuration) shows the `[remote]` and `[remote.backends]`
  shape.
- If a remote runs both a Herdr server and tmux, set its kind explicitly in
  `[remote.backends]`. The auto probe picks Herdr whenever a Herdr server is
  running, so agents living in tmux sessions would be missed.
- `remote.transport` selects the proxy attachment transport: `ssh` by default,
  plus `mosh` and `et`. Listing always uses SSH.

### On each remote machine

- Install nexo using the method above and start that machine's multiplexer.
- nexo must be on `PATH` for **non-interactive** SSH shells. Put the export in
  a file those shells read, such as `~/.zshenv`, not `~/.zshrc`. Verify with
  `ssh MACHINE 'command -v nexo'`: a working shell prints the path. Use a UTF-8
  locale for those shells too.
- For a tmux remote, add focus events and the init line to that machine's
  `~/.tmux.conf`, then reload it:

  ```tmux
  set -g focus-events on
  if-shell 'command -v nexo >/dev/null 2>&1' 'run-shell "nexo tmux init --backend=tmux"'
  ```

  ```sh
  ssh MACHINE 'tmux source-file ~/.tmux.conf'
  ```

  The focus hook is required for seen-marking: the local listing reads the
  `seen` flag the remote hook writes. Without it the working/stopped state
  still appears, but `seen` never advances.
- A remote config only needs `[agents]`, and only when the defaults do not fit;
  agent processes and working patterns are evaluated on the remote. It reads
  its own `$XDG_CONFIG_HOME/nexo/config.toml`, or
  `~/.config/nexo/config.toml` when `XDG_CONFIG_HOME` is unset, and never a
  `[remote]` section. The main machine's `--config-file` is not forwarded.

```toml
# On the remote, only when the defaults do not fit.
[agents]
processes = ["pi", "claude"]
idle-seconds = 3
poll-interval = 2
working-pattern = { pi = "⠋" }
```

## Upgrading

The tmux watcher reloads itself when its nexo binary is replaced atomically, as
`just install` does, so upgrades need no manual step. Changing
`[agents] highlight-color` or any `[glance]` key still needs a restart, because
the watcher reads those values once at startup. See
[docs/agents.md](docs/agents.md) for details and the non-Linux startup-window
limitation. Init stops the running watcher and starts a new one, so rerun it on
every machine running the watcher:

```sh
nexo tmux init --backend=tmux
ssh MACHINE 'nexo tmux init --backend=tmux'
```

If the watcher runs with an explicit `--config-file`, pass the same path to
init. Herdr keeps its native agent state and notifications, so a Herdr-only
machine needs no restart.

## Configuration

nexo reads one TOML file. Without `-c` or `--config-file`, it reads
`$XDG_CONFIG_HOME/nexo/config.toml`, or `$HOME/.config/nexo/config.toml` when
`XDG_CONFIG_HOME` is unset. The default path is optional; an explicit path must
exist and parse successfully. Unknown keys are rejected.

```toml
backend = "auto"
paths = ["~/src", "~/work/*"]

[agents]
notify = true

[glance]
colors = { working = "yellow", unseen = "red", seen = "green" }

[remote]
machines = ["devbox"]

[remote.backends]
devbox = "tmux"
```

See [docs/configuration.md](docs/configuration.md) for every option, its
default, and a complete annotated example.

## Usage

Running `nexo` with no command prints usage. Use `nexo --help` for the
command table and global flags, or `nexo <command> --help` (and
`nexo help <command>`) for a command's subcommands, flags, and examples. The
TUI entry point accepts an optional starting tab, and global flags can appear
before or after a command. See [docs/usage.md](docs/usage.md) for the full
command reference and examples.

| Command | Purpose |
| --- | --- |
| `nexo tui [agents\|containers\|projects]` | Open the tabbed terminal UI. |
| `nexo list [--places]` | List project candidates; `--places` adds open local and remote containers, each local one with a screen preview. |
| `nexo agents [watch]` | List agent panes or run the watcher. |
| `nexo previous` | Open the last non-proxy local container. |
| `nexo remote list\|open\|new\|close\|previous\|paste\|passthrough` | List, open, create, close, and attach to remote containers. |
| `nexo open PATH` | Open a discovered project candidate. |
| `nexo clone`, `nexo delete`, `nexo worktree` | Clone repositories, delete candidates, and create or extract Git worktrees. |
| `nexo backend` | Print the selected backend and its capabilities. |
| `nexo tmux init`, `nexo tmux glance on\|off\|toggle` | Install the tmux adapter, or show, hide, or toggle the glance row. |

Add `--json` for structured output where offered. See
[docs/usage.md](docs/usage.md) for the complete command reference and
examples, [docs/list-json.md](docs/list-json.md) for JSON shapes, and
[docs/tui.md](docs/tui.md) for the picker keys and row behaviour.

## Backends

Exactly one backend is active per invocation: the `--backend` flag wins over
the `backend` config key, and an empty or `auto` selection detects the inner
multiplexer. Backends are native-first: nexo uses a multiplexer's own
capability when one exists and only fills gaps. tmux and Herdr are
implemented; Zellij, WezTerm, and Kitty are design checks only. See
[docs/backends.md](docs/backends.md) for the capability matrix,
[docs/herdr.md](docs/herdr.md) for the Herdr adapter,
[docs/agents.md](docs/agents.md) for the tmux agent features, and
[docs/remote.md](docs/remote.md) for remote containers.

## Contributing

Build and verify changes locally:

```sh
just build
just test
just lint
```

Please keep capability interfaces small, adapters thin, and behavior native to
the selected backend where possible. Start with [CONTEXT.md](CONTEXT.md), then
read the relevant decision records in [docs/adr](docs/adr),
[docs/agents.md](docs/agents.md), [docs/remote.md](docs/remote.md), and
[docs/worktree.md](docs/worktree.md).

## License

[MIT](LICENSE)

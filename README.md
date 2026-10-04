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

Install nexo using one of the methods below, then run the guided setup shown after the install check.

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

Then configure the local machine and its SSH remotes in one reviewed plan:

```sh
nexo setup
```

A Go install writes to `$(go env GOBIN)`, or `$(go env GOPATH)/bin` when
`GOBIN` is unset; add that directory to `PATH` if the check prints nothing.

## Local setup

On a fresh machine, the guided path is one command:

```sh
nexo setup
```

It detects the multiplexer owning the current pane, writes the shared config,
installs or reloads the selected adapter, and starts the tmux watcher. Use
`nexo setup --check` to print the local and remote checklist. In scripts use
`--yes`, `--dry-run`, `--local-only`, or repeat `--machine ALIAS`; setup never
prompts when stdin is not a TTY. The hand-operated reference is in
[docs/setup.md](docs/setup.md).

The setup wizard configures the adapter owned by the current pane. It keeps
bindings opt-in: choose them during setup or add your own later. tmux accepts
only tmux key names such as `C-s`, `a`, `F1`, or `Enter`; the wizard rejects
invalid names and every spelling of Ctrl-Space, which is the shared multiplexer
prefix. Herdr uses its native agent state and notifications and needs no nexo
watcher. See
[docs/setup.md](docs/setup.md) for manual tmux/Herdr instructions,
[docs/agents.md](docs/agents.md) for watcher details, and
[docs/remote.md](docs/remote.md) for remote bindings and proxy controls.

## Remote machines

`nexo setup` also configures every selected SSH alias from one place. It probes
machines in parallel, installs a matching binary, makes it visible to
non-interactive shells, checks UTF-8, configures the remote multiplexer, and
records `[remote].machines` and `[remote.backends]`. A different-platform
remote is built from the running binary's tagged revision or commit with the
local Go toolchain; an unversioned development binary refuses that remote.
Select a subset with
`nexo setup --machine devbox --machine gpu1`; use `--local-only` to skip SSH.
The full hand-operated reference, including Herdr remotes and proxy
behaviour, is in [docs/setup.md](docs/setup.md) and [docs/remote.md](docs/remote.md).
A remote proxy is one local container per machine (not one per remote
container). Its client follows the machine's most recently attached tmux
session; `remote open MACHINE/CONTAINER` targets it explicitly, while M-o only
returns to the existing machine proxy.

The wizard selects SSH aliases, installs or cross-builds nexo, repairs the
non-interactive PATH and locale, configures the chosen remote backend, and
records it in `[remote.backends]`. If both tmux and a Herdr server are running,
setup asks for that machine's backend in a TTY; in scripts, set the config key
first. Use `--fix-locale` only after reviewing the offered UTF-8 locale.

For manual provisioning, remote startup files, Herdr remotes, backend choice,
proxy transports, and optional remote `[agents]` configuration, use
[docs/setup.md](docs/setup.md) and [docs/remote.md](docs/remote.md). The
wizard is the recommended path and `nexo setup --check` is the read-only
verification command.

## Upgrading

Use `nexo upgrade` (or its identical `nexo update` alias) to atomically install
the latest tagged release on this machine and every configured remote. Use
`--nightly` for `main`, `--version vX.Y.Z` for a specific release, and
`--dry-run` or `--machine ALIAS` to inspect or limit the targets. The default
fails with guidance to use `--nightly` until the first release is published.

Upgrade builds the requested revision on this machine with its Go toolchain
and authenticated Git access, then copies the binary to each target, so a
private repository needs the same `GOPRIVATE` and credentials the initial
install used. A Homebrew-managed nexo is refused because Homebrew cannot
publish atomically; run `brew upgrade vieitesss/tap/nexo` on that machine.

The tmux watcher reloads itself when its nexo binary is replaced atomically, as
`nexo upgrade` and `just install` do, so upgrades need no manual step. Changing
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
| `nexo upgrade` / `nexo update` | Atomically upgrade this machine and configured remotes; use `--nightly`, `--version`, `--machine`, or `--dry-run`. |

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

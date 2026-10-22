<p align="center">
  <img src="assets/logo.svg" alt="nexo" width="120">
</p>
<h1 align="center">nexo</h1>
<p align="center"><strong>Jump between your projects, sessions, and the coding agents waiting on you.</strong></p>

<p align="center">
  <a href="https://github.com/vieitesss/nexo/releases"><img src="https://img.shields.io/github/v/release/vieitesss/nexo?style=flat-square&label=release" alt="Latest release"></a>
  <a href="https://github.com/vieitesss/nexo/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/vieitesss/nexo/ci.yml?style=flat-square&label=tests" alt="Tests"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/vieitesss/nexo?style=flat-square" alt="MIT license"></a>
  <img src="https://img.shields.io/badge/backend-tmux%20%7C%20herdr-5b6472?style=flat-square" alt="tmux and Herdr backends">
</p>

nexo is a terminal UI on top of your multiplexer (tmux or
[Herdr](https://herdr.dev)). One keypress shows:

- **Projects**: every Git repository under the folders you configure, with
  branch, changes, and ahead/behind. Pick one to open or jump to its session.
- **Containers**: the sessions open on this machine and on your SSH machines.
- **Agents**: every coding agent pane (Claude Code, Codex, pi, …), with the
  ones that stopped and need you first.

| Dot | Meaning |
| --- | --- |
| 🟡 | The agent is working. |
| 🔴 | The agent stopped and you haven't looked yet. |
| 🟢 | The agent stopped and you've seen it. |

## Install

Homebrew:

```sh
brew install vieitesss/tap/nexo
```

Go:

```sh
go install github.com/vieitesss/nexo/cmd/nexo@latest
```

Or download a darwin/linux archive from the
[releases page](https://github.com/vieitesss/nexo/releases) and put `nexo` on
your `PATH`.

## Get started

Run the guided setup from inside tmux or Herdr:

```sh
nexo setup
```

It detects your multiplexer, writes the config, installs the adapter and key
bindings you choose, and can set up your SSH machines too. `nexo setup --check`
shows what is configured without changing anything.

Then open the TUI with your binding, or run:

```sh
nexo tui
```

## Configure

nexo reads `~/.config/nexo/config.toml` (or `$XDG_CONFIG_HOME/nexo/config.toml`):

```toml
paths = ["~/src", "~/work/*"]   # where your projects live

[agents]
notify = true                   # desktop notification when an agent stops

[remote]
machines = ["devbox"]           # SSH aliases to list and open
```

Every option is in [docs/configuration.md](docs/configuration.md).

## Commands

| Command | Does |
| --- | --- |
| `nexo tui [agents\|containers\|projects]` | Open the TUI, optionally on a tab. |
| `nexo open PATH` | Open a project. |
| `nexo previous` | Go back to the last session. |
| `nexo agents` | List agent panes. |
| `nexo remote list\|open\|previous` | List and open sessions on SSH machines. |
| `nexo clone`, `nexo worktree`, `nexo delete` | Clone repos, create Git worktrees, delete projects. |
| `nexo upgrade` | Upgrade nexo here and on your SSH machines. |

Most commands take `--json`. Run `nexo <command> --help` for flags, or see
[docs/usage.md](docs/usage.md).

## Docs

- [Setup](docs/setup.md) and [configuration](docs/configuration.md)
- [TUI keys and rows](docs/tui.md)
- [Agents, notifications, and the glance row](docs/agents.md)
- [Remote machines](docs/remote.md)
- [Git worktrees](docs/worktree.md)
- [Herdr](docs/herdr.md) and [other backends](docs/backends.md)
- [Releases and upgrading](docs/release.md)

## Contributing

```sh
just build && just test && just lint
```

Start with [CONTEXT.md](CONTEXT.md) for the vocabulary and
[docs/adr](docs/adr) for the decisions behind the design.

## License

[MIT](LICENSE)

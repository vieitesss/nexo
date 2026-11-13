# Configuration

nexo reads one TOML file. Without `-c` or `--config-file`, it reads
`$XDG_CONFIG_HOME/nexo/config.toml`, or `$HOME/.config/nexo/config.toml` when
`XDG_CONFIG_HOME` is unset. The default path is optional; an explicit path must
exist and parse successfully. Unknown keys are rejected, so a typo fails at
startup instead of silently using a default.

`[agents]`, `[glance]`, and `[tui]` control the tmux watcher and the interface.
`[remote]` and `[remote.backends]` configure remote machines; see
[remote.md](remote.md) for the setup on both sides.

## Complete example

```toml
backend = "auto"
paths = ["~/src", "~/work/*"]
git-from-home = false

[agents]
processes = ["pi", "claude", "codex", "opencode", "hermes", "aider", "cursor"]
idle-seconds = 3
poll-interval = 2
notify = true
sound = false
working-pattern = { pi = "⠋" }
highlight-color = "red"

[glance]
# Agent fields shown per entry. Known fields render in this fixed order:
# dot, target, harness, session, window, branch, age.
fields = ["dot", "target"]
# Row tint: a tmux colour, a palette option such as @thm_surface_0, or "default".
tint = "default"
# State dot: "color" draws one ● coloured by state; "symbols" draws ◐ ● ○ uncoloured.
dots = "color"
# Dot colours: tmux colours or palette options; "default" keeps the status foreground.
colors = { working = "yellow", unseen = "red", seen = "green" }

[tui]
# Theme selection: auto, dark, or light. A running TUI follows changes live.
theme = "auto"

# Up to three extra keys on a Projects row; see "Project actions" below.
[[tui.project-actions]]
key = "ctrl+s"
behavior = "split-right"
label = "split"

[clone]
default-protocol = "ssh"
ssh-prefix = "git@github.com:"
https-prefix = "https://github.com/"

[remote]
machines = ["devbox"]
# "~", "~/..." and "$HOME" expand to the home directory. Use "" for the normal SSH lookup.
ssh-config = "~/.ssh/config"
transport = "ssh"
reconnect-initial = "1s"
reconnect-max = "30s"

# Optional per-machine multiplexer selection. Unlisted machines use auto.
[remote.backends]
devbox = "tmux"             # auto, tmux, or herdr
```

## Options and defaults

| Option | Default | Meaning |
| --- | --- | --- |
| `backend` | auto-detect (empty) | Select `auto`, `tmux`, or `herdr`; a flag takes precedence over this key. |
| `paths` | `[]` | Parent directories whose immediate children are project candidates. A trailing `/*` discovers one more directory level; `~` and `$HOME` expand. |
| `git-from-home` | `false` | Also discover Git directories directly under `$HOME`. |
| `agents.processes` | `pi`, `claude`, `codex`, `opencode`, `hermes`, `aider`, `cursor` | Allowlisted executable names used to identify agent panes. |
| `agents.idle-seconds` | `3` | Seconds without a working indicator before a tmux agent is `stopped`. Values `0` or less use the default. |
| `agents.poll-interval` | `2` | Seconds between tmux watcher and navigator refreshes. Values `0` or less use the default. |
| `agents.working-pattern` | `{}` | Per-harness literal screen substring used as the working indicator; configured patterns take precedence over built-ins. |
| `agents.notify` | `true` | Queue tmux working-to-stopped desktop notifications. Herdr notifications are native and unaffected. |
| `agents.sound` | `false` | Play a short sound with tmux notifications. |
| `agents.highlight-color` | `red` | tmux window-list colour for a window holding an unseen stopped agent. A value with a `=` is used as a full style (for example `bg=red,bold`); otherwise it becomes `fg=<colour>`. Read when the watcher starts, so rerun `nexo tmux init` after changing it; init replaces the running watcher. Herdr uses its native agent state instead. |
| `glance.fields` | `dot`, `target` | Agent fields shown in each glance entry, in the fixed order `dot, target, harness, session, window, branch, age`. Unknown or duplicate fields are rejected. |
| `glance.tint` | none | Background tint for the tmux glance row: a tmux colour, a `@palette` option such as `@thm_surface_0`, or `default`/empty for no tint. |
| `glance.dots` | `color` | How the glance `dot` field shows agent state: `color` draws one `●` in the state's colour; `symbols` draws an uncoloured `◐` working, `●` unseen stopped or `○` seen stopped, for terminals without colour. |
| `glance.colors.working` | `yellow` | Glance dot colour for a working agent: a tmux colour, a `@palette` option, or `default` for the status foreground. |
| `glance.colors.unseen` | `red` | Glance dot colour for an unseen stopped agent; default red, independently of `agents.highlight-color`. Same forms as `working`. |
| `glance.colors.seen` | `green` | Glance dot colour for a seen stopped agent. Same forms as `working`. |
| `tui.theme` | `auto` | Select `auto`, `dark`, or `light`; auto queries the terminal background and falls back to `COLORFGBG`, then dark. A running TUI polls the config and follows `dark`/`light` changes without a restart; `auto` is not re-probed mid-session. |
| `tui.project-actions` | `[]` | Up to three extra keys on a Projects row, each opening the project beside the current pane; see [Project actions](#project-actions). |
| `clone.default-protocol` | `ssh` | Protocol selected when the clone form opens: `ssh` or `https`. |
| `clone.ssh-prefix` | `git@github.com:` | Prefix prepended to the repository path in the SSH form. |
| `clone.https-prefix` | `https://github.com/` | Prefix prepended to the repository path in the HTTPS form. |
| `remote.machines` | `[]` | SSH aliases to list and open. `local`, the local hostname, and aliases beginning with `-` are rejected. |
| `remote.backends` | `{}` | Optional per-machine backend table. Keys also configure machines, even when absent from `remote.machines`; values are `auto`, `tmux`, or `herdr`. Unlisted machines use `auto`. |
| `remote.ssh-config` | `""` | Optional SSH config path for listing and SSH attachment. A leading `~`, `~/...` or `$HOME` expands to the home directory; any other value is passed through as written; empty uses the normal SSH lookup. |
| `remote.transport` | `ssh` | Proxy attachment transport: `ssh`, `mosh`, or `et`. Listing always uses SSH. |
| `remote.reconnect-initial` | `1s` | Initial retry delay for SSH proxy attachments; must be positive and no greater than `reconnect-max`. |
| `remote.reconnect-max` | `30s` | Maximum retry delay for SSH proxy attachments; must be positive. |

## Project actions

Enter opens a project in its own container. A project action opens it beside
the pane you came from instead, with a key of your choice:

```toml
[[tui.project-actions]]
key = "ctrl+s"
behavior = "split-right"
label = "split"

[[tui.project-actions]]
key = "alt+w"
behavior = "float"
```

| Field | Meaning |
| --- | --- |
| `key` | `ctrl+<letter>` or `alt+<letter>`, lower case. Keys that nexo's actions, its search box, or the terminal already use are rejected: `ctrl+a b c d e f h i j k m n o p r t u v w x y` and `alt+b d f`. That leaves `ctrl+g l q s z` and the other `alt+` letters. |
| `behavior` | `split-right`, `split-down`, `tab`, or `float`. |
| `label` | Optional footer text of at most 8 columns; defaults to the behavior. |

At most three actions fit beside the built-in footer hints. Each backend
implements the behaviors it can; `nexo backend --json` lists them under
`project_behaviors`:

| Behavior | tmux | Rex | Herdr |
| --- | --- | --- | --- |
| `split-right`, `split-down` | `split-window` | `rex split` | `herdr pane split` |
| `tab` | `new-window` | `rex window new` | `herdr tab create` |
| `float` | not supported | floating layer | not supported |

tmux and Herdr popups block all other input and close with their command, so
neither is offered as a float. One config serves every backend: an action the
current backend cannot perform is left out of the footer, and its key shows
`<behavior> is not supported by <backend>` instead of failing.

Splits go beside the pane you came from. nexo's tmux and Herdr popups are not
panes, so those backends find it on their own. A Rex launcher that opens nexo
in a floating block must name it in `NEXO_ORIGIN_PANE`, because the float
itself is focused while nexo runs; any backend honours that variable.

The action runs while the TUI is still open. Success closes the TUI; a
failure keeps it open and shows the error in the footer.

## Changes that need tmux init

The tmux watcher re-executes itself when its binary is atomically replaced, so
`nexo upgrade` does not restart or signal it. The watcher still reads
`[agents] highlight-color` and every `[glance]` key once when it starts. After
changing any of them, rerun `nexo tmux init` on each machine running the
watcher (and keep the same `--config-file` when one was used). Herdr needs no
watcher init. `[tui] theme` is the exception: a running TUI polls the config
and follows `dark`/`light` changes without a restart.

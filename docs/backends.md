# Future backend capability check

This note checks the v1 capability interfaces against the three candidate
backends, in priority order. It is a design check only: it does not add
backend code or change backend selection. A single nexo invocation still
selects exactly one backend.

## Reading the tables

- **Native** means the backend has the operation and its adapter can call it.
- **nexo fills gap** means the backend exposes a lower-level primitive, but
  nexo must supply the capability's semantics (for example, polling screen
  text to infer agent state or starting `ssh` in a pane).
- **Unsupported** means the capability cannot be provided without violating
  the interface's contract. The feature must discover that the capability is
  absent rather than controlling a different backend.

The commands below assume the backend's control interface is enabled and
reachable. They are not a promise that a backend is installed.

## Capability matrix

| Interface | Zellij 0.44+ | WezTerm | Kitty |
| --- | --- | --- | --- |
| `ProjectOpener` | **Native** — `attach --create-background` and `action switch-session` provide a project container and switching. | **nexo fills gap** — `wezterm cli spawn --new-window --workspace <name> --cwd <dir>` creates a workspace in a new window; `--workspace` requires `--new-window` and applies only to that newly created window. The CLI cannot switch the GUI to an existing workspace, so selecting one needs a Lua-side/user action. | **Native** for opening a project tab — `@ launch --type=tab --cwd`. Kitty has no live daemon persistence, but explicit session files can recreate tabs and layouts after exit. |
| `PaneLister` | **Native** — `action list-panes --all --json`. | **Native** — `cli list --format json`. | **Native** — `@ ls`, including `foreground_processes`. |
| `ScreenReader` | **Native** — `action dump-screen` (targeted with `--pane-id`). | **Native** — `cli get-text`. | **Native** — `@ get-text`. |
| `PaneFocuser` | **Native** — `action focus-pane-id`. | **Native** — `cli activate-pane --pane-id`. This focuses a pane, not an arbitrary workspace. | **Native** — `@ focus-window --match id:N`. |
| `Popup` | **Native** — `run`/`new-pane --floating` provides a floating pane. | **Unsupported** — there is no popup/floating-pane CLI operation. | **Native** with a limitation — `@ launch --type=overlay` is an overlay rather than a persistent pane. |
| `AgentSource` | **nexo fills gap** — pane JSON has command/title data but no PID or agent working state. Combine pane listing and screen reads with nexo's agent detection. | **nexo fills gap** — `cli list --format json` does not expose a foreground process or agent state; use screen reads and nexo's detection. | **nexo fills gap** — `foreground_processes` makes process identification native, but working/stopped and seen-stopped state still needs nexo polling and screen inspection. |
| `Notifier` | **nexo fills gap** — `zellij pipe` can feed a status plugin, but the status bridge and notification semantics belong to nexo. | **nexo fills gap** — Lua `set_right_status` is a status surface, not a CLI notification API; a Lua bridge is required. | **nexo fills gap** — `tab_bar.py` can render status, but requires a nexo status bridge. |
| `RemoteProxy` | **nexo fills gap** — launch an `ssh` attach command in a native pane; Zellij has no remote-container primitive. | **nexo fills gap** — launch an `ssh` attach command with `cli spawn`; this does not solve the separate GUI-workspace switching limitation. | **nexo fills gap** — launch an `ssh` attach command in a tab; Kitty has no native remote-container primitive. |

No interface change is required. The gaps are implementation work behind the
existing small interfaces; the only truly absent operation is WezTerm's
`Popup`. In particular, no capability requires aggregating backends or
rebuilding a host-native feature.

## Backend facts checked

The following are the facts used by the matrix:

- **Zellij:** `action list-panes --all --json` lists pane metadata but does not
  provide a PID; `dump-screen`, `focus-pane-id`, floating panes, and session
  actions are documented. WASM plugins receive subscribed events, and
  `zellij pipe` sends messages to a plugin. Zellij's session state persists.
- **WezTerm:** `cli spawn --new-window --workspace <name> --cwd <dir>` creates
  a new workspace window; `--workspace` requires `--new-window` and applies
  only to that newly created window. `cli list` has a JSON format; `cli
  get-text` and `cli activate-pane` are documented CLI operations. Workspaces
  resemble sessions but are not an exact session equivalent, and the CLI has
  no GUI-workspace switch operation. Status is configured through Lua
  (`set_right_status`); there is no popup operation. CLI control is through a
  Unix domain socket.
- **Kitty:** remote control exposes `@ launch`, `@ ls`, `@ get-text`, and
  `@ focus-window`; `launch` supports tabs and overlays, and `ls` exposes
  foreground processes. Status is rendered by the Python tab-bar hook. Kitty
  has no live daemon persistence, while explicit session files can recreate
  tabs and layouts after exit.

## Versions and sources

Checked 2026-09-29 against the official documentation and release pages:

- **Zellij:** v0.44+ (the current release page showed v0.45.1). Sources:
  [CLI actions](https://zellij.dev/documentation/cli-actions),
  [CLI recipes](https://zellij.dev/documentation/cli-recipes.html),
  [plugin commands](https://zellij.dev/documentation/plugin-api-commands.html),
  [plugin events](https://zellij.dev/documentation/plugin-api-events.html),
  [v0.45.1 release](https://github.com/zellij-org/zellij/releases/tag/v0.45.1).
- **WezTerm:** the documented CLI operations are available in the current
  documentation; `get-text` requires 20230320-124340-559cb7b0 or newer and
  `activate-pane` requires 20230326-111934-3666303c or newer. Sources:
  [`spawn`](https://wezterm.org/cli/cli/spawn.html),
  [`list`](https://wezterm.org/cli/cli/list.html),
  [`get-text`](https://wezterm.org/cli/cli/get-text.html),
  [`activate-pane`](https://wezterm.org/cli/cli/activate-pane.html),
  [workspaces](https://wezterm.org/recipes/workspaces.html), and
  [Lua window events](https://wezterm.org/config/lua/window-events/).
- **Kitty:** v0.49.1. Sources:
  [remote control](https://sw.kovidgoyal.net/kitty/remote-control/),
  [launch](https://sw.kovidgoyal.net/kitty/launch/),
  [sessions](https://sw.kovidgoyal.net/kitty/sessions/),
  [tab-bar configuration](https://sw.kovidgoyal.net/kitty/conf/#tab-bar),
  [v0.49.1 release](https://github.com/kovidgoyal/kitty/releases/tag/v0.49.1).

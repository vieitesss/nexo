# nexo

nexo is a Go CLI and terminal UI that takes you to your projects, your
sessions on this and other machines, and the coding agents that need you. It
works on top of a terminal multiplexer the user picks: tmux and Herdr first,
more later.

## Language

### Backends

**Backend**:
The terminal multiplexer nexo drives in one invocation: tmux or Herdr in v1;
Zellij, WezTerm and Kitty are candidates, in that order.
_Avoid_: driver, provider

**Inner multiplexer**:
When multiplexers are nested, the one whose live pane or client owns nexo's
controlling TTY. Auto-detection selects it.

**Backend selection**:
The order that decides the backend: `--backend` flag, then the config
default, then auto-detection of the inner multiplexer. Exactly one backend is
active per invocation; nexo never controls one multiplexer from another.

**Capability**:
A small Go interface for one thing a backend can do (open a project
container, list panes, read a pane's screen, focus a pane, show a popup,
provide agent state, notify, proxy a remote session). A backend implements
only the capabilities it supports; features check for them.

**Native capability**:
A capability the multiplexer already provides itself (for example, Herdr's own
agent status). nexo uses a native capability whenever one exists and only
fills gaps with its own implementation.
_Avoid_: reimplementing native features

**Adapter**:
The thin, logic-free integration shipped per backend (a TPM entry file for
tmux, a plugin manifest for Herdr) that registers keys, popups and status
hooks and calls the `nexo` binary.

### Containers

**Container**:
The top-level multiplexer unit that holds a project: a tmux session or a Herdr
workspace.
_Avoid_: using "session" generically

**Pane**:
The smallest multiplexer unit nexo tracks. Agents and Git worktrees are
located through panes.

### Places

**Place**:
Anything the place picker can take you to: a project candidate, an open local
container, or a container on a remote machine.

**Place picker**:
The single picker that lists places and opens the chosen one.
_Avoid_: switcher, launcher

**Project candidate**:
A directory made available by configured-path expansion or home repository
discovery. Independent of the backend that will open it.

**Group**:
The configured parent directory under which project candidates are listed.

**Git context**:
The checkout identity (branch or short SHA), Git change summary (additions,
deletions, dirty marker) and cached upstream divergence (↑ahead ↓behind, from
local refs only, never fetched) shown for a repository.

**Machine**:
Any computer whose containers nexo can switch to, including the local one.
"SSH host" names only the alias used to reach a machine.
_Avoid_: host, server

**Proxy**:
A local container named `MACHINE/CONTAINER` whose only pane attaches to a
container on another machine. Closing or detaching a proxy never closes the
remote container.
_Avoid_: mirror, remote session (that is what a proxy attaches to)

### Agents

**Agent pane**:
A pane whose foreground process is an allowlisted coding-agent executable
(`pi`, `claude`, `codex`, `opencode`, `hermes`, `aider`, `cursor`, …).

**Working indicator**:
The live marker a harness paints only while it works (a braille spinner, a
live status line, a progress bar, a running hint). nexo's only positive
signal when it must detect agent state itself.

**Working**:
An agent pane showing its working indicator.
_Avoid_: busy, active

**Stopped**:
An agent pane with no working indicator for the idle threshold. Means "go
look"; waiting for input is treated the same.
_Avoid_: idle, done

**Unseen-stopped** / **Seen-stopped**:
A stopped agent whose exact pane you have / have not focused since it
stopped. Unseen-stopped agents are the ones that need you.

**Agent navigator**:
The picker that lists every agent pane, unseen-stopped first, and jumps to
the chosen one. Separate from the place picker because it answers "who needs
me?", not "where do I go?".
_Avoid_: popup, switcher

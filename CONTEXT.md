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
container, list containers, read a pane's screen, focus a pane, provide agent
state, notify, proxy a remote session). A backend implements only the
capabilities it supports; features check for them. Native popup surfaces are
manifest-owned: Herdr's popup integration lives in its plugin manifest, not
in a Go capability.

**Native capability**:
A capability the multiplexer already provides itself (for example, Herdr's own
agent status). nexo uses a native capability whenever one exists and only
fills gaps with its own implementation.
_Avoid_: reimplementing native features

**Adapter**:
The thin, logic-free integration shipped per backend (a TPM entry file for
tmux, a plugin manifest for Herdr) that registers popups and status hooks and
calls the `nexo` binary. Key bindings remain owned by the user.

### Containers

**Container**:
The top-level multiplexer unit that holds a project: a tmux session or a Herdr
workspace.
_Avoid_: using "session" generically

**Pane**:
The smallest multiplexer unit nexo tracks. Agents and Git worktrees are
located through panes.

### Lifecycle

**Lifecycle**:
The shared policy for opening a project container and creating a worktree:
path preflight, result defaults, and cleanup sequencing. Native command
construction, stable identity lookup, focus, and rollback primitives remain in
backend adapters.

### Places

**Place**:
Anything the TUI can take you to: a project candidate, an open local container,
or a container on a remote machine.

**TUI place**:
A project candidate or container row shown by the Containers or Projects tab.
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

**Remote kind**:
The multiplexer running on a configured remote machine: tmux or Herdr. A
listing detects and carries this value so an open operation can choose a native
Herdr path or a tmux proxy without probing the machine again.

**Proxy**:
A local container named `MACHINE/CONTAINER` whose only pane attaches to a
container on another machine. Closing or detaching a proxy never closes the
remote container.
_Avoid_: mirror, remote session (that is what a proxy attaches to)

**Proxy identity**:
The machine/container pair that identifies a proxy independently of its local
container label or native metadata. Machine matching is case-insensitive. The
container spelling follows the resolved remote multiplexer rather than the
local proxy adapter: a remote tmux container uses tmux's canonical
session-name spelling (`_` for `.` and `:`), while a native Herdr remote
container keeps its spelling.

**Place catalog**:
The backend-neutral module that combines project candidates, local and remote
container snapshots, machine identity, proxy merging, and agent decoration
into one coherent place snapshot. Callers consume its snapshot interface;
source refreshes replace their machine-owned data rather than layering stale
rows.

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

**Agent state ledger**:
The shared durable module that turns normalized backend observations into
agent state, owns stopped/seen transition timestamps and disappearance cleanup,
and persists records under the per-backend state lock. Backend adapters retain
native observation and notification mechanics.

**Agents tab**:
The TUI tab that lists every agent pane, unseen-stopped first, and jumps to
the chosen one. It answers "who needs me?" while the Containers and Projects
tabs answer "where do I go?".
_Avoid_: popup, switcher

**Agent display labels**:
Optional backend-neutral `container` and `window` labels shown by the Agents
tab. They are display values only; `target` remains opaque and is passed to the
selected backend for focus.

**Agent container identity**:
The stable backend-native identity used to associate an agent pane with its
container. It is distinct from the optional display labels.

### TUI

**TUI**:
The single terminal interface with Agents, Containers and Projects tabs. A
user chooses how to open it by binding keys in the multiplexer configuration.

**TUI tab**:
The initial view selected when the TUI opens: Agents, Containers or Projects.
Without an explicit tab, Agents is selected when an unseen-stopped agent
exists; otherwise Containers is selected.

**Containers tab**:
The one-line container view orders unseen-stopped agents first, then containers
by most recent backend activity. A backend without a native activity timestamp
uses deterministic container-name order within its activity bucket; Herdr is
currently such a backend.

### Distribution

**Install method**:
The machine-local method that owns the executable nexo is launched from:
Homebrew, Go install, or a release binary. Detection follows the actual
invocation path, and the executable referent behind a launch symlink is the
file upgrade replaces.

**Upgrade**:
The command that resolves an authenticated tagged release (or `main` with
`--nightly`) and installs it on the local machine and selected configured
machines. The requested revision is built on the local machine with its Go
toolchain, then copied and renamed beside the resolved executable, so a launch
symlink and the watcher keep working. A Homebrew-owned executable is refused
before any change because Homebrew cannot publish atomically under this
package's control. The tmux watcher re-executes its replaced binary; upgrade
never stops or signals the watcher.

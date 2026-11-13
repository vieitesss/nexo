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
| `session.name-template` | absent (legacy naming) | Optional generated container name; see [Session naming](#session-naming). |
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
| `clone.default-protocol` | `ssh` | Protocol selected when the clone form opens: `ssh` or `https`. |
| `clone.ssh-prefix` | `git@github.com:` | Prefix prepended to the repository path in the SSH form. |
| `clone.https-prefix` | `https://github.com/` | Prefix prepended to the repository path in the HTTPS form. |
| `remote.machines` | `[]` | SSH aliases to list and open. `local`, the local hostname, and aliases beginning with `-` are rejected. |
| `remote.backends` | `{}` | Optional per-machine backend table. Keys also configure machines, even when absent from `remote.machines`; values are `auto`, `tmux`, or `herdr`. Unlisted machines use `auto`. |
| `remote.ssh-config` | `""` | Optional SSH config path for listing and SSH attachment. A leading `~`, `~/...` or `$HOME` expands to the home directory; any other value is passed through as written; empty uses the normal SSH lookup. |
| `remote.transport` | `ssh` | Proxy attachment transport: `ssh`, `mosh`, or `et`. Listing always uses SSH. |
| `remote.reconnect-initial` | `1s` | Initial retry delay for SSH proxy attachments; must be positive and no greater than `reconnect-max`. |
| `remote.reconnect-max` | `30s` | Maximum retry delay for SSH proxy attachments; must be positive. |

## Session naming

Naming is opt-in; the complete example above intentionally leaves it unset.
To configure generated names for project opens and generated worktree labels:

```toml
[session]
name-template = "{org}/{team}/{repo}"
```

The only variables are `{org}`, `{team}`, and `{repo}`. Literals and repeated
or adjacent variables are supported; expressions and brace escaping are not.
Values are substituted once, not evaluated as commands or environment variables.

With `org=acme`, `repo=api`, and no team, separators immediately beside each
missing `{team}` are removed: a contiguous run of `/`, `-`, or `_` on its
left first, otherwise on its right. Other punctuation is unchanged.

| Template | Team `platform` | No team |
| --- | --- | --- |
| `{org}/{team}/{repo}` | `acme/platform/api` | `acme/api` |
| `{org}-{team}-{repo}` | `acme-platform-api` | `acme-api` |
| `{org}_{team}_{repo}` | `acme_platform_api` | `acme_api` |
| `{org}/{team}-{repo}` | `acme/platform-api` | `acme-api` |
| `{team}/{repo}` | `platform/api` | `api` |
| `task-{team}` | `task-platform` | `task` |
| `{team}` | `platform` | Open fails (empty name) |
| `literal--{org}/{repo}` | `literal--acme/api` | `literal--acme/api` |

### Where values come from

Only a repository marker at the selected project root counts; an enclosing
repository does not name a child project. Jujutsu (`.jj`) takes priority over
Git (`.git`, including worktree files), then Mercurial (`.hg`). Failed or
ambiguous discovery uses filesystem fallback, not another VCS reader.
Selected symlink paths remain the paths opened and used for fallback.

- **Git:** use `origin`'s fetch URL, or the sole remote if there is no
  `origin`. Multiple fetch URLs, missing/invalid URLs, or ambiguous remotes
  fall back. Push URLs, branch tracking, and an attractive `upstream` do not
  override this selection. Normal Git configuration, URL rewrites and trust
  checks apply.
- **Git-backed Jujutsu:** use documented `jj root` and `jj git root`, with
  `--ignore-working-copy`, color and paging disabled; read the backing Git
  directory with the same Git remote rules. No working-copy snapshot is made.
- **Mercurial:** use the single resolved `default` URL from native
  `hg paths default -Tjson`. Mercurial resolves `path://` aliases; multiple
  URLs, absent defaults or unsupported output fall back. `pushurl` and
  `default-push` are ignored.

HTTP(S), SSH, Git and SCP-style remote locations with two path components
provide `org/repo`; three provide `org/team/repo`. Git/Jujutsu remove one
terminal `.git`; Mercurial preserves extensions. Credentials, host, port,
query and fragment never become naming values. Local/file URLs, unknown
transports, deeper namespaces, dot components, empty components and unsafe
encoded delimiters/control characters fall back. Interpretation is positional,
not forge-specific: a self-hosted deployment prefix can look like an org.

Filesystem fallback uses the selected directory's immediate parent basename as
`org`, its basename as `repo`, and no `team`: `~/work/acme/api` becomes
`acme/api`. Org/repo always come together from one source. Missing tools,
ordinary directories and unsupported VCSs also use this fallback; no tools are
installed. Root edges fail if a variable required by the template is unavailable.

### Validation, timing and compatibility

An explicitly empty/whitespace-only template, unknown variable or unmatched
brace is a configuration error. An empty/whitespace-only rendered result fails
that open. Outer whitespace is trimmed; NUL, ASCII controls and names with no
visible content are rejected. Generated names replace `.` and `:` with `_`
after rendering; Unicode is retained. Rex truncates generated labels to 128
Unicode characters; other native validation and errors remain unchanged.
Native labels of adopted containers, including Herdr-created worktrees, remain
authoritative rather than being renamed to match the template.

Metadata discovery is local, read-only and noninteractive, with one **one-second
total deadline**, further bounded by the caller's context. A discovery timeout
falls back; caller cancellation stops the open instead of creating a container.
No fetch, authentication, trust bypass or network lookup is added. Installed
CLI extensions and user config still run normally; this is not an extension
sandbox. Naming errors do not include raw remote URLs or command output.

Leaving `name-template` absent preserves the existing naming and lookup path
exactly, without extra naming-metadata commands. Changing the template, remote
metadata or fallback directory name can create a differently named container
or match a different existing one on the next open. There is no migration,
old-name alias/probe, automatic rename, collision prompt, suffix or association
registry. Different templates and Rex truncation can collide under existing
lookup rules. This adds no flags, grouping, cloning, or branch/worktree support
for additional VCSs. See the [approved contract](designs/session-naming-contract.md)
for the full naming rules.

## Changes that need tmux init

The tmux watcher re-executes itself when its binary is atomically replaced, so
`nexo upgrade` does not restart or signal it. The watcher still reads
`[agents] highlight-color` and every `[glance]` key once when it starts. After
changing any of them, rerun `nexo tmux init` on each machine running the
watcher (and keep the same `--config-file` when one was used). Herdr needs no
watcher init. `[tui] theme` is the exception: a running TUI polls the config
and follows `dark`/`light` changes without a restart.

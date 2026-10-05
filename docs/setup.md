# Guided setup and manual reference

`nexo setup` is the supported first-run path. It integrates with the
multiplexer it finds: the one set by `--backend` or the `backend` key, the one
owning this pane, or the only one installed. It asks only when tmux and Herdr
are both installed and nothing chose between them. It does not write `backend`
to the configuration, so nexo keeps detecting the multiplexer at run time.
Setup then asks for project roots and agent notifications, and writes only
nexo-managed blocks. It can configure SSH aliases without opening a remote
shell.

```sh
nexo setup
nexo setup --check
nexo setup --dry-run --local-only
nexo setup --yes --machine devbox --machine gpu1
```

`--check` is read-only and prints the binary on the non-interactive SSH PATH,
version, UTF-8 locale, effective focus events and installed nexo focus hooks,
init line or plugin, watcher, and selected backend for every machine. A failed
machine does not prevent other machines from being probed, but setup exits
non-zero when any machine fails.

When a remote has a different OS or architecture and no nexo binary yet, setup
builds the running binary's tagged revision (or commit) with the local Go
toolchain before copying it. A development binary with no usable revision
refuses that remote instead of guessing; use a versioned release or commit
build when configuring a different platform.

With a non-TTY stdin setup never prompts. Pass `--yes` to accept defaults and
select all SSH aliases, or use `--local-only` / `--machine ALIAS` to make the
remote answer explicit. Add `--fix-locale` when a remote offers a UTF-8 locale
that setup should add to its startup file. `--dry-run` prints the complete
local and remote plan and does not install binaries, edit files, or run reload
commands. Re-running setup replaces its managed
block and reports unchanged files as `ok`.

## Managed files and backups

Each edited user file contains one block delimited by:

```text
# >>> nexo >>>
...
# <<< nexo <<<
```

Before the first edit nexo keeps a sibling `<file>.nexo.bak`; an existing backup
is never overwritten. Configuration is merged with the same atomic config
writer used by `clone --add-parent`. Existing TOML values, comments, and formatting outside changed value ranges are
retained; setup adds only the selected `paths`, `[agents].notify`,
`[remote].machines`, and detected `[remote.backends]` entries. New keys use
canonical TOML formatting (comments on existing values are not rewritten).
`config.toml` is intentionally not wrapped in a managed block: the parser
edits exact TOML value ranges and validates the complete result. Setup edits
these shapes: top-level keys (including dotted keys such as
`agents.notify`), ordinary `[agents]`, `[remote]`, and `[remote.backends]`
tables, and one-level inline tables such as `agents = { notify = true }`.
It clearly refuses nested inline tables and table-local dotted keys rather than
trying to rewrite arbitrary TOML; move those values to the table forms above
before running setup. Unknown TOML keys remain errors.

## Manual fallback

The following is useful when provisioning a machine before nexo itself is
available. Install nexo and make it visible to non-interactive shells first:

```sh
command -v nexo
# zsh reads this for SSH commands; bash users can use ~/.bashrc.
export PATH="$HOME/.local/bin:$PATH"
```

For tmux, the managed setup is equivalent to:

```tmux
set -g focus-events on
if-shell 'command -v nexo >/dev/null 2>&1' 'run-shell "nexo tmux init --backend=tmux"'
```

Then reload with `tmux source-file ~/.tmux.conf`. With TPM use
`set -g @plugin 'vieitesss/nexo'` instead of the `if-shell` line. Herdr users
install the plugin and reload its server configuration:

```sh
herdr plugin install vieitesss/nexo
herdr server reload-config
```

Keep key bindings opt-in. Either binding can be pressed directly or after
the prefix: setup reads `prefix+a` as "the prefix, then `a`" for both tmux
and Herdr, and any other key as pressed on its own. For tmux, `C-s` goes in
the root key table and `prefix+a` in the prefix table. A tmux key pressed on
its own needs `C-` or `M-` (F1–F12 excepted), so it never takes a plain `a`
or `Enter` from every pane. Herdr keys use Herdr's own spelling (`ctrl+s`,
`alt+g`, `f5`), and one pressed on its own that types a character needs
`ctrl+` or `alt+`. Setup rejects a key Herdr could not parse, since Herdr
would silently disable it. Never use the Ctrl-Space prefix itself. If you
add bindings, check for an existing binding first.

## Manual remote provisioning

When the wizard cannot be used, configure each SSH alias without an
interactive login. The command must see nexo on the non-interactive PATH:

```sh
ssh MACHINE 'command -v nexo'
ssh MACHINE 'locale charmap'
```

For zsh put the export in `~/.zshenv`; bash commonly uses `~/.bashrc` and
fish uses `~/.config/fish/config.fish`:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

A remote tmux backend needs focus events and the startup adapter in
`~/.tmux.conf`, then a reload:

```tmux
set -g focus-events on
if-shell 'command -v nexo >/dev/null 2>&1' 'run-shell "nexo tmux init --backend=tmux"'
```

```sh
ssh MACHINE 'tmux source-file ~/.tmux.conf'
ssh MACHINE 'nexo tmux init --backend=tmux'
```

A Herdr backend needs Herdr 0.9.0 or newer, the plugin, and a server reload:

```sh
ssh MACHINE 'herdr plugin install vieitesss/nexo'
ssh MACHINE 'herdr server reload-config'
```

If both backends run on one machine, persist the answer rather than relying on
a probe:

```toml
[remote.backends]
MACHINE = "tmux"
```

With `MACHINE = "auto"` and both servers running, `nexo setup` integrates both
instead: the tmux block and watcher, plus the Herdr block and plugin. The plan
and `--check` show `backend=auto (tmux+herdr)`, and a failed check names the
missing side. If Herdr cannot be integrated (for example it is older than
0.9.0), setup skips it with an action line and integrates tmux only. With one
server running, an `auto` machine gets that one (`backend=auto (tmux)`). Setup
never replaces an explicit `auto` in `[remote.backends]` with the kind it found.
Without any entry, setup asks when both servers are running.

A remote nexo config contains only `[agents]`; `[remote]` is local-machine
configuration. `docs/remote.md` covers proxy transport and remote bindings.

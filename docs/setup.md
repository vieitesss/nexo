# Guided setup and manual reference

`nexo setup` is the supported first-run path. It uses the multiplexer owning
this pane, asks for project roots and agent notifications, and writes only
nexo-managed blocks. It can configure SSH aliases without opening a remote
shell.

```sh
nexo setup
nexo setup --check
nexo setup --dry-run --local-only
nexo setup --yes --machine devbox --machine gpu1
```

`--check` is read-only and prints the binary on the non-interactive SSH PATH,
version, UTF-8 locale, focus hooks, init line or plugin, watcher, and selected
backend for every machine. A failed machine does not prevent other machines
from being probed, but setup exits non-zero when any machine fails.

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
edits exact TOML value ranges and validates the complete result. Unknown TOML
keys remain errors.

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

Keep key bindings opt-in. If you add the README examples, check for an
existing binding first. Remote machines need a UTF-8 locale and the same
multiplexer setup; `nexo setup --check` is the preferred verification.

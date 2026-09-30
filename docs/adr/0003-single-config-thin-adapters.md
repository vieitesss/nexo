# One TOML config, thin adapters

All user configuration lives in `~/.config/nexo/config.toml`, independent of
the backend, so switching multiplexers needs no reconfiguration. Each backend
ships a thin adapter (a TPM entry file for tmux, a `herdr-plugin.toml` for
Herdr) that only registers keys, popups and status hooks and calls the `nexo`
binary. Adapters contain no logic.

> Superseded in part by ADR 0005 (key bindings).

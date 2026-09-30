# One TOML config, thin adapters

All user configuration lives in `~/.config/nexo/config.toml`, independent of
the backend, so switching multiplexers needs no reconfiguration. Each backend
ships a thin adapter (a TPM entry file for tmux, a `herdr-plugin.toml` for
Herdr) that registers popups and status hooks and calls the `nexo` binary.
Users own key bindings; adapters contain no application logic.

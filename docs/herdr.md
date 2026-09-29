# Herdr adapter

The repository ships `herdr-plugin.toml`, a thin Herdr adapter with no
application logic. It declares two Herdr plugin actions and two plugin panes;
the panes run nexo's existing commands in Herdr's native popup surface:

- `nexo.place-picker` opens `nexo --backend=herdr`.
- `nexo.agent-navigator` opens `nexo --backend=herdr agents pick`.

The manifest uses only the Herdr 0.9.0 plugin fields documented at
[herdr.dev/docs/plugins](https://herdr.dev/docs/plugins/). It has no `[[events]]`
hooks: nexo reads Herdr's native agent state directly, and Herdr owns its own
agent notifications, so no event hook is needed.

## Install or link

For a GitHub release, install the plugin with the repository's GitHub shorthand
(the optional path is needed when the manifest is in a subdirectory):

```sh
herdr plugin install OWNER/REPOSITORY[/SUBDIRECTORY]
```

When developing from a checkout, link that checkout instead:

```sh
herdr plugin link /path/to/nexo
```

`herdr plugin install` and `herdr plugin link` register the manifest; neither
command adds keybindings to the user's Herdr configuration. The plugin
manifest schema has actions and panes, but no keybinding table. Add the
following documented `plugin_action` commands to
`~/.config/herdr/config.toml`, under the existing `[keys]` configuration (the
same `[[keys.command]]` format shown by `herdr --default-config` and in
[Herdr's configuration guide](https://herdr.dev/docs/configuration/#custom-command-keybindings):

```toml
[[keys.command]]
key = "ctrl+s"
type = "plugin_action"
command = "nexo.place-picker"
description = "open nexo place picker"

[[keys.command]]
key = "prefix+a"
type = "plugin_action"
command = "nexo.agent-navigator"
description = "open nexo agent navigator"
```

`ctrl+s` is the no-prefix place-picker binding used by the tmux adapter; the
Herdr default configuration reserves `prefix+s` for Settings, so the direct
binding avoids that clash. `prefix+a` is free in Herdr's default keymap and
matches the tmux agent-navigator binding.

Reload the running Herdr server after editing its configuration:

```sh
herdr server reload-config
```

The two actions can also be invoked directly for a smoke test:

```sh
herdr plugin action invoke nexo.place-picker
herdr plugin action invoke nexo.agent-navigator
```

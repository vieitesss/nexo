# Herdr adapter

The repository ships `herdr-plugin.toml`, a thin Herdr adapter with four
entries for nexo's TUI. Each entry opens a 100% x 100% native popup in the
current Herdr pane:

- `nexo.tui` opens the default tab (Agents when an agent needs you,
  Containers otherwise).
- `nexo.tui-agents` opens Agents.
- `nexo.tui-containers` opens Containers.
- `nexo.tui-projects` opens Projects.

The manifest uses only the Herdr 0.9.0 plugin fields documented at
[herdr.dev/docs/plugins](https://herdr.dev/docs/plugins/). It has no `[[events]]`
hooks: nexo reads Herdr's native agent state directly, and Herdr owns its own
agent notifications.

## Install or link

Install the `nexo` binary first and make sure it is on `PATH`; the plugin only
registers the manifest and invokes the existing `nexo` executable. See
[docs/release.md](release.md) for nexo installation options.

For the GitHub repository, install the plugin with:

```sh
herdr plugin install vieitesss/nexo
```

When developing from this repository's checkout, link it instead:

```sh
herdr plugin link ~/personal/nexo
```

`herdr plugin install` and `herdr plugin link` register the manifest; neither
command adds keybindings to the user's Herdr configuration. Add the following
`plugin_action` commands to `~/.config/herdr/config.toml`, under the existing
`[keys]` configuration (the same `[[keys.command]]` format shown by
`herdr --default-config` and in [Herdr's configuration guide](https://herdr.dev/docs/configuration/#custom-command-keybindings)):

```toml
[[keys.command]]
key = "ctrl+s"
type = "plugin_action"
command = "nexo.tui"
description = "open nexo TUI"

[[keys.command]]
key = "prefix+a"
type = "plugin_action"
command = "nexo.tui-agents"
description = "open nexo agents"

[[keys.command]]
key = "prefix+c"
type = "plugin_action"
command = "nexo.tui-containers"
description = "open nexo containers"

[[keys.command]]
key = "prefix+p"
type = "plugin_action"
command = "nexo.tui-projects"
description = "open nexo projects"
```

Keys remain entirely under the user's control. Reload the configuration after
editing it:

```sh
herdr server reload-config
```

The actions can also be invoked directly for a smoke test:

```sh
herdr plugin action invoke nexo.tui
herdr plugin action invoke nexo.tui-agents
```

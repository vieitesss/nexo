# Startup measurements

Normal invocations are silent. Set `NEXO_STARTUP_TRACE=1` to enable
microsecond timing markers. The trace is disabled by default and has
negligible cost. For the TUI, markers are buffered until the terminal is
restored, so they never write into the running screen. The trace covers
process setup and the TUI's first frame, first rows per tab, project,
container, agent, Git, and configured-machine sources. `list --json` and
`open` also mark their discovery and backend stages.

Markers are tab-separated records:

```
nexo-startup\t<invocation>\t<event>\t<elapsed-microseconds>µs
```

`start`, `list-start`, and `command-start` are invocation setup markers;
`places-start` identifies the combined place listing. `first-frame` is the
first sized `Model.frame` construction; it occurs before Bubble Tea's output
writer receives that frame. `first-rows-agents`, `first-rows-containers`, and
`first-rows-projects` mean that the corresponding projection has at least one
loaded item or group, whether or not that tab is currently visible. Source
markers (`projects-start/done`, `containers-start/done`, `agents-start/done`,
`remote-start/done`, and `git-start/done`) bracket their worker calls. The
markers are diagnostic measurements, not an output-boundary measurement.

The adaptive `[tui] theme = "auto"` (the default) asks the controlling terminal
for its background over OSC 11. That probe runs synchronously before Bubble Tea
starts, while the terminal still has a single reader, so it can never consume
keys or terminal responses the program is expecting. It does delay the first
frame by one terminal round trip. On this Pi, measured in a pane of an isolated
`tmux -L` server whose terminal answers OSC 11, the probe adds a median of about
0.4 ms to the first frame (auto 1.2 ms versus 0.5 ms for an explicit theme;
the pre-program probe phase is 0.56 ms versus 0.16 ms). When the terminal never
answers, the probe falls back to tmux's client theme (inside tmux only), then
`COLORFGBG`, then dark, which costs about 2-3 ms for the client-theme query. Set
`[tui] theme = "light"` or `[tui] theme = "dark"` to skip the probe entirely.

For repeatable local measurements, use a throwaway multiplexer server and a
wrapper in `PATH` that forwards commands to that server. A fake command runner
can supply responses for a remote backend without contacting a real machine.
Keep the environment markers for live multiplexer sessions unset when running
the harness.

Example for a non-interactive listing:

```sh
NEXO_STARTUP_TRACE=1 nexo list --json >/tmp/places.json
```

The trace is diagnostic only; it does not change source ordering or output
formats. In the TUI, project-discovery warnings appear as a status toast rather
than being written to the active terminal.

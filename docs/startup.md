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

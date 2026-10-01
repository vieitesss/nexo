# Startup measurements

Normal invocations are silent. Set `NEXO_STARTUP_TRACE=1` to emit
microsecond timing markers to stderr. The trace covers process setup and the
TUI's first visible frame, first rows per tab, terminal theme probe, project,
container, agent, Git, and configured-machine sources. `list --json` and
`open` also mark their discovery and backend stages.

For repeatable local measurements, use a throwaway multiplexer server and a
wrapper in `PATH` that forwards commands to that server. A fake command runner
can supply responses for a Herdr-shaped backend without contacting a real
machine. Keep the environment markers for live multiplexer sessions unset when
running the harness.

Example for a non-interactive listing:

```sh
NEXO_STARTUP_TRACE=1 nexo list --json >/tmp/places.json
```

The trace is diagnostic only; it does not change source ordering or output
formats.

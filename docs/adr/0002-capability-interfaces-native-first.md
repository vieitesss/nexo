# Capability interfaces, native first

Backends are described by small Go capability interfaces (`ProjectOpener`,
`PaneLister`, `ScreenReader`, `PaneFocuser`, `Popup`, `AgentSource`,
`Notifier`, `RemoteProxy`, …) rather than one large interface returning
`ErrUnsupported`. Features discover capabilities with type assertions.

When a multiplexer natively provides a capability, nexo uses it instead of its
own implementation. Example: on Herdr, `AgentSource` reads Herdr's agent
status and events; on tmux, it is nexo's screen-scraping poller. nexo's own
implementations exist to fill gaps, not to replace the host.

Considered: one big interface (hides what a backend really supports) and
per-backend features (duplicates logic, makes new backends expensive).

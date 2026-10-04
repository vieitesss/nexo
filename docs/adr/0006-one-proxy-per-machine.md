# One proxy per machine

- Status: accepted
- Date: 2026-02-21

## Decision

A nexo proxy is a local tmux session or Herdr workspace per configured machine,
not per remote container. Its `@nexo-proxy`/`nexo-proxy` metadata is the
machine name only; the local session/workspace label is derived from the
machine and may be suffixed (for example, `machine~remote`). A local
container whose label happens to match a machine is never adopted as a proxy.
Only the tag identifies a proxy: untagged sessions are never proxies. Legacy
tagged `MACHINE/CONTAINER` metadata is left alone and readers use its machine;
it is not migrated.

A machine proxy's tmux pane resolves an attachment target on the remote at
attach time using `session_last_attached`, sorted numerically, rather than
`session_activity`. `session_activity` can be advanced by a chatty pane and
would not represent the session most recently focused by a terminal. The
resolution runs inside `sh -c`, so SSH, mosh, and ET use the same behavior.
Reconnections repeat this unpinned resolution.

When an explicit open requests a container, creation uses one initial pinned
attach. If the machine proxy already exists, nexo switches its remote tmux
client through a backend-specific rendezvous key. The attachment shell writes
its remote tty (not its process ID) to the rendezvous file; switching resolves
that tty with `tmux list-clients -F '#{client_tty} #{client_name}'`. A missing
remote target is reported without restarting a healthy proxy. A missing or
stale client falls back to tmux `respawn-pane`; Herdr closes and recreates the
workspace. remote previous never retargets an existing proxy.

An empty remote is a valid proxy state: the pane stays alive and waits for `r`
to retry or `q` to close. It never creates a remote session implicitly.

## Consequences

- Repeated remote previous switches local focus only, preserving the remote session shown.
- Remote sessions removed after being written to history cannot detach a local
  client, because history selects machines and attachment resolves live.
- Explicit opens remain fluid without creating duplicate machine proxies.
- Herdr machine proxies carry `NEXO_PROXY_MACHINE` only; there is no pinned
  `NEXO_PROXY_CONTAINER`.
- Readers retain machine extraction for legacy `MACHINE/CONTAINER` metadata,
  while newly recorded proxy history is `{Machine, Proxy: true}` with no name.

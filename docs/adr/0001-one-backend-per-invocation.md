# One backend per invocation

nexo drives exactly one backend per invocation, chosen by `--backend`, then
the config default, then auto-detection of the inner multiplexer. It never
aggregates across multiplexers or controls one from another. Cross-backend
views (for example, agents in tmux and Herdr in one list) stay out of scope
until there is a concrete need; the capability model does not preclude them.

v1 backends are tmux and Herdr. The capability interfaces are checked on paper
against Zellij, WezTerm and Kitty (in that order of priority) so that adding
them is additive.

## Bridged remotes

One case reaches across the boundary: a local backend that owns a proxy
container, whose only pane attaches to a *different* remote multiplexer. That
is not aggregation and not control of one multiplexer from another: the local
backend still owns the container, and the remote multiplexer is driven by its
own client over the same SSH transport. nexo never drives Herdr with tmux
commands or the reverse, and needs neither a local Herdr install nor a nested
client.

The proxy seam carries the remote kind
(`capabilities.RemoteProxy.Proxy(ctx, remoteKind, machine, container)`), so one
method covers both pairs: a backend attaches to its own kind natively and runs
the other kind's client in the pane it already owns. A backend that cannot
bridge a kind rejects it instead of guessing, and the native pairs keep their
existing paths:

| Local backend | Remote kind | Path |
| --- | --- | --- |
| tmux | tmux | local tmux proxy (unchanged) |
| Herdr | tmux | local Herdr workspace proxy (unchanged) |
| Herdr | Herdr | native `herdr machine add` (unchanged) |
| tmux | Herdr | local tmux proxy whose pane runs the remote Herdr client |

A local Herdr with a remote Herdr must keep the native path; the bridge is for
local backends that cannot hand the machine to Herdr's own sidebar.

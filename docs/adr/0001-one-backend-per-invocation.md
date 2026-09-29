# One backend per invocation

nexo drives exactly one backend per invocation, chosen by `--backend`, then
the config default, then auto-detection of the inner multiplexer. It never
aggregates across multiplexers or controls one from another. Cross-backend
views (for example, agents in tmux and Herdr in one list) stay out of scope
until there is a concrete need; the capability model does not preclude them.

v1 backends are tmux and Herdr. The capability interfaces are checked on paper
against Zellij, WezTerm and Kitty (in that order of priority) so that adding
them is additive.

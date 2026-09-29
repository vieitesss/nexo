# Two pickers, built with Bubble Tea

nexo is one binary with two pickers: the place picker (project candidates,
local containers, remote containers) and the agent navigator (ranked by
urgency, not location). Subcommands expose the same operations for scripting,
with `--json` output.

Both pickers are built with Bubble Tea, Lip Gloss and Bubbles, with nexo's own
fuzzy ranking. No `fzf` dependency: the pickers need folded groups, a tree
spine, in-picker dialogs and colors derived from the terminal palette.

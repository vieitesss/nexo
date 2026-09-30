# One TUI with tabs; keys are bound by the user

- Status: accepted
- Date: 2026-02-20

## Decision

Expose one `nexo tui [agents|containers|projects]` entry point. With no tab,
open Agents when an unseen-stopped agent exists; otherwise open Containers.
The TUI owns navigation between tabs, while the command's optional tab chooses
the initial view.

nexo does not install multiplexer key bindings. Users bind the command in their
mux configuration, including popup geometry and border choices. Backend
adapters provide popup entry points and native status integration only.

The Herdr adapter provides one action and popup pane for the default TUI and
one for each explicit tab. The tmux adapter retains hooks and the agent
watcher, and removes bindings previously recorded by nexo only when their
current command is byte-identical to the ownership record.

## Supersedes

This supersedes the two-picker entry-point portion of ADR 0004 and the
adapter-owned key registration portion of ADR 0003. Bubble Tea and nexo's
fuzzy ranking remain the TUI implementation choices from ADR 0004.

## Consequences

- Users can choose keys and popup geometry without nexo configuration.
- Existing configuration files continue to load, with a migration warning for
  removed tmux key and popup options.
- `nexo agents` remains the scriptable listing and watcher command; its picker
  entry point is replaced by the TUI's Agents tab.

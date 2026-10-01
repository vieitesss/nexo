# Terminal interface

The terminal interface renders aligned, one-line rows in each tab. The selected
row keeps its identity while the terminal is resized or asynchronous data is
refreshed.

## Agents and containers

Agents and containers are grouped by machine. The local machine appears first,
followed by remote machines; each machine header is bold with a muted agent or
container count. A machine header has no action, so it is not selectable:
keyboard navigation and clicks skip it, and an empty, loading, or failed
section simply leaves nothing selected. Agents stay attention-ordered within
their machine, and a remote header shows loading, warning, or error status
while discovery runs.
Container rows show the container name followed by one coloured state dot per
agent. They do not repeat harness names, state words, ages, activity ages, or
proxy labels. Agent rows show the container name without a machine prefix.

## Projects

Projects are grouped by their containing folder. On an empty query every folder
and every checkout with linked worktrees starts folded. A folder header is
selectable because it folds; Left and Right act on it. Right unfolds the
selected folder or checkout. Left folds the selected row when it is expanded —
a folder header or a checkout with worktrees — and keeps the selection there;
otherwise it folds the parent and moves the selection there, so a worktree
folds its checkout, a folded checkout folds its folder, and a peer project
folds the same folder. Left on a folded folder does nothing. A folded checkout
keeps a subtle count of its hidden worktree children. Search does not change
the saved fold state.

## Search

Printable input performs one flat fuzzy search. The first non-empty query selects
the best match. Refining a query keeps the selected project, container, or agent
when it still matches; otherwise the best remaining match is selected. Project
search ranks name matches before branch matches, then visible folder matches.
Equal scores are ordered by shorter name, checkout before linked worktree, then
name.

The Projects tab uses natural column widths whenever the full row fits. When a
terminal is too narrow, the name column is capped proportionally before lower
priority columns are reduced or removed. In search results the containing folder
is shown to the left of the name as a path (`~/work/prefapp/gitops-k8s`),
right-aligned and ending in `/` so names stay aligned. The branch and its Git
summary (`⎇ main  +12 −3 * ↑1 ↓2`) share the column after the name; the branch
truncates before the summary. The folder truncates from the left with `…` and is
dropped before the name loses characters. Matched characters in names, branches,
and visible folders are highlighted independently within their cells.

## Keys

See the picker key table in the README for the complete list. Left/Right only
fold project rows and project folders; they do not fold machine groups in the
Agents or Containers tabs. In the search row, Ctrl-W deletes back to whitespace, Alt-Backspace
deletes back to the previous alphanumeric word boundary, and Ctrl-U clears the
query.

## Mouse

Mouse support is enabled for tab and row clicks, folding project headers,
opening a selected row, and wheel navigation. A checkout's chevron folds it;
clicking elsewhere selects it, and a second click opens it. Clicks and wheel
events are ignored while the kill confirmation or inline rename footer is
active. Shift-drag still selects text in most terminals; hold Shift when you
need the terminal's normal text selection.

Kill and rename run without leaving the interface. Each action reports success
or its error in a toast, then refreshes the local and remote rows. A successful
kill clears marks and keeps the selection by container identity when possible.

## Colours

The brand colours come from the icon in `assets/logo.svg`. The lime accent
(`#B8F15A`), the foreground (`#E8EAED`) and the muted grey (`#8A919E`) are
adaptive: on a light terminal they switch to an olive accent (`#4E7A00`), a
darker grey (`#5F6774`) and a near-black foreground (`#1F2329`) so the text
stays readable. The icon also defines a
ground (`#0F1115`), a tile (`#171A21`) and a tile border (`#2A2F3A`); the
interface paints no background, so those are not used on screen.

Semantic colours stay as terminal ANSI codes and follow the user's theme:
agent state is red, yellow or green, Git additions and deletions are bright
green and bright red, and errors are red. The selected row is the only painted
background — one blue bar with a single foreground. Bold names and headers keep
their weight on the selected row, and agent state dots, state words and stopped
ages keep exactly the same terminal ANSI colour they have when unselected.

The `[tui]` `theme` setting controls how adaptive colours are selected. The
default `"auto"` queries OSC 11 on the controlling terminal (including inside
tmux), then falls back to `COLORFGBG` and finally dark mode when no answer is
available. tmux versions older than 3.4 may not answer when the pane has its
default background; set `theme = "light"` in that case. Set it to `"dark"` or
`"light"` to force a variant. Semantic ANSI colours continue to follow the
terminal in every mode.

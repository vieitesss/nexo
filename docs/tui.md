# Terminal interface

The terminal interface renders aligned, one-line rows in each tab. The selected
row keeps its identity while the terminal is resized or asynchronous data is
refreshed.

## Projects

Projects are grouped by their containing folder. On an empty query every folder
and every checkout with linked worktrees starts folded. Right unfolds the
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
Containers tab. In the search row, Ctrl-W deletes back to whitespace, Alt-Backspace
deletes back to the previous alphanumeric word boundary, and Ctrl-U clears the
query.

## Mouse

Mouse support is enabled for tab and row clicks, folding project headers,
opening a selected row, and wheel navigation. A checkout's chevron folds it;
clicking elsewhere selects it, and a second click opens it. Clicks and wheel
events are ignored while the kill confirmation or inline rename footer is
active. Shift-drag still selects text in most terminals; hold Shift when you
need the terminal's normal text selection.

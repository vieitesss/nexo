# Terminal interface

The terminal interface renders aligned, one-line rows in each tab. The selected
row keeps its identity while the terminal is resized or asynchronous data is
refreshed.

## Projects

Projects are grouped by their containing folder. Folder headers, checkout
projects, and linked worktree children start expanded. A checkout with linked
worktrees can be folded with Left and unfolded with Right; the same keys fold or
unfold a containing folder. A folded checkout remains the selected row and shows
a subtle count of its hidden worktree children. Search does not change the saved
fold state.

## Search

Printable input performs one flat fuzzy search. The first non-empty query selects
the best match. Refining a query keeps the selected project, container, or agent
when it still matches; otherwise the best remaining match is selected. Project
search ranks name matches before branch matches, then visible folder matches.
Equal scores are ordered by shorter name, checkout before linked worktree, then
name.

The Projects tab uses natural column widths whenever the full row fits. When a
terminal is too narrow, the name column is capped proportionally before lower
priority columns are reduced or removed. Matched characters in names, branches,
and visible folders are highlighted independently within their cells.

## Keys

See the picker key table in the README for the complete list. Left/Right only
fold project rows and project folders; they do not fold machine groups in the
Containers tab.

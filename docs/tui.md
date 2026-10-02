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
proxy labels. Agent rows lead with the bold container name without a machine
prefix, followed by the muted harness name.

Every remote machine whose listing succeeded ends its section with a selectable
`+ new container…` row. The row carries the multiplexer kind the listing
resolved, so choosing it never probes the machine again. `Enter` creates the
container named after the current search query; an empty query opens the
staged name form as an inline prompt instead. Creation runs the shared remote
`new` flow and focuses the created container. A machine that is still loading,
a machine whose listing failed, and the local machine have no such row.

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

Project rows carry a short-lived local Git context: the branch name, or the
short commit SHA when the checkout is detached; additions, deletions, and a
dirty marker; and ahead/behind divergence read from local refs only. nexo never
fetches to build it.

## Search

Printable input performs one flat fuzzy search. The first non-empty query selects
the best match. Refining a query keeps the selected project, container, or agent
when it still matches; otherwise the best remaining match is selected. Agent
search matches harness, container, machine, target, and branch. Project
search ranks name matches before branch matches, then visible folder matches.
Equal scores are ordered by shorter name, checkout before linked worktree, then
name.

The Projects tab uses natural column widths whenever the full row fits. When a
terminal is too narrow, the name column is capped proportionally before lower
priority columns are reduced or removed. In search results the containing folder
is shown to the left of the name as a path (`~/work/prefapp/gitops-k8s`),
right-aligned and ending in `/` so names stay aligned. The branch and its Git
summary (`⎇ main  #12  +12 −3 * ↑1 ↓2`) share the column after the name; the
branch truncates before the summary and the pull request badge. The folder
truncates from the left with `…` and is dropped before the name loses
characters. Matched characters in names, branches, and visible folders are
highlighted independently within their cells.

## Pull request state

The Projects tab shows `#123` beside the branch when the checkout's upstream
branch has a pull request: green for open, magenta for merged, and red for
closed. The badge loads asynchronously after the local Git context and never
delays the first frame, a search keystroke, or a refresh. The badge appears
only when `gh` is on `PATH`, the branch tracks an upstream whose remote is on
github.com, and the request's head owner and branch match that upstream; a
branch without an upstream shows nothing instead of guessing `origin`. No
request, a fork-owned or mismatched head, missing authentication, a network
failure, and a missing `gh` all leave the row unchanged and show no error.

Answers, the absence of a request included, are cached for 30 seconds. While
the TUI is open it checks the project rows around the cursor every 5 seconds
and asks `gh` again only for a checkout whose cached answer expired, so a
merged request or a request created after the first look appears within about
half a minute without one query per row per tick. The selected row keeps the
badge's state colour; only the selection background is painted, never the
badge.

The colours are terminal ANSI palette entries with a light and a dark variant,
so the terminal's own palette decides the exact contrast. Against the
conventional xterm palette both variants reach the WCAG AA 4.5:1 ratio on the
row background and on the TUI's selection background.

## Project actions

Press `Ctrl-O` on a selected project row to open the clone form without leaving the
Projects tab. The tabbed view remains visible behind the border-only modal. Its
source stage has editable protocol and repository fields; the destination stage
validates a new path before Git runs. Enter advances from any field and submits
from the destination stage, while Escape cancels. Completed stages collapse to a
summary and can be reopened with Shift-Tab; a failed source or destination remains
inline. Cloning refreshes the project catalog and selects the new project without
opening or focusing a container. The configured `[clone]` section supplies SSH
and HTTPS prefixes.

Press `Ctrl-D` in the Projects tab to create an empty directory. The directory
form reuses the clone form's stage, inline errors, `Enter`/`Escape` behaviour
and add-parent offer, but runs no Git command and creates missing parents.
Creation never opens or focuses a container; on success the catalog refreshes
and selects the new directory.

Press `Ctrl-E` on a selected Git checkout (including a linked worktree) to open the
branch-extraction form. The destination starts with a sibling-path suggestion and
the return-branch stage starts with the same previous-branch/default-branch
suggestion used by the CLI; both remain editable. Enter advances through the
stages and starts the asynchronous extraction from the return-branch stage.
Dirty checkouts, existing destinations, branch ownership conflicts, and the lack
of an eligible return branch remain inline in the form. Escape cancels, including
an in-flight extraction, and late results from a canceled action are ignored. A
successful extraction refreshes the catalog, selects the new worktree, and opens
and focuses it according to the selected backend's worktree policy.

Press `Ctrl-Y` on a selected project row to open the delete preflight. The
border-only staged form keeps the exact lexical path (including a symlink),
shows that warnings are overridable but blockers are not, and offers OS trash
by default or an explicit permanent mode. The `accept warnings` toggle is the
TUI equivalent of `--force`; Git worktrees always use non-force `git worktree
remove`. Escape cancels, and Enter submits from any field. A successful delete
refreshes the Projects catalog and keeps selection near the removed row.

## Keys

| Key | Behavior |
| --- | --- |
| `Tab` / `Shift-Tab` | Switch tabs |
| `F1` / `F2` / `F3` | Jump to Agents / Containers / Projects |
| `↑` / `↓`, `Ctrl-N` / `Ctrl-P` | Move |
| `Enter` | Open the selected item, focus the selected agent, or name a new remote container |
| `←` / `→` | Fold the selected folder or checkout, step Left to its parent, or unfold with Right |
| `Ctrl-T` | Mark the selected container |
| `Ctrl-O` | Open the clone form for the selected Projects row |
| `Ctrl-D` | Open the new-directory form in the Projects tab |
| `Ctrl-E` | Open the branch-extraction form for the selected Git checkout or worktree |
| `Ctrl-Y` | Open the delete preflight and confirmation form for the selected Projects row |
| `Ctrl-X` | Kill marked or selected containers after confirmation |
| `Ctrl-R` | Rename the selected container |
| `Esc` / `Ctrl-C` | Clear search, then close / close |
| Printable keys, `Backspace` / `Delete` | Fuzzy search, erase search |

`Ctrl-Y` opens project deletion; it is intentionally separate from container
kill (`Ctrl-X`). Left/Right only fold project rows and project folders; they do
not fold machine groups in the Agents or Containers tabs. In the search row,
Ctrl-W deletes back to whitespace, Alt-Backspace deletes back to the previous
alphanumeric word boundary, and Ctrl-U clears the query.

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
adaptive: on a light terminal they switch to a brighter lime-olive accent
(`#669900`), a darker grey (`#5F6774`) and a near-black foreground (`#1F2329`)
so the text stays readable while the accent stays close to the lime. The icon
also defines a
ground (`#0F1115`), a tile (`#171A21`) and a tile border (`#2A2F3A`); the
interface paints no background, so those are not used on screen.

Semantic colours stay as terminal ANSI codes and follow the user's theme:
agent state is red (unseen stopped) or yellow (working), and a seen stopped
agent is green, following the same state hierarchy as the glance row's dots.
Git additions and deletions are bright green and bright red, and errors are
red. The selected row is the only painted
background — one blue bar with a single foreground. Modal forms use a border/frame
but do not paint their body, so the selected-row rule remains intact. Bold names
and headers keep
their weight on the selected row, and agent state dots, state words and stopped
ages keep exactly the same terminal ANSI colour they have when unselected.

The `[tui]` `theme` setting controls how adaptive colours are selected. The
default `"auto"` queries OSC 11 on the controlling terminal (including inside
tmux), then falls back to `COLORFGBG` and finally dark mode when no answer is
available. tmux versions older than 3.4 may not answer when the pane has its
default background; set `theme = "light"` in that case. Set it to `"dark"` or
`"light"` to force a variant. Semantic ANSI colours continue to follow the
terminal in every mode.

A running interface follows `[tui].theme` without a restart. It polls the
config file about once a second and recolours when the file changes, whether it
was rewritten in place or replaced with `mv`. This is what lets a remote
appearance script rewrite `theme = "light"|"dark"` over SSH and have an
already-open interface pick it up. Only the theme is reapplied; `paths`,
`remote.*` and the other keys keep the values loaded at startup.

`auto` is not re-probed mid-session: the OSC 11 probe is only safe before the
interface takes over the terminal, so switching to `"auto"` keeps the current
variant. A file that is missing, invalid, or not valid TOML keeps the current
palette and never interrupts the interface; at most it shows one status line
per distinct bad value.

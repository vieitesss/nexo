package tui

// Rendering is the presentation module behind View. Its interface is a sized
// frame; layout, ANSI selection handling, and row styling stay behind that seam
// so width fixes have locality and every tab benefits from the same policy.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

type renderFrame struct {
	width, height     int
	sized             bool
	tab               Tab
	query             []rune
	rows              []row
	cursor            string
	marked            map[picker.ContainerRef]bool
	markedTargetsList []picker.ContainerRef
	now               func() time.Time
	toast             string
	refreshErr        error
	gitErrors         map[string]string
	projection        rowProjection
	confirmKill       bool
	renameMode        bool
	renameInput       []rune
}

func (m Model) frame() renderFrame {
	projection := m.projection()
	if m.trace != nil && m.sized && m.width > 0 && m.height > 0 {
		m.trace.MarkOnce("first-frame")
		for _, item := range []struct {
			name string
			tab  Tab
		}{{"agents", AgentsTab}, {"containers", ContainersTab}, {"projects", ProjectsTab}} {
			if hasLoadedRows(projection.Rows(item.tab)) {
				m.trace.MarkOnce("first-rows-" + item.name)
			}
		}
	}
	return renderFrame{
		width: m.width, height: m.height, sized: m.sized, tab: m.input.tab,
		query: append([]rune(nil), m.input.query...), rows: m.rows,
		cursor: m.input.cursor, marked: m.input.marked,
		markedTargetsList: projection.markedTargets(m.input.marked), now: m.now,
		toast: m.toast, refreshErr: m.source.refreshErr, gitErrors: m.gitErrors,
		projection: projection, confirmKill: m.input.confirmKill,
		renameMode: m.input.renameMode, renameInput: append([]rune(nil), m.input.renameInput...),
	}
}

func (m Model) View() string { return m.frame().view() }

func (m renderFrame) currentRow() (row, bool) {
	for _, candidate := range m.rows {
		if candidate.id == m.cursor && candidate.cursorable {
			return candidate, true
		}
	}
	return row{}, false
}

func (m renderFrame) currentItem() (row, bool) {
	value, ok := m.currentRow()
	return value, ok && value.kind == itemRow
}

func (m renderFrame) currentContainer() bool {
	current, ok := m.currentItem()
	return ok && current.place.Kind == picker.ContainerKind && current.place.ContainerID != ""
}

func (m renderFrame) markedTargets() []picker.ContainerRef {
	return append([]picker.ContainerRef(nil), m.markedTargetsList...)
}

func hasLoadedRows(rows []row) bool {
	for _, candidate := range rows {
		if candidate.kind == itemRow || candidate.kind == groupRow {
			return true
		}
	}
	return false
}

type hitTarget struct {
	tab   Tab
	rowID string
	fold  bool
}

// hitTest is the rendering seam consumed by input. It uses the same frame,
// rows, layout, and tab counts that View renders, so mouse coordinates cannot
// drift from the visible presentation.
func (m renderFrame) hitTest(x, y int) hitTarget {
	if y == 0 {
		offset := 0
		for _, item := range []struct {
			name string
			tab  Tab
		}{{"Agents", AgentsTab}, {"Containers", ContainersTab}, {"Projects", ProjectsTab}} {
			width := shell.StringWidth(item.name) + shell.StringWidth(fmt.Sprintf(" %d", m.projection.MatchCount(item.tab)))
			if x >= offset && x < offset+width {
				return hitTarget{tab: item.tab}
			}
			offset += width + 3
		}
		return hitTarget{}
	}
	if y < 2 || y >= m.height-1 {
		return hitTarget{}
	}
	lines, start := m.bodyLayout(m.height - 3)
	lineIndex := start + y - 2
	if lineIndex < 0 || lineIndex >= len(lines) || lines[lineIndex].rowID == "" {
		return hitTarget{}
	}
	for _, candidate := range m.rows {
		if candidate.id != lines[lineIndex].rowID || !candidate.cursorable {
			continue
		}
		fold := candidate.foldable && x >= 2+shell.StringWidth(foldIndent(candidate)) && x < 2+shell.StringWidth(foldIndent(candidate))+shell.StringWidth(foldArrow(candidate))
		return hitTarget{rowID: candidate.id, fold: fold}
	}
	return hitTarget{}
}

type bodyLine struct {
	text  string
	rowID string
}

type projectColumns struct {
	name, git, path int
	search          bool
}

const (
	// projectMinName is the floor the name column may shrink to before a row
	// stops being readable.
	projectMinName = 16
	// projectMinBranch is the floor the Git column may shrink to before the
	// name column gives up any of its own cells.
	projectMinBranch = 12
	// projectMinPath is the narrowest folder column worth rendering: enough for
	// a leading ellipsis and a path segment.
	projectMinPath = 6
	// projectNameShare is the share of the row the name column may claim
	// before it is capped. Names are what the user scans, so they lead.
	projectNameShare = 0.45
)

const (
	selectedStateMarkerStart = "\x1b[58;5;0m"
	selectedStateMarkerEnd   = "\x1b[59m"
)

// palette holds the brand colours of assets/logo.svg. Ground, tile and
// tileBorder are the icon frame; the TUI never paints a background, so on
// screen only Fg, Muted and Accent are used. Every entry carries a light and a
// dark variant, because the lime accent is unreadable on a light terminal: the
// light variants are an olive accent and a near-black foreground.
var palette = struct {
	Ground     lipgloss.AdaptiveColor
	Tile       lipgloss.AdaptiveColor
	TileBorder lipgloss.AdaptiveColor
	Fg         lipgloss.AdaptiveColor
	Muted      lipgloss.AdaptiveColor
	Accent     lipgloss.AdaptiveColor
}{
	Ground:     lipgloss.AdaptiveColor{Light: "#0F1115", Dark: "#0F1115"},
	Tile:       lipgloss.AdaptiveColor{Light: "#171A21", Dark: "#171A21"},
	TileBorder: lipgloss.AdaptiveColor{Light: "#2A2F3A", Dark: "#2A2F3A"},
	Fg:         lipgloss.AdaptiveColor{Light: "#1F2329", Dark: "#E8EAED"},
	Muted:      lipgloss.AdaptiveColor{Light: "#5F6774", Dark: "#8A919E"},
	Accent:     lipgloss.AdaptiveColor{Light: "#4E7A00", Dark: "#B8F15A"},
}

func (m renderFrame) view() string {
	if !m.sized || m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := []string{m.tabLine(), m.searchLine()}
	bodyHeight := m.height - 3
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	body := m.renderBody(bodyHeight)
	lines = append(lines, body...)
	lines = append(lines, m.footer())
	for len(lines) < m.height {
		lines = append(lines, fit("", m.width))
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

func (m renderFrame) tabLine() string {
	parts := make([]string, 0, 3)
	for _, item := range []struct {
		name string
		tab  Tab
	}{{"Agents", AgentsTab}, {"Containers", ContainersTab}, {"Projects", ProjectsTab}} {
		count := m.projection.MatchCount(item.tab)
		label := item.name
		countText := fmt.Sprintf(" %d", count)
		if item.tab == m.tab {
			label = accent().Bold(true).Underline(true).Render(label)
		} else {
			label = normal().Render(label)
		}
		parts = append(parts, label+muted().Render(countText))
	}
	return fit(strings.Join(parts, "   "), m.width)
}

func (m renderFrame) searchLine() string {
	count := ""
	if len(m.query) > 0 {
		matches := m.projection.MatchCount(m.tab)
		label := "matches"
		if matches == 1 {
			label = "match"
		}
		count = muted().Render(fmt.Sprintf("%d %s", matches, label))
	}
	countWidth := shell.StringWidth(count)
	promptWidth := m.width - countWidth - 1
	if promptWidth < 1 {
		promptWidth = 1
	}
	prompt := shell.PromptLine(promptWidth, accent().Render("⌕ "), string(m.query), accent().Render("▌"))
	gap := m.width - shell.StringWidth(prompt) - countWidth
	if gap < 0 {
		count = shell.Truncate(count, max(0, m.width-shell.StringWidth(prompt)), "")
		countWidth = shell.StringWidth(count)
		gap = m.width - shell.StringWidth(prompt) - countWidth
	}
	return fit(prompt+strings.Repeat(" ", max(0, gap))+count, m.width)
}

func (m renderFrame) bodyLayout(height int) ([]bodyLine, int) {
	lines := []bodyLine{}
	selectedLine := -1
	for _, row := range m.rows {
		if row.cursorable && row.id == m.cursor {
			selectedLine = len(lines)
		}
		primary, secondary := m.rowText(row)
		rowID := ""
		if row.cursorable {
			rowID = row.id
		}
		lines = append(lines, bodyLine{text: primary, rowID: rowID})
		if height >= 2 && secondary != "" && row.kind == itemRow {
			lines = append(lines, bodyLine{text: secondary, rowID: rowID})
		}
	}
	if height <= 0 {
		return nil, 0
	}
	start := shell.ViewportStart(len(lines), height, selectedLine)
	if height >= 2 && selectedLine >= 0 && selectedLine+1 < len(lines) && start+height <= selectedLine+1 {
		start = selectedLine + 2 - height
	}
	if start < 0 {
		start = 0
	}
	if start+height > len(lines) {
		start = len(lines) - height
	}
	if start < 0 {
		start = 0
	}
	return lines, start
}

func (m renderFrame) renderBody(height int) []string {
	lines, start := m.bodyLayout(height)
	if height <= 0 {
		return nil
	}
	out := []string{}
	for i := start; i < len(lines) && len(out) < height; i++ {
		value := fit(lines[i].text, m.width)
		if lines[i].rowID != "" && lines[i].rowID == m.cursor {
			value = selectedRowBackground(value)
		}
		out = append(out, value)
	}
	for len(out) < height {
		out = append(out, fit("", m.width))
	}
	return out
}

func (m renderFrame) rowText(row row) (string, string) {
	selected := row.cursorable && row.id == m.cursor
	marker := "  "
	// Secondary agent details may occupy a second line, but the selection
	// accent belongs to the row's first line only. This keeps its height
	// constant while moving across every tab.
	switch row.kind {
	case machineRow:
		labelStyle := bold()
		value := strings.Repeat("  ", row.depth) + labelStyle.Render(highlightText(sanitize(row.label), string(m.query), selected)) + muted().Render(fmt.Sprintf("  %d", row.count))
		if row.remoteErr != "" {
			value += muted().Render("  " + sanitize(row.remoteErr))
		} else if row.remoteWarn != "" {
			value += yellow().Render("  " + sanitize(row.remoteWarn))
		} else if row.loading {
			value += muted().Render("  loading…")
		}
		return marker + value, ""
	case groupRow:
		labelStyle := bold()
		return marker + foldPrefixStyled(row) + labelStyle.Render(abbreviateHome(row.label)) + muted().Render(fmt.Sprintf("  %d", row.count)), ""
	case separatorRow:
		return "", ""
	case itemRow:
		if row.itemKind == "agent" {
			return m.agentText(row, marker, selected)
		}
		if row.itemKind == "container" {
			return m.containerText(row, marker, selected)
		}
		return m.placeText(row, marker, selected)
	default:
		return "", ""
	}
}

func (m renderFrame) agentText(row row, marker string, selected bool) (string, string) {
	a := row.agent
	container := a.Container
	nameStyle := bold()
	primary := nameStyle.Render(highlightText(sanitize(a.Harness), string(m.query), selected)) + muted().Render("  "+highlightText(sanitize(container), string(m.query), selected))
	state := strings.ToLower(strings.TrimSpace(sanitize(a.State)))
	stateLabel := "● " + state
	if state == "stopped" && a.StoppedSince != nil {
		stateLabel += " " + stoppedAge(m.now(), *a.StoppedSince)
	}
	left := stateText(a, stateLabel, selected)
	if a.Branch != "" {
		branch := highlightText("⎇ "+sanitize(a.Branch), string(m.query), selected)
		if !strings.Contains(branch, "\x1b[") {
			branch = muted().Render(branch)
		}
		left += "   " + branch
	}
	targetText := highlightText(sanitize(a.Target), string(m.query), selected)
	target := targetText
	if !strings.Contains(targetText, "\x1b[") {
		target = muted().Render(targetText)
	}
	indent := strings.Repeat("  ", row.depth)
	gap := m.width - shell.StringWidth(marker) - shell.StringWidth(indent) - shell.StringWidth(left) - shell.StringWidth(target)
	if gap < 2 {
		gap = 2
	}
	secondary := marker + indent + left + strings.Repeat(" ", gap) + target
	return marker + indent + primary, secondary
}

func stateText(agent capabilities.Agent, text string, selected bool) string {
	if !selected {
		return stateColor(agent).Render(text)
	}
	return selectedStateMarkerStart + stateColor(agent).Render(text) + selectedStateMarkerEnd
}

func (m renderFrame) containerText(row row, marker string, selected bool) (string, string) {
	name := sanitize(row.place.ContainerName)
	if row.nameWidth > 0 {
		name = shell.Truncate(name, row.nameWidth, "…")
		name += strings.Repeat(" ", max(0, row.nameWidth-shell.StringWidth(name)))
	}
	nameGutter := "  "
	marked := m.marked[picker.ContainerRef{Machine: row.place.Machine, ID: row.place.ContainerID}]
	if marked {
		nameGutter = accent().Render("◆ ")
		name = highlightMarkedText(name, string(m.query), selected)
	} else {
		name = highlightText(name, string(m.query), selected)
	}
	name = nameGutter + name
	agents := row.place.Agents
	dots := make([]string, 0, len(agents))
	for _, agent := range agents {
		dots = append(dots, stateText(agent, "●", selected))
	}
	line := name
	if len(dots) > 0 {
		line += " " + strings.Join(dots, " ")
	}
	available := max(1, m.width-shell.StringWidth(marker))
	if shell.StringWidth(line) > available {
		line = shell.Truncate(line, available, "…")
	}
	return marker + line, ""
}

// projectLayout sizes the project columns from the content of the visible
// rows. Names lead: the longest visible name claims its share of the row up
// front, the branch and its Git summary share one column, and the flat-mode
// folder on the left takes only what is left over. The folder is left-
// truncated and dropped before the name gives up cells; the branch truncates
// before its Git summary, which stays visible.
func (m renderFrame) projectLayout() projectColumns {
	layout := projectColumns{search: string(m.query) != ""}
	maxName, maxGit, maxSummary, maxPath := 0, 0, 0, 0
	for _, candidate := range m.rows {
		if candidate.kind != itemRow || candidate.itemKind != "project" {
			continue
		}
		if width := projectNameWidth(candidate); width > maxName {
			maxName = width
		}
		branch := shell.StringWidth(projectBranch(candidate.place))
		summary := shell.StringWidth(m.projectSummary(candidate.place))
		git := branch
		if summary > 0 {
			if branch > 0 {
				git += 2
			}
			git += summary
		}
		if git > maxGit {
			maxGit = git
		}
		if summary > maxSummary {
			maxSummary = summary
		}
		if layout.search {
			// The trailing slash is part of the folder cell.
			if width := shell.StringWidth(abbreviateHome(candidate.group)) + 1; width > maxPath {
				maxPath = width
			}
		}
	}

	// The marker and the gutter between the name and the Git column are fixed
	// costs. The folder column takes only what the name and Git column leave
	// unused, so it never costs the name a cell.
	base := max(0, m.width-(2+2))

	// The name and the Git column are sized on their own, without the folder.
	// A row that already fits keeps its natural widths.
	if maxName+maxGit <= base {
		layout.name, layout.git = maxName, maxGit
	} else {
		// The name leads. It claims the longest visible name up to its share
		// of the row, but never drops below its floor.
		nameFloor := min(projectMinName, maxName)
		nameCap := int(float64(m.width) * projectNameShare)
		if nameCap < projectMinName {
			nameCap = projectMinName
		}
		nameTarget := min(maxName, nameCap)
		if nameTarget < nameFloor {
			nameTarget = nameFloor
		}
		if nameTarget > base {
			nameTarget = base
		}

		// The Git summary never truncates; the branch gives up cells first
		// and the name shrinks only after the summary floor is protected.
		gitFloor := max(maxSummary, min(maxGit, projectMinBranch))
		git := min(maxGit, max(base-nameTarget, 0))
		if git < gitFloor {
			nameTarget = min(nameTarget, max(nameFloor, base-gitFloor))
			git = min(maxGit, max(base-nameTarget, 0))
			if git < gitFloor {
				git = gitFloor
			}
		}
		layout.name, layout.git = nameTarget, git
	}

	// The folder is lowest priority: it is right-aligned and left-truncated,
	// so it can stop short of its natural width without moving the name, and
	// it is dropped whole before the name loses a cell or the branch is cut.
	if layout.search && maxPath > 0 {
		if leftover := base - layout.name - layout.git; leftover >= projectMinPath {
			layout.path = min(maxPath, leftover)
		}
	}

	layout.name = max(layout.name, 0)
	layout.git = max(layout.git, 0)
	return layout
}

func projectNameWidth(candidate row) int {
	width := shell.StringWidth(foldPrefix(candidate) + projectLabel(candidate.place))
	if candidate.foldable && !candidate.expanded {
		width += shell.StringWidth(fmt.Sprintf("  %d", candidate.count))
	}
	return width
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m renderFrame) placeText(row row, marker string, selected bool) (string, string) {
	if row.place.Kind != picker.ProjectKind {
		return marker + highlightText(projectLabel(row.place), string(m.query), selected), ""
	}
	layout := m.projectLayout()
	name := foldPrefix(row) + projectLabel(row.place)
	count := ""
	if row.foldable && !row.expanded {
		count = fmt.Sprintf("  %d", row.count)
	}
	name = shell.Truncate(name, max(0, layout.name-shell.StringWidth(count)), "…")
	nameText := highlightText(name, string(m.query), selected)
	if prefix := foldPrefix(row); prefix != "" {
		// Tree gutters and fold chevrons are structural, not part of fuzzy matching.
		nameText = foldPrefixStyled(row) + highlightText(strings.TrimPrefix(name, prefix), string(m.query), selected)
	}
	if count != "" {
		nameText += muted().Render(count)
	}
	nameCell := fit(nameText, layout.name)
	gitCell := fit(m.projectGitStyled(row.place, layout.git, selected), layout.git)
	line := ""
	if layout.search && layout.path > 0 {
		line = m.projectPathCell(row.group, layout.path, selected)
	}
	line += nameCell + "  " + gitCell
	return marker + line, ""
}

func (m renderFrame) projectGitStyled(place picker.Place, width int, selected bool) string {
	if width <= 0 {
		return ""
	}
	branch := highlightText(projectBranch(place), string(m.query), selected)
	if !strings.Contains(branch, "\x1b[") {
		branch = muted().Render(branch)
	}
	summary := m.projectSummaryStyled(place)
	if summary == "" {
		return shell.Truncate(branch, width, "")
	}
	summaryWidth := shell.StringWidth(summary)
	if summaryWidth >= width {
		return shell.Truncate(summary, width, "")
	}
	if projectBranch(place) == "" {
		return summary
	}
	branchWidth := width - summaryWidth - 2
	if branchWidth <= 0 {
		return summary
	}
	return shell.Truncate(branch, branchWidth, "") + "  " + summary
}

func (m renderFrame) projectPathCell(group string, width int, selected bool) string {
	path := shell.TruncateLeft(abbreviateHome(group)+"/", width, "…")
	pathText := highlightText(path, string(m.query), selected)
	if !strings.Contains(pathText, "\x1b[") {
		pathText = muted().Render(pathText)
	}
	return strings.Repeat(" ", max(0, width-shell.StringWidth(path))) + pathText
}

func projectBranch(place picker.Place) string {
	if place.GitContext == nil {
		if place.Git {
			return "…"
		}
		return ""
	}
	if identity := sanitize(place.GitContext.Identity); identity != "" {
		return "⎇ " + identity
	}
	return ""
}

func (m renderFrame) projectSummary(place picker.Place) string {
	if errText := sanitize(m.gitErrors[place.Path]); errText != "" {
		return "git: error " + errText
	}
	if place.GitContext == nil {
		return ""
	}
	g := *place.GitContext
	parts := []string{}
	if g.Adds > 0 {
		parts = append(parts, "+"+itoa(g.Adds))
	}
	if g.Deletes > 0 {
		parts = append(parts, "−"+itoa(g.Deletes))
	}
	if g.Dirty {
		parts = append(parts, "*")
	}
	if g.Ahead > 0 {
		parts = append(parts, "↑"+itoa(g.Ahead))
	}
	if g.Behind > 0 {
		parts = append(parts, "↓"+itoa(g.Behind))
	}
	return strings.Join(parts, " ")
}

func (m renderFrame) projectSummaryStyled(place picker.Place) string {
	if errText := sanitize(m.gitErrors[place.Path]); errText != "" {
		return muted().Render("git: error " + errText)
	}
	if place.GitContext == nil {
		return ""
	}
	g := *place.GitContext
	parts := []string{}
	if g.Adds > 0 {
		parts = append(parts, brightGreen().Render("+"+itoa(g.Adds)))
	}
	if g.Deletes > 0 {
		parts = append(parts, brightRed().Render("−"+itoa(g.Deletes)))
	}
	if g.Dirty {
		parts = append(parts, yellow().Render("*"))
	}
	if g.Ahead > 0 {
		parts = append(parts, divergence().Render("↑"+itoa(g.Ahead)))
	}
	if g.Behind > 0 {
		parts = append(parts, divergence().Render("↓"+itoa(g.Behind)))
	}
	return strings.Join(parts, " ")
}

func (m renderFrame) footer() string {
	if m.toast != "" {
		return fit(accent().Render(sanitize(m.toast)), m.width)
	}
	if m.confirmKill {
		return fit(accent().Render("kill selected containers? ")+accent().Render("y")+muted().Render(" confirm  ")+accent().Render("n")+muted().Render(" cancel"), m.width)
	}
	if m.renameMode {
		suffix := muted().Render("  Enter save  Esc cancel")
		promptWidth := m.width - shell.StringWidth(suffix)
		if promptWidth < 1 {
			promptWidth = 1
		}
		prompt := shell.PromptLine(promptWidth, accent().Render("rename: "), string(m.renameInput), accent().Render("▌"))
		return fit(prompt+suffix, m.width)
	}
	if m.refreshErr != nil {
		return fit(muted().Render("agent refresh: "+sanitize(m.refreshErr.Error())), m.width)
	}
	left := []string{}
	left = append(left, hint("↑↓", "move"))
	if _, ok := m.currentItem(); ok {
		left = append(left, hint("Enter", "open"))
	}
	left = append(left, hint("Tab", "tabs"))
	if m.tab == ProjectsTab {
		if current, ok := m.currentRow(); ok && (current.foldable || foldParent(current) != "") {
			left = append(left, hint("←→", "fold"))
		}
	}
	if m.currentContainer() || len(m.markedTargets()) > 0 {
		left = append(left, hint("Ctrl-X", "kill"))
	}
	if m.currentContainer() {
		left = append(left, hint("Ctrl-R", "rename"), hint("Ctrl-T", "mark"))
	}
	right := ""
	if count := len(m.markedTargets()); count > 0 {
		right = accent().Render(fmt.Sprintf("%d marked", count))
	}
	available := m.width
	if right != "" {
		available -= shell.StringWidth(right) + 1
	}
	leftText := wholeHints(left, available)
	if right != "" {
		return fit(leftText+strings.Repeat(" ", max(1, m.width-shell.StringWidth(leftText)-shell.StringWidth(right)))+right, m.width)
	}
	return fit(leftText, m.width)
}

func wholeHints(hints []string, width int) string {
	if width <= 0 {
		return ""
	}
	parts := make([]string, 0, len(hints))
	used := 0
	for _, value := range hints {
		separator := 0
		if len(parts) > 0 {
			separator = 2
		}
		partWidth := shell.StringWidth(value)
		if used+separator+partWidth > width {
			break
		}
		parts = append(parts, value)
		used += separator + partWidth
	}
	return strings.Join(parts, "  ")
}

func hint(keyValue, label string) string {
	return accent().Bold(true).Render(keyValue) + " " + muted().Render(label)
}

func sanitize(value string) string { return shell.Plain(value) }

func fit(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = stripBackgroundResets(value)
	value = shell.Truncate(value, width, "")
	return value + strings.Repeat(" ", max(0, width-shell.StringWidth(value)))
}

func selectedRowBackground(value string) string {
	// Selected rows use one foreground everywhere except explicitly marked
	// agent-state segments. Strip every other per-segment foreground (and faint)
	// first, then restore the selection start after each reset. Bold and
	// underline survive, so matched letters and marked names stay distinguishable.
	value = stripForegroundMarked(value)
	style := lipgloss.NewStyle().Background(selectionBackground()).Foreground(selectionForeground())
	start := strings.TrimSuffix(style.Render(""), "\x1b[0m")
	if start == "" {
		return value
	}
	value = strings.ReplaceAll(value, "\x1b[0m", "\x1b[0m"+start)
	return start + value + "\x1b[0m"
}

// stripForegroundMarked drops every unmarked foreground colour and the faint
// attribute from styled text while keeping the remaining SGR attributes
// (reset, bold, underline, …).
func stripForegroundMarked(value string) string {
	marked := false
	out := strings.Builder{}
	for index := 0; index < len(value); {
		if strings.HasPrefix(value[index:], selectedStateMarkerStart) {
			marked = true
			index += len(selectedStateMarkerStart)
			continue
		}
		if strings.HasPrefix(value[index:], selectedStateMarkerEnd) {
			marked = false
			index += len(selectedStateMarkerEnd)
			continue
		}
		if value[index] != '\x1b' || index+1 >= len(value) || value[index+1] != '[' {
			out.WriteByte(value[index])
			index++
			continue
		}
		end := strings.IndexByte(value[index+2:], 'm')
		if end < 0 {
			out.WriteString(value[index:])
			break
		}
		end += index + 2
		if kept := keptMarkedAttributes(value[index+2:end], marked); kept != "" {
			out.WriteString("\x1b[")
			out.WriteString(kept)
			out.WriteByte('m')
		}
		index = end + 1
	}
	return out.String()
}

func keptMarkedAttributes(params string, marked bool) string {
	if marked {
		return params
	}
	return keptAttributes(params)
}

func keptAttributes(params string) string {
	parts := strings.Split(params, ";")
	kept := make([]string, 0, len(parts))
	for position := 0; position < len(parts); position++ {
		code, err := strconv.Atoi(parts[position])
		if err != nil {
			if parts[position] != "" {
				kept = append(kept, parts[position])
			}
			continue
		}
		switch {
		case code == 2, code == 39:
			// Faint and the default foreground both fight the selection.
		case code == 38:
			// Skip the extended foreground payload (38;5;n or 38;2;r;g;b).
			if position+1 < len(parts) {
				switch parts[position+1] {
				case "5":
					position += 2
				case "2":
					position += 4
				}
			}
		case code >= 30 && code <= 37, code >= 90 && code <= 97:
			// Standard and bright foreground colours.
		default:
			kept = append(kept, parts[position])
		}
	}
	return strings.Join(kept, ";")
}

func selectionBackground() lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: "#d9eaf7", Dark: "#31516f"}
}

func selectionForeground() lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: "#0b1b28", Dark: "#f4f8fc"}
}

func stripBackgroundResets(value string) string {
	return strings.ReplaceAll(value, "\x1b[49m", "")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func itoa(value int) string { return fmt.Sprintf("%d", value) }

func stoppedAge(now, since time.Time) string { return picker.StoppedAge(now, since) }

func stateColor(agent capabilities.Agent) lipgloss.Style {
	switch {
	case strings.EqualFold(strings.TrimSpace(agent.State), "stopped") && !agent.Seen:
		return red()
	case strings.EqualFold(strings.TrimSpace(agent.State), "stopped"):
		return green()
	default:
		return yellow()
	}
}

func normal() lipgloss.Style { return lipgloss.NewStyle().Foreground(palette.Fg) }

func muted() lipgloss.Style { return lipgloss.NewStyle().Foreground(palette.Muted) }

func foldStyle() lipgloss.Style { return muted() }

func accent() lipgloss.Style { return lipgloss.NewStyle().Foreground(palette.Accent) }

// Semantic colours stay terminal ANSI codes, so agent state, Git summary and
// errors follow the user's terminal theme instead of the brand palette.
func divergence() lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color("6")) }

func red() lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color("1")) }

func green() lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color("2")) }

func yellow() lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color("3")) }

func brightGreen() lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color("10")) }

func brightRed() lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color("9")) }

func bold() lipgloss.Style { return normal().Bold(true) }

func highlightText(text, query string, selected bool) string {
	match, ok := picker.Rank(query, text)
	if !ok || len(match.Positions) == 0 {
		return text
	}
	positions := map[int]bool{}
	for _, pos := range match.Positions {
		positions[pos] = true
	}
	runes := []rune(text)
	out := strings.Builder{}
	for i, r := range runes {
		if positions[i] {
			style := accent().Bold(true)
			if selected {
				// The selected row has no per-segment colour, so the match
				// relies on bold and underline alone to stay visible.
				style = style.Underline(true)
			}
			out.WriteString(style.Render(string(r)))
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// highlightMarkedText paints every name character of a marked row with the
// accent colour. Matching characters add bold and underline, so the query
// stays visible without a second colour. A selected row drops the accent and
// keeps the mark cue as bold weight instead.
func highlightMarkedText(text, query string, selected bool) string {
	match, ok := picker.Rank(query, text)
	if !ok || len(match.Positions) == 0 {
		if selected {
			return bold().Render(text)
		}
		return accent().Render(text)
	}
	positions := map[int]bool{}
	for _, pos := range match.Positions {
		positions[pos] = true
	}
	out := strings.Builder{}
	for i, r := range []rune(text) {
		style := accent()
		if selected {
			// The selected row keeps the mark cue as weight rather than colour.
			style = bold()
		}
		if positions[i] {
			style = style.Bold(true).Underline(true)
		}
		out.WriteString(style.Render(string(r)))
	}
	return out.String()
}

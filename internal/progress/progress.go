// Package progress provides an inline Bubble Tea view for work performed on
// several named machines. It owns no command or installation policy.
package progress

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Step is the current operation shown beside a machine.
type Step string

const (
	StepProbing    Step = "probing"
	StepBuilding   Step = "building"
	StepCopying    Step = "copying"
	StepInstalling Step = "installing"
)

// The shared TUI theme is intentionally not imported: internal/tui cannot be
// imported here without coupling the reusable command component to its form
// package. These are foreground-only semantic colours and never paint a
// background.
var (
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	doneStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	failedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// Go's pseudo-versions have three forms: a timestamp directly after the
	// base version, a 0.timestamp prerelease, and a prerelease.0.timestamp.
	// Keep metadata (for example +dirty or +incompatible) when shortening one.
	pseudoVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+-(?:[0-9]{14}|0\.[0-9]{14}|[0-9A-Za-z.-]+\.0\.[0-9]{14})-([0-9a-fA-F]{12})((?:\+[A-Za-z0-9.-]+)*)$`)
)

// State is the lifecycle state of one machine.
type State string

const (
	StateRunning   State = "running"
	StateDone      State = "done"
	StateFailed    State = "failed"
	StateSkipped   State = "skipped"
	StateCancelled State = "cancelled"
)

// Event updates one machine row. At and Elapsed are optional: running events
// can leave them zero and let the view read the current clock, while a
// terminal event carries the measured elapsed time.
type Event struct {
	Machine string
	State   State
	Step    Step
	Old     string
	New     string
	Err     error
	At      time.Time
	Elapsed time.Duration
}

// EventMsg delivers an Event to a Bubble Tea model.
type EventMsg struct{ Event Event }

// CompleteMsg tells a model that all workers have returned. The live view is
// cleared before Bubble Tea exits; callers print FinalView as ordinary output
// after Program.Run so it cannot be clipped by the renderer.
type CompleteMsg struct{}

// Model is an inline, non-alt-screen progress view.
type Model struct {
	Header string

	rows   []Row
	spin   spinner.Model
	cancel func()
	width  int
	height int
	done   bool
}

// Row is the public snapshot of one machine's state.
type Row struct {
	Machine string
	State   State
	Step    Step
	Old     string
	New     string
	Started time.Time
	Elapsed time.Duration
	Err     error
}

// NewModel creates a model with one running row per machine. Rows are kept in
// the given order so the display remains stable as concurrent events arrive.
func NewModel(header string, machines []string) Model {
	model := Model{
		Header: header,
		rows:   make([]Row, 0, len(machines)),
		spin:   spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
	model.spin.Style = accentStyle
	for _, name := range machines {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if model.rowIndex(name) >= 0 {
			continue
		}
		model.rows = append(model.rows, Row{Machine: name, State: StateRunning, Step: StepProbing})
	}
	return model
}

// SetCancel registers the operation cancellation invoked by Ctrl-C. It is
// intentionally separate from the Bubble Tea context: workers get a chance
// to publish their cancelled result and the final view remains visible.
func (m *Model) SetCancel(cancel func()) {
	if m == nil {
		return
	}
	m.cancel = cancel
}

// Apply applies an event and is useful to non-Bubble-Tea callers and tests.
func (m *Model) Apply(event Event) {
	if m == nil {
		return
	}
	name := strings.TrimSpace(event.Machine)
	if name == "" {
		return
	}
	index := m.rowIndex(name)
	row := Row{Machine: name}
	if index >= 0 {
		row = m.rows[index]
	}
	at := event.At
	if at.IsZero() {
		at = time.Now()
	}
	if row.Started.IsZero() {
		row.Started = at
	}
	if event.State != "" {
		row.State = event.State
	} else if row.State == "" {
		row.State = StateRunning
	}
	if event.Step != "" {
		row.Step = event.Step
	}
	if event.Old != "" {
		row.Old = event.Old
	}
	if event.New != "" {
		row.New = event.New
	}
	if event.State == StateCancelled {
		row.Err = nil
	} else if event.Err != nil {
		row.Err = event.Err
	}
	if event.Elapsed > 0 {
		row.Elapsed = event.Elapsed
	} else if row.State != StateRunning && row.Elapsed == 0 {
		row.Elapsed = at.Sub(row.Started)
	}
	if index < 0 {
		m.rows = append(m.rows, row)
	} else {
		m.rows[index] = row
	}
}

// Rows returns a stable snapshot suitable for assertions or another renderer.
func (m Model) Rows() []Row {
	return append([]Row(nil), m.rows...)
}

func (m Model) rowIndex(name string) int {
	for index, row := range m.rows {
		if row.Machine == name {
			return index
		}
	}
	return -1
}

// Init starts the spinner. There is no alt-screen command here; Bubble Tea's
// default renderer redraws inline and leaves the terminal's final frame.
func (m Model) Init() tea.Cmd { return m.spin.Tick }

// Update handles progress events, spinner ticks, and Ctrl-C cancellation.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case EventMsg:
		m.Apply(msg.Event)
		return m, nil
	case CompleteMsg:
		m.done = true
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			if m.cancel != nil {
				m.cancel()
			}
			return m, nil
		}
	}
	updated, cmd := m.spin.Update(msg)
	m.spin = updated
	return m, cmd
}

// View renders only the live, height-bounded view. Once CompleteMsg is
// applied it is empty: the command prints FinalView after Program.Run.
func (m Model) View() string {
	if m.done {
		return ""
	}
	return m.render(false)
}

// FinalView renders the complete table and every full failure error. It is
// intended for ordinary stdout after Bubble Tea exits, not for the live
// redrawable view.
func (m Model) FinalView() string { return m.render(true) }

type renderedRow struct {
	machine, status, step, elapsed, detail string
}

type tableLayout struct {
	machine, status, step, elapsed, detail int
	showStep, showElapsed, showDetail      bool
}

func (m Model) render(final bool) string {
	rows := m.Rows()
	rendered := make([]renderedRow, len(rows))
	nameWidth, statusWidth := lipgloss.Width("MACHINE"), lipgloss.Width("STATUS")
	stepWidth, elapsedWidth := lipgloss.Width("STEP"), lipgloss.Width("ELAPSED")
	detailWidth := lipgloss.Width("DETAIL")
	for index, row := range rows {
		status := string(row.State)
		switch row.State {
		case "", StateRunning:
			status = m.spin.View() + " running"
		case StateDone:
			status = doneStyle.Render("✓ done")
		case StateSkipped:
			status = doneStyle.Render("✓ skipped")
		case StateFailed:
			status = failedStyle.Render("✗ failed")
		case StateCancelled:
			status = mutedStyle.Render("– cancelled")
		}
		step := string(row.Step)
		if row.State == StateDone || row.State == StateSkipped {
			step = ""
		}
		elapsed := row.Elapsed
		if row.State == StateRunning {
			started := row.Started
			if started.IsZero() {
				started = time.Now()
			}
			elapsed = time.Since(started)
		}
		viewRow := renderedRow{
			machine: row.Machine,
			status:  status,
			step:    step,
			elapsed: mutedStyle.Render(formatElapsed(elapsed)),
			detail:  mutedStyle.Render(detailForRow(row)),
		}
		rendered[index] = viewRow
		nameWidth = maxWidth(nameWidth, viewRow.machine)
		statusWidth = maxWidth(statusWidth, viewRow.status)
		stepWidth = maxWidth(stepWidth, viewRow.step)
		elapsedWidth = maxWidth(elapsedWidth, viewRow.elapsed)
		detailWidth = maxWidth(detailWidth, viewRow.detail)
	}
	layout := makeTableLayout(nameWidth, statusWidth, stepWidth, elapsedWidth, detailWidth, m.width)

	selected := rendered
	more := 0
	if !final && m.height > 0 {
		// Reserve one line for the column header and one for the overflow
		// marker. The header itself consumes one line when present.
		available := m.height - 1
		if m.Header != "" {
			available--
		}
		if available < 0 {
			available = 0
		}
		if len(selected) > available {
			available--
			if available < 0 {
				available = 0
			}
			more = len(selected) - available
			selected = selected[:available]
		}
	}

	var b strings.Builder
	if m.Header != "" {
		header := m.Header
		if m.width > 0 {
			header = ansi.Truncate(header, m.width, "…")
		}
		b.WriteString(header)
		b.WriteByte('\n')
	}
	writeTableRow(&b, []string{"MACHINE", "STATUS", "STEP", "ELAPSED", "DETAIL"}, layout)
	for _, row := range selected {
		writeTableRow(&b, []string{row.machine, row.status, row.step, row.elapsed, row.detail}, layout)
	}
	if more > 0 {
		line := fmt.Sprintf("+%d more", more)
		if m.width > 0 {
			line = ansi.Truncate(line, m.width, "…")
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if !final {
		return strings.TrimSuffix(b.String(), "\n")
	}
	for _, row := range rows {
		if row.State != StateFailed || row.Err == nil {
			continue
		}
		for _, line := range fullErrorLines(row, m.width) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func makeTableLayout(machine, status, step, elapsed, detail, width int) tableLayout {
	layout := tableLayout{
		machine:     machine,
		status:      status,
		step:        step,
		elapsed:     elapsed,
		detail:      detail,
		showStep:    true,
		showElapsed: true,
		showDetail:  true,
	}
	if width <= 0 {
		return layout
	}
	// Long aliases are the first thing to constrain. Twelve cells is enough
	// to distinguish ordinary aliases while leaving room for STATUS.
	if layout.machine > 12 {
		layout.machine = 12
	}
	if layout.machine < 1 {
		layout.machine = 1
	}
	if tableWidth(layout) > width {
		withoutDetail := layout
		withoutDetail.showDetail = false
		withoutDetail.detail = 0
		available := width - tableWidth(withoutDetail) - 2
		// Keep a useful sliver of detail when it fits; otherwise drop the
		// entire lower-priority column before dropping ELAPSED or STEP.
		if available >= 4 {
			layout.detail = available
		} else {
			layout.showDetail = false
			layout.detail = 0
		}
	}
	if tableWidth(layout) > width && layout.showElapsed {
		layout.showElapsed = false
		layout.elapsed = 0
	}
	if tableWidth(layout) > width && layout.showStep {
		layout.showStep = false
		layout.step = 0
	}
	if tableWidth(layout) > width {
		// STATUS is mandatory. At normal terminal widths this only further
		// shortens MACHINE; retain one cell even for pathological widths.
		layout.machine = max(1, width-layout.status-2)
	}
	return layout
}

func tableWidth(layout tableLayout) int {
	width := layout.machine + layout.status
	columns := 2
	if layout.showStep {
		width += layout.step
		columns++
	}
	if layout.showElapsed {
		width += layout.elapsed
		columns++
	}
	if layout.showDetail {
		width += layout.detail
		columns++
	}
	return width + 2*(columns-1)
}

func writeTableRow(b *strings.Builder, values []string, layout tableLayout) {
	columns := []struct {
		value int
		width int
	}{
		{value: 0, width: layout.machine},
		{value: 1, width: layout.status},
	}
	if layout.showStep {
		columns = append(columns, struct {
			value int
			width int
		}{value: 2, width: layout.step})
	}
	if layout.showElapsed {
		columns = append(columns, struct {
			value int
			width int
		}{value: 3, width: layout.elapsed})
	}
	if layout.showDetail {
		columns = append(columns, struct {
			value int
			width int
		}{value: 4, width: layout.detail})
	}
	for index, column := range columns {
		if index > 0 {
			b.WriteString("  ")
		}
		value := values[column.value]
		if column.width > 0 {
			value = ansi.Truncate(value, column.width, "…")
			value = padVisible(value, column.width)
		}
		b.WriteString(value)
	}
	b.WriteByte('\n')
}

func fullErrorLines(row Row, width int) []string {
	text := row.Machine + ": "
	if row.Err != nil {
		text += row.Err.Error()
	}
	if width <= 0 {
		return strings.Split(text, "\n")
	}
	// Reserve the continuation indent while wrapping. This also wraps an
	// overlong machine label instead of allowing it to hide the error.
	wrapWidth := width
	indent := ""
	if width >= 3 {
		wrapWidth = width - 2
		indent = "  "
	}
	lines := wrapText(text, wrapWidth)
	for index := 1; index < len(lines); index++ {
		lines[index] = indent + lines[index]
	}
	return lines
}

func wrapText(value string, width int) []string {
	if width < 1 {
		width = 1
	}
	lines := make([]string, 0)
	for _, paragraph := range strings.Split(value, "\n") {
		wrapped := ansi.Wordwrap(paragraph, width, " ")
		for _, line := range strings.Split(wrapped, "\n") {
			if ansi.StringWidth(line) <= width {
				lines = append(lines, line)
				continue
			}
			lines = append(lines, strings.Split(ansi.Hardwrap(line, width, false), "\n")...)
		}
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func detailForRow(row Row) string {
	if row.State == StateCancelled {
		return ""
	}
	if row.Err != nil {
		return shortError(row.Err)
	}
	oldVersion := abbreviateVersion(row.Old)
	newVersion := abbreviateVersion(row.New)
	switch row.State {
	case StateDone:
		if oldVersion != "" && newVersion != "" {
			return oldVersion + " → " + newVersion
		}
		if newVersion != "" {
			return newVersion
		}
	case StateSkipped:
		if oldVersion != "" {
			return oldVersion + " (already on target)"
		}
	}
	if newVersion != "" {
		return newVersion
	}
	return oldVersion
}

func abbreviateVersion(value string) string {
	match := pseudoVersionPattern.FindStringSubmatch(value)
	if len(match) == 0 {
		return value
	}
	return match[1] + match[2]
}

func maxWidth(current int, value string) int {
	return max(current, lipgloss.Width(value))
}

func padVisible(value string, width int) string {
	padding := width - lipgloss.Width(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}

func formatElapsed(elapsed time.Duration) string {
	if elapsed <= 0 {
		return "0s"
	}
	return elapsed.Round(time.Second).String()
}

func shortError(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(err.Error(), "\n", 2)[0])
}

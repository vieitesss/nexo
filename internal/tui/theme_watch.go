package tui

// The theme watcher is a deep module behind the coordinator's tick seam. It
// owns config polling, change detection (mtime, size and inode) and the
// once-per-bad-content status policy. The model only schedules its tick and
// applies an accepted reload, so the untestable parts stay on the other side
// of the interface.

import (
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/config"
)

// themeReloadInterval is the default gap between config polls. It matches the
// agent refresh cadence closely enough that an edit lands within a second.
const themeReloadInterval = time.Second

// themeTickMsg asks the watcher to compare the config file with its last
// observation. It carries no state: the watcher owns the baseline.
type themeTickMsg struct{}

// themeReloadMsg carries an accepted poll result. An empty theme means the file
// did not change, or the change did not name a new light or dark variant. A
// non-empty toast is the single status line for bad content.
type themeReloadMsg struct {
	theme string
	toast string
}

// themeWatcher follows [tui].theme in one config file. A missing path disables
// it. Each observation reads the file when its identity moved on; the first
// also reads once so a write between config load and startup is not lost.
// "auto" is never re-probed mid-session: the OSC 11 probe is only safe before
// Bubble Tea owns the terminal.
type themeWatcher struct {
	path     string
	interval time.Duration
	stat     func(string) (os.FileInfo, error)
	readFile func(string) ([]byte, error)
	current  string

	baseline os.FileInfo
	bad      string
}

func newThemeWatcher(path, current string, deps Deps) *themeWatcher {
	if path == "" {
		return nil
	}
	interval := deps.ThemeReloadInterval
	if interval <= 0 {
		interval = themeReloadInterval
	}
	stat := deps.ThemeStat
	if stat == nil {
		stat = os.Stat
	}
	readFile := deps.ThemeRead
	if readFile == nil {
		readFile = os.ReadFile
	}
	return &themeWatcher{
		path: path, interval: interval, stat: stat, readFile: readFile, current: current,
	}
}

// Tick polls the config once and schedules the next poll. A nil watcher has
// nothing to schedule.
func (w *themeWatcher) Tick() tea.Cmd {
	if w == nil {
		return nil
	}
	return tea.Batch(w.check(), w.tick())
}

func (w *themeWatcher) check() tea.Cmd {
	return func() tea.Msg { return w.poll() }
}

func (w *themeWatcher) tick() tea.Cmd {
	return tea.Tick(w.interval, func(time.Time) tea.Msg { return themeTickMsg{} })
}

// poll observes the file and returns a reload only when its identity changed
// and the new value names a light or dark variant. The first observation reads
// once too, so a write between config load and the first poll is not lost. Bad
// content keeps the current theme and toasts once per distinct content. A
// failed stat keeps the current theme silently: the file may be mid-replacement.
func (w *themeWatcher) poll() tea.Msg {
	info, err := w.stat(w.path)
	if err != nil {
		return nil
	}
	changed := w.baseline == nil || themeFileChanged(w.baseline, info)
	if !changed {
		return nil
	}
	w.baseline = info
	return w.readTheme()
}

// readTheme reads and applies [tui].theme, keeping the current variant when
// the file is unreadable, invalid, or set to auto.
func (w *themeWatcher) readTheme() tea.Msg {
	contents, err := w.readFile(w.path)
	if err != nil {
		return w.badStatus("!"+err.Error(), "theme reload: "+err.Error())
	}
	theme, err := config.ThemeFromConfig(contents, w.path)
	if err != nil {
		return w.badStatus(string(contents), "theme reload: "+err.Error())
	}
	w.bad = ""
	switch theme {
	case config.ThemeDark, config.ThemeLight:
		if theme == w.current {
			return nil
		}
		w.current = theme
		return themeReloadMsg{theme: theme}
	default:
		// auto (or an absent value) keeps the current variant; live probing is
		// unsafe once Bubble Tea owns the terminal.
		return nil
	}
}

// badStatus reports bad content once per distinct value, so repeated polls of
// the same broken file do not refill the status line.
func (w *themeWatcher) badStatus(content, toast string) tea.Msg {
	if content == w.bad {
		return nil
	}
	w.bad = content
	return themeReloadMsg{toast: toast}
}

// themeFileChanged reports whether the file moved on since the last
// observation. Size and mtime catch an in-place rewrite; os.SameFile catches a
// file replaced with mv, which can keep both.
func themeFileChanged(previous, current os.FileInfo) bool {
	return current.Size() != previous.Size() ||
		!current.ModTime().Equal(previous.ModTime()) ||
		!os.SameFile(previous, current)
}

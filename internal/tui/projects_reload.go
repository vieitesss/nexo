package tui

import tea "github.com/charmbracelet/bubbletea"

// ReloadProjects replaces the project source after a filesystem action.
// Git decoration is scheduled from the accepted replacement, just like the
// initial load, and stale generations are ignored by Accept.
func (s *sourceState) ReloadProjects() tea.Cmd {
	if s.projectSource == nil {
		return nil
	}
	s.generations.projects++
	return s.refreshProjectsAt(s.generations.projects)
}

// Package capabilities defines the small operations a backend may support.
package capabilities

import "sort"

// Names reports the capabilities implemented by backend in stable order.
func Names(backend any) []string {
	var names []string
	if _, ok := backend.(ProjectOpener); ok {
		names = append(names, "ProjectOpener")
	}
	if _, ok := backend.(PaneLister); ok {
		names = append(names, "PaneLister")
	}
	if _, ok := backend.(ScreenReader); ok {
		names = append(names, "ScreenReader")
	}
	if _, ok := backend.(PaneFocuser); ok {
		names = append(names, "PaneFocuser")
	}
	if _, ok := backend.(Popup); ok {
		names = append(names, "Popup")
	}
	if _, ok := backend.(AgentSource); ok {
		names = append(names, "AgentSource")
	}
	if _, ok := backend.(Notifier); ok {
		names = append(names, "Notifier")
	}
	if _, ok := backend.(RemoteProxy); ok {
		names = append(names, "RemoteProxy")
	}
	sort.Strings(names)
	return names
}

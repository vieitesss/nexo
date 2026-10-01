// Package capabilities defines the small operations a backend may support.
package capabilities

import "sort"

// Names reports the capabilities implemented by backend in stable order.
func Names(backend any) []string {
	var names []string
	if _, ok := backend.(ProjectOpener); ok {
		names = append(names, "ProjectOpener")
	}
	if _, ok := backend.(WorktreeCreator); ok {
		names = append(names, "WorktreeCreator")
	}
	if _, ok := backend.(ContainerOpener); ok {
		names = append(names, "ContainerOpener")
	}
	if _, ok := backend.(ContainerLister); ok {
		names = append(names, "ContainerLister")
	}
	if _, ok := backend.(ContainerRenamer); ok {
		names = append(names, "ContainerRenamer")
	}
	if _, ok := backend.(ContainerKiller); ok {
		names = append(names, "ContainerKiller")
	}
	if _, ok := backend.(ScreenReader); ok {
		names = append(names, "ScreenReader")
	}
	if _, ok := backend.(PaneFocuser); ok {
		names = append(names, "PaneFocuser")
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

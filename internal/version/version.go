// Package version reports the nexo build version.
package version

import "runtime/debug"

// Version is replaced by release builds through -ldflags. Development builds
// use the module version when it is available and otherwise report "dev".
var Version = "dev"

// String returns the most useful version known for this binary.
func String() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

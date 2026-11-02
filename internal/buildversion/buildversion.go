// Package buildversion names the running build for issue-cli and
// issue-viewer: the -ldflags "-X main.Version=…" stamp that make install and
// the release Action set, else the module version Go records in the binary
// (`go install github.com/kacperwerema/dark-factory/cmd/issue-cli@v0.60.0`
// reports v0.60.0), else "dev".
package buildversion

import "runtime/debug"

// readBuildInfo is debug.ReadBuildInfo, swapped in tests.
var readBuildInfo = debug.ReadBuildInfo

// Resolve returns stamped when the build was stamped (anything but "" or
// "dev"); otherwise the main module's version from the build info, when Go
// recorded one ("(devel)" and "" are not one); otherwise "dev".
func Resolve(stamped string) string {
	if stamped != "" && stamped != "dev" {
		return stamped
	}
	if info, ok := readBuildInfo(); ok && info != nil {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

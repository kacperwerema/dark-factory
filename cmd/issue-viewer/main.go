// Command issue-viewer serves the Dark Factory board: the issue
// files of one project or every project in projects.yaml as web pages.
// The viewer itself is internal/viewer; this file only starts it.
package main

import (
	"fmt"
	"os"

	"github.com/kacperwerema/dark-factory/internal/buildversion"
	"github.com/kacperwerema/dark-factory/internal/viewer"
)

// Version is set at build time via -ldflags "-X main.Version=v0.X.Y";
// unstamped, main uses the module version Go recorded (a go install
// …/cmd/issue-viewer@v0.60.0 says v0.60.0), else "dev" (internal/buildversion).
var Version = "dev"

func main() {
	viewer.Version = buildversion.Resolve(Version)
	if err := viewer.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "issue-viewer:", err)
		os.Exit(1)
	}
}

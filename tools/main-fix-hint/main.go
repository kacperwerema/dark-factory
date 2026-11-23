// Command main-fix-hint is make validate's hint after a red test step: it
// reads the step's go test output and prints one `validate: main has <sha>
// <subject> touching <path>: merge main and re-run` line per commit main
// has since the branch's merge base that touches a failing test's file or
// package (ship.MainFixHints). Nothing when there is none. It always exits 0:
// the verdict stays make validate's.
//
//	go run ./tools/main-fix-hint [-main <branch>] <test output file>
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/kacperwerema/dark-factory/internal/ship"
)

func main() {
	mainRef := flag.String("main", "", "the branch main fixes land on (default: main, else master)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: main-fix-hint [-main <branch>] <test output file>")
		return
	}
	b, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		return
	}
	ref := *mainRef
	if ref == "" {
		ref = "main"
		if exec.Command("git", "rev-parse", "-q", "--verify", "refs/heads/main").Run() != nil {
			ref = "master"
		}
	}
	for _, h := range ship.MainFixHints(".", ref, string(b)) {
		fmt.Println("validate: " + h)
	}
}

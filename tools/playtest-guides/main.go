// Command playtest-guides copies the playtest guides from docs/ into the
// folder issue-cli embeds. It runs from cmd/issue-cli's go:generate line:
//
//	go generate ./cmd/issue-cli/...
//
// or by hand: go run ./tools/playtest-guides <src> <dst>.
package main

import (
	"fmt"
	"os"

	"github.com/kacperwerema/dark-factory/internal/guidesync"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: playtest-guides <docs folder> <embedded folder>")
		os.Exit(2)
	}
	changed, err := guidesync.Sync(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "playtest-guides:", err)
		os.Exit(1)
	}
	for _, n := range changed {
		fmt.Printf("playtest-guides: %s/%s\n", os.Args[2], n)
	}
}

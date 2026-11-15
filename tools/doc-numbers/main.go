// Command doc-numbers writes the docs' Numbers table from the constants. It
// runs from internal/docnumbers' go:generate line:
//
//	go generate ./internal/docnumbers/...
//
// or by hand: go run ./tools/doc-numbers docs/numbers.md.
package main

import (
	"fmt"
	"os"

	"github.com/kacperwerema/dark-factory/internal/docnumbers"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: doc-numbers <page>")
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[1], []byte(docnumbers.Render()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "doc-numbers:", err)
		os.Exit(1)
	}
}

// Package slowtier marks a Go test that runs only in local CI's slow tier
// (make test SLOW_TESTS=1, the nightly), not in make validate: a test that
// proves the same thing as a cheaper fast-tier one beside it, at whole-run
// cost (docs/testing.md, "Test tiers"). A kernel leaf.
package slowtier

import (
	"os"
	"testing"
)

// Var is set to 1 by make test SLOW_TESTS=1, which local CI's slow tier runs.
const Var = "DF_SLOW_TESTS"

// On reports whether this run is the slow tier's.
func On() bool { return os.Getenv(Var) == "1" }

// Only skips t outside the slow tier, naming why and how to run it.
func Only(t testing.TB, why string) {
	t.Helper()
	if !On() {
		t.Skipf("slow tier only (%s; make test SLOW_TESTS=1 or %s=1 go test runs it): %s", Var, Var, why)
	}
}

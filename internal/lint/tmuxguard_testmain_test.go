package lint

import (
	"os"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/rules"
	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// ownerRules is the machine's rules.md as the test binary found it, before
// TestMain pointed XDG_CONFIG_HOME away from it.
var ownerRules string

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard), and keeps checkRules off the owner's rules.md: a
// stray line in that file once turned every template's lint test red in
// every worktree. A test that wants a machine rules file sets its own
// XDG_CONFIG_HOME (rules_test.go).
func TestMain(m *testing.M) {
	ownerRules = rules.MachinePath()
	dir, err := os.MkdirTemp("", "lint-test-xdg-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	code := 0
	func() {
		defer os.RemoveAll(dir)
		cleanup := tmuxguard.Isolate()
		code = m.Run()
		cleanup()
	}()
	os.Exit(code)
}

func TestLintTestsNeverReadTheOwnersRules(t *testing.T) {
	got := rules.MachinePath()
	if got == ownerRules || !strings.Contains(got, "lint-test-xdg-") {
		t.Fatalf("lint tests read %s, not TestMain's temp config: a stray line in the owner's rules.md would turn them red", got)
	}
}

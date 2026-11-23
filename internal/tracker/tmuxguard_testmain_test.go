package tracker

import (
	"os"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard), and removes the drift tests' shared git fixtures
// (driftFixturesDir) before the guard checks the binary's temp folder.
func TestMain(m *testing.M) {
	cleanup := tmuxguard.Isolate()
	code := m.Run()
	_ = os.RemoveAll(driftFixturesDir())
	cleanup()
	os.Exit(code)
}

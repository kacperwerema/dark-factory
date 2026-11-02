package agentrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain points XDG_STATE_HOME at an empty directory so no test reads or
// writes this machine's subscription state (routing) or auto-start budget.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agentrun-test-xdg-")
	if err == nil {
		os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	}
	// Every tmux the tests start reaches a private server (internal/tmuxguard).
	cleanupTmux := tmuxguard.Isolate()
	code := m.Run()
	cleanupTmux()
	if err == nil {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

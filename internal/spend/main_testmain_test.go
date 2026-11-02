package spend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain points CODEX_HOME at an empty directory so no test reads this
// machine's Codex session logs.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "spend-test-")
	if err == nil {
		os.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
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

package spend

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points CODEX_HOME at an empty directory so no test reads this
// machine's Codex session logs.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "spend-test-")
	if err == nil {
		os.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
	}
	code := m.Run()
	if err == nil {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

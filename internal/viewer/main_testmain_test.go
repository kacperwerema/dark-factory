package viewer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestMain points XDG_DATA_HOME at an empty directory so no test reads this
// machine's queued report-bug files (Status page and sidebar counts), and
// XDG_STATE_HOME too so a test viewer never overwrites the real viewer.json.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "issue-viewer-test-xdg-")
	if err == nil {
		os.Setenv("XDG_DATA_HOME", dir)
		os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
		// No test reads the owner's Codex session logs.
		os.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
		// No test reads the owner's Claude Code transcripts.
		os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	}
	// No test reads a real tmux pane for limits or agents (routing).
	scanPaneLimits = func(*tracker.Project, string, time.Time) []agentrun.PaneLimit { return nil }
	sessionAgent = func(*tracker.Project, string) string { return "" }
	// Every tmux the tests start reaches a private server (internal/tmuxguard).
	cleanupTmux := tmuxguard.Isolate()
	code := m.Run()
	cleanupTmux()
	if err == nil {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

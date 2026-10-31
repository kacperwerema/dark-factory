package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain gives the tests their own state and config homes and drops an
// inherited $ISSUE_VIEWER_CONFIG, so the config lookup never finds this
// machine's viewer.json, ~/.config projects.yaml or the viewer's config.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "issue-cli-test-xdg-")
	if err == nil {
		os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
		// No test reads the owner's Codex session logs.
		os.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
		os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
		// No test reads the owner's Claude Code transcripts (issue-cli spend).
		os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	}
	os.Unsetenv("ISSUE_VIEWER_CONFIG")
	code := m.Run()
	if err == nil {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

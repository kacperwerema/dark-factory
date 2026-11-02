package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain gives the tests their own state and config homes and drops an
// inherited $ISSUE_VIEWER_CONFIG, so the config lookup never finds this
// machine's viewer.json, ~/.config projects.yaml or the viewer's config.
func TestMain(m *testing.M) {
	// A test that needs issue-cli as a program (a git hook calling it) puts
	// a stub on PATH that runs this test binary with ISSUE_CLI_TEST_AS_CLI=1.
	if os.Getenv("ISSUE_CLI_TEST_AS_CLI") == "1" {
		err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
		if err != nil {
			if msg := err.Error(); msg != "" {
				os.Stderr.WriteString(msg + "\n")
			}
			os.Exit(processExitCode(err))
		}
		os.Exit(0)
	}
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
	// Every tmux the tests start reaches a private server (internal/tmuxguard).
	cleanupTmux := tmuxguard.Isolate()
	code := m.Run()
	cleanupTmux()
	if err == nil {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

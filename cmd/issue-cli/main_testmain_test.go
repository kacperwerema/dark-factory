package main

import (
	"github.com/kacperwerema/dark-factory/internal/leaks"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestMain gives the tests their own state and config homes and drops an
// inherited $ISSUE_VIEWER_CONFIG, so the config lookup never finds this
// machine's viewer.json, ~/.config projects.yaml or the viewer's config.
func TestMain(m *testing.M) {
	// A test that needs issue-cli as a program (a git hook calling it) puts
	// a stub on PATH that runs this test binary with ISSUE_CLI_TEST_AS_CLI=1.
	// A stand-in issue-viewer for viewer restart's tests (cmd_viewer_test.go).
	if os.Getenv("ISSUE_CLI_TEST_AS_VIEWER") == "1" {
		standInViewer(os.Args[1:])
	}
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
	// ISSUE_CLI_TEST_AS_MAIN=1 runs it as the shipped binary does, through
	// main: the clilog record and telemetry too (bench smoke's stand-in
	// worker is checked by its clilog calls).
	if os.Getenv("ISSUE_CLI_TEST_AS_MAIN") == "1" {
		// lead_test.go's init drops the lead scope, in a child too: a
		// child that runs as a lead names it here.
		if scope := os.Getenv("ISSUE_CLI_TEST_LEAD_SCOPE"); scope != "" {
			os.Setenv("ISSUE_VIEWER_LEAD_SCOPE", scope)
		}
		main()
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "issue-cli-test-xdg-")
	if err == nil {
		os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
		os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	}
	os.Unsetenv("ISSUE_VIEWER_CONFIG")
	// The lead tick's daily doctor leaks would read this machine's
	// processes: a test that wants a finding stubs findLeaks itself.
	findLeaks = func(leaks.Options) leaks.Report { return leaks.Report{} }
	// Nor the shared server's lead sessions and the viewer's RSS readings:
	// a test that wants a long-running finding stubs longReport.
	longReport = func([]*tracker.Project, time.Time) leaks.LongReport { return leaks.LongReport{} }
	// Every tmux the tests start reaches a private server, and no test reads
	// the owner's Codex or Claude Code folders (internal/tmuxguard).
	cleanupTmux := tmuxguard.Isolate()
	code := m.Run()
	cleanupTmux()
	if err == nil {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

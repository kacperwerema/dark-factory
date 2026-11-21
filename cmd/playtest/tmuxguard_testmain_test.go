package main

import (
	"os"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard). PLAYTEST_TEST_RUNNER makes the binary a runner that
// starts one browser and waits to be stopped (stop_test.go). The binaries
// buildTestBinaries built go after m.Run, before the guard looks for
// leftovers in TMPDIR.
func TestMain(m *testing.M) {
	if helperRunner() {
		return
	}
	cleanup := tmuxguard.Isolate()
	code := m.Run()
	if testBinsDir != "" {
		_ = os.RemoveAll(testBinsDir)
	}
	cleanup()
	os.Exit(code)
}

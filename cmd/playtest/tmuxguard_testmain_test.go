package main

import (
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard). PLAYTEST_TEST_RUNNER makes the binary a runner that
// starts one browser and waits to be stopped (stop_test.go).
func TestMain(m *testing.M) {
	if helperRunner() {
		return
	}
	tmuxguard.Main(m)
}

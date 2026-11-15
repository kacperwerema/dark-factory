package viewer

import (
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/recent"
)

// TestViewerKeepsItsMachineReadingByDefault: a production viewer has no
// OffEnv, and any value but "1" leaves its 500 ms alone.
func TestViewerKeepsItsMachineReadingByDefault(t *testing.T) {
	for _, value := range []string{"", "0"} {
		t.Setenv(recent.OffEnv, value)
		if got := machineReadingWindow(); got != 500*time.Millisecond {
			t.Errorf("%s=%q: window = %v, want 500ms", recent.OffEnv, value, got)
		}
	}
}

// TestOffEnvTurnsTheViewersReadingOff is the viewer's half of what
// TestScenarioEnvTurnsOffTheMachineReading pins on the scenario's side.
func TestOffEnvTurnsTheViewersReadingOff(t *testing.T) {
	t.Setenv(recent.OffEnv, "1")
	if got := machineReadingWindow(); got != 0 {
		t.Errorf("window = %v, want 0", got)
	}
}

package docref

import (
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain: no test reads this machine's Codex or Claude Code session logs,
// and every tmux reaches a private server (internal/tmuxguard).
func TestMain(m *testing.M) { tmuxguard.Main(m) }

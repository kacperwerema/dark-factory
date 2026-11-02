package playtestruns

import (
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard).
func TestMain(m *testing.M) { tmuxguard.Main(m) }

package privacy

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard), and sets gitEnvVars once for the whole binary with
// HOME and XDG_CONFIG_HOME on a folder of its own, so a parallel test (the
// release-flow ones, which cannot call t.Setenv) runs git as gitEnv's does.
func TestMain(m *testing.M) {
	cleanup := tmuxguard.Isolate()
	// TMPDIR is the binary's own folder now (tmuxguard), so a fixed name.
	home := filepath.Join(os.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for k, v := range gitEnvVars(home) {
		os.Setenv(k, v)
	}
	// The only go these tests start is CheckBuild's, on a stand-in module
	// with no go.sum (TestPickReportsABranchThatNoLongerBuilds).
	os.Setenv("GOTOOLCHAIN", "local")
	os.Setenv("GOFLAGS", "-mod=mod")
	code := m.Run()
	_ = os.RemoveAll(home)
	cleanup()
	os.Exit(code)
}

package tmuxguard_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// A pane's processes outlive `tmux kill-server`: it closes the panes and
// returns, and on a loaded machine a pane still writes into its test's
// t.TempDir while the test's cleanup removes it. TestNewcomerWalkthrough
// failed internal/viewer that way on 2026-11-21 ("viewer.test left
// TestNewcomerWalkthrough<N> in TMPDIR"). Here the pane ignores SIGHUP and
// writes a second after the kill: once KillServer returns, nothing of the
// server writes any more.
func TestKillServerLeavesNothingRunning(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	sockDir, err := os.MkdirTemp("/tmp", "tmg-") // tmux's ~104-byte socket path limit
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "s")
	started, late := filepath.Join(dir, "started"), filepath.Join(dir, "late")
	script := `trap "" HUP; : > ` + started + `; sleep 1; echo late > ` + late
	start := exec.Command("tmux", "-S", sock, "new-session", "-d", "sh", "-c", script)
	start.Env = tmuxguard.EnvWithoutTMUX()
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(started); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatal("the pane never started")
	}
	tmuxguard.KillServer(sock, tmuxguard.EnvWithoutTMUX())
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(late); err == nil {
		t.Error("the pane wrote after KillServer returned: its process outlived the server")
	}
}

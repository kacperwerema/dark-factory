package leaks

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard). LEAKS_HELPER makes the binary a stand-in: "sleep"
// waits (its arguments may look like Chrome's), "daemon" starts a detached
// sleeping copy of itself, writes its pid to LEAKS_PIDFILE and exits, so
// the copy's parent becomes pid 1 (codex app-server-daemon's shape).
func TestMain(m *testing.M) {
	switch os.Getenv("LEAKS_HELPER") {
	case "sleep":
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	case "daemon":
		cmd := exec.Command(os.Args[0], os.Args[1:]...)
		cmd.Env = append(os.Environ(), "LEAKS_HELPER=sleep")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if cmd.Start() != nil {
			os.Exit(2)
		}
		f := os.Getenv("LEAKS_PIDFILE")
		_ = os.WriteFile(f+".tmp", []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
		_ = os.Rename(f+".tmp", f)
		os.Exit(0)
	}
	tmuxguard.Main(m)
}

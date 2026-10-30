//go:build unix

package bench

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cmd in its own process group and makes its cancel end the
// whole group, so a stopped worker leaves no tool process behind.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

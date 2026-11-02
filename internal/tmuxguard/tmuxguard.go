// Package tmuxguard keeps a test binary off the developer's tmux server.
//
// Every package's TestMain calls Main (or Isolate around its own m.Run).
// Before any test runs, TMUX and TMUX_PANE leave the process's environment
// and TMUX_TMPDIR points at a fresh folder under /tmp, so every tmux the
// tests start reaches a private server: the test helpers, the code under
// test, scripts and built binaries the tests run, and a child started with a
// nil Env. That last one is the shape that killed the shared server on
// 2026-10-31 and 2026-11-01: cmd/playtest built a Scenario with only Root,
// its command inherited the worker's $TMUX and `tmux kill-server` went to
// every lead and worker on the machine. TestEveryTestBinaryDropsTMUX (repo
// root) fails on a test package without it.
package tmuxguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Isolate drops TMUX and TMUX_PANE from this process's environment and
// points TMUX_TMPDIR at a fresh folder under /tmp (not os.TempDir: under
// macOS's deep temp dir a socket path passes the ~104-byte limit). The
// cleanup it returns kills that folder's server by its -S socket, with TMUX
// removed from the kill's environment, and removes the folder.
func Isolate() (cleanup func()) {
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")
	dir, err := os.MkdirTemp("/tmp", "tmg-")
	if err != nil {
		// No private folder: still never the developer's server. A tmux
		// started now finds no TMUX and the default socket of an unused name.
		os.Setenv("TMUX_TMPDIR", filepath.Join("/tmp", "tmg-none-"+strconv.Itoa(os.Getpid())))
		return func() {}
	}
	os.Setenv("TMUX_TMPDIR", dir)
	return func() {
		sock := Socket(dir)
		if _, err := os.Stat(sock); err == nil {
			kill := exec.Command("tmux", "-S", sock, "kill-server")
			kill.Env = EnvWithoutTMUX()
			_ = kill.Run()
		}
		_ = os.RemoveAll(dir)
	}
}

// Main runs the tests isolated and exits with their code.
func Main(m interface{ Run() int }) {
	cleanup := Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// Socket is where tmux puts its default socket under a TMUX_TMPDIR.
func Socket(tmpdir string) string {
	return filepath.Join(tmpdir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
}

// EnvWithoutTMUX is this process's environment with TMUX and TMUX_PANE
// removed.
func EnvWithoutTMUX() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") && !strings.HasPrefix(kv, "TMUX_PANE=") {
			env = append(env, kv)
		}
	}
	return env
}

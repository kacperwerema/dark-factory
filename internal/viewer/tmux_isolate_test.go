package viewer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// isolateTmux points tmux at a private server for the test so it never
// touches the developer's real sessions, and kills that server afterwards.
// The code under test finds the server through TMUX_TMPDIR; the test itself
// talks to it only through testTmux, which names the socket with -S, and the
// cleanup kills it the same way with TMUX removed from its environment
// (~/CLAUDE.md: never kill-server without env -u TMUX and your own socket;
// TestTmuxCallsInTestsNameTheirSocket). Agents start in $SHELL as a login
// shell; /bin/sh keeps the developer's shell profile out of it.
func isolateTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX", "")
	// Not t.TempDir(): under macOS's deep temp dir the socket path passes
	// the ~104-byte limit and tmux can't connect.
	dir, err := os.MkdirTemp("/tmp", "tmx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("SHELL", "/bin/sh")
	sock := testTmuxSocket(t)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", sock, "kill-server")
		kill.Env = envWithoutTMUX()
		_ = kill.Run()
		_ = os.RemoveAll(dir)
	})
}

// testTmuxSocket is the socket of the test's private server: where tmux
// puts its default socket under the TMUX_TMPDIR isolateTmux set.
func testTmuxSocket(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" || !strings.HasPrefix(dir, "/tmp/") {
		t.Fatalf("testTmux without isolateTmux (TMUX_TMPDIR=%q)", dir)
	}
	return filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
}

// testTmux is a tmux command on the test's private server, named with -S.
func testTmux(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("tmux", append([]string{"-S", testTmuxSocket(t)}, args...)...)
	cmd.Env = envWithoutTMUX()
	return cmd
}

// envWithoutTMUX is this process's environment with TMUX removed.
func envWithoutTMUX() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") {
			env = append(env, kv)
		}
	}
	return env
}

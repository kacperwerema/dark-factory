package procs_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/procs"
	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// The test binary is its own stand-in: macOS gives no environment for
// Apple's binaries (/bin/sh, /bin/sleep), and a daemon a real CLI detaches
// is a binary of its own. PROCS_HELPER=daemon starts a detached copy of
// itself (setsid, its own session, the parent exits at once: the shape of
// codex app-server-daemon) and writes the child's pid to PROCS_PIDFILE;
// PROCS_HELPER=sleep just sleeps.
func TestMain(m *testing.M) {
	switch os.Getenv("PROCS_HELPER") {
	case "sleep":
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	case "parent":
		// Names PROCS_DIR on its command line and in CODEX_HOME, as the
		// shell running `CODEX_HOME=<dir> go test` does, and starts a child
		// that stops every process of that home.
		cmd := exec.Command(os.Args[0], os.Getenv("PROCS_DIR"))
		cmd.Env = append(os.Environ(), "PROCS_HELPER=stophomes")
		if err := cmd.Run(); err != nil {
			os.Exit(3)
		}
		os.Exit(0) // still alive to say so
	case "stophomes":
		procs.StopHomes([]string{os.Getenv("PROCS_DIR")}, time.Second)
		os.Exit(0)
	case "daemon":
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "PROCS_HELPER=sleep")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		pidfile := os.Getenv("PROCS_PIDFILE")
		_ = os.WriteFile(pidfile+".tmp", []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
		_ = os.Rename(pidfile+".tmp", pidfile)
		os.Exit(0)
	}
	tmuxguard.Main(m)
}

// startDaemon runs the daemonizing stand-in with env and returns the
// detached child's pid, stopping it at the end of the test.
func startDaemon(t *testing.T, env ...string) int {
	t.Helper()
	pidfile := filepath.Join(t.TempDir(), "pid")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(append(os.Environ(), "PROCS_HELPER=daemon", "PROCS_PIDFILE="+pidfile), env...)
	if err := cmd.Run(); err != nil {
		t.Fatalf("stand-in: %v", err)
	}
	var pid int
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(pidfile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("stand-in wrote no pid")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

func TestEnvReadsAnotherProcesssEnvironment(t *testing.T) {
	pid := startDaemon(t, "PROCS_MARK=m-42")
	var got string
	for i := 0; i < 100 && got == ""; i++ {
		got = procs.Getenv(pid, "PROCS_MARK")
		time.Sleep(20 * time.Millisecond)
	}
	if got != "m-42" {
		t.Fatalf("Getenv(PROCS_MARK) = %q, want m-42", got)
	}
}

// TestStopHomesStopsADaemonizedStandIn is the codex app-server-daemon
// shape: a process that detached into its own session, whose parent is
// gone, and whose only tie to the temp home is CODEX_HOME in its
// environment (nothing on its command line).
func TestStopHomesStopsADaemonizedStandIn(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, "agent-cli", "codex")
	pid := startDaemon(t, "CODEX_HOME="+codexHome)
	other := startDaemon(t, "CODEX_HOME="+filepath.Join(t.TempDir(), "elsewhere"))
	if !procs.Alive(pid) {
		t.Fatal("stand-in daemon is not running")
	}
	// The env is readable only once exec has finished.
	for i := 0; i < 100 && procs.Getenv(pid, "CODEX_HOME") == ""; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	stopped := procs.StopHomes([]string{home}, 2*time.Second)
	if procs.Alive(pid) {
		t.Fatalf("daemon %d with CODEX_HOME under %s still alive (stopped %v)", pid, home, stopped)
	}
	if !procs.Alive(other) {
		t.Fatalf("StopHomes stopped %d, whose home is elsewhere", other)
	}
}

func TestParsePS(t *testing.T) {
	out := "  101     1   501 12-19:25:39 /Applications/Google Chrome --user-data-dir=/tmp/x --headless\n" +
		"  202   101   501       10:27 sh -c sleep 5\n" +
		"garbage\n"
	got := procs.ParsePS(out)
	if len(got) != 2 {
		t.Fatalf("got %d procs, want 2: %+v", len(got), got)
	}
	if got[0].PID != 101 || got[0].PPID != 1 || got[0].UID != 501 || got[0].Command != "/Applications/Google Chrome --user-data-dir=/tmp/x --headless" {
		t.Errorf("first = %+v", got[0])
	}
	if want := 12*24*time.Hour + 19*time.Hour + 25*time.Minute + 39*time.Second; got[0].Age != want {
		t.Errorf("age = %v, want %v", got[0].Age, want)
	}
	if got[1].Age != 10*time.Minute+27*time.Second || got[1].Command != "sh -c sleep 5" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestUnderHome(t *testing.T) {
	env := []string{"CODEX_HOME=/tmp/tmg-1/agent-cli/codex", "CLAUDE_CONFIG_DIR=/Users/x/.claude"}
	if v, ok := procs.UnderHome(env, "/tmp/tmg-1"); !ok || v != "CODEX_HOME" {
		t.Errorf("UnderHome = %q %v", v, ok)
	}
	if _, ok := procs.UnderHome(env, "/tmp/tmg"); ok {
		t.Error("/tmp/tmg matched /tmp/tmg-1 by prefix")
	}
}

// TestStopHomesSparesItsAncestors: the shell (and go test) that started a
// run names the home on its command line and in its environment; stopping
// it would kill the worker that ran the tests.
func TestStopHomesSparesItsAncestors(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], dir)
	cmd.Env = append(os.Environ(), "PROCS_HELPER=parent", "PROCS_DIR="+dir, "CODEX_HOME="+dir)
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the parent did not finish cleanly (stopped by its child?): %v", err)
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("parent hung")
	}
}

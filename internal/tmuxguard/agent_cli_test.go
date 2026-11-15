package tmuxguard_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// guardRunVar makes this test binary a run of its own: Isolate, one launch,
// cleanup. The tests below read how that run ends, as a go test run would.
const guardRunVar = "TMUXGUARD_TEST_RUN"

// The ways a launch can find its CLI, each one a way PATH was set.
const (
	viaProcessPath = "process PATH"
	viaLoginShell  = "tmux -e PATH, lost in the login shell"
	viaStartLine   = "PATH in the start command"
	viaAbsolute    = "absolute path"
	viaStandIn     = "a stand-in, PATH in the start command"
)

// guardedRun is the run: GUARD_AGENT launched the GUARD_WAY way under the
// guard. PATH holds the stand-ins a test would set up (GUARD_STANDINS); the
// CLI that acts like the real one sits in GUARD_REAL, which only ~/.profile
// puts first, as an owner's rc files do.
func guardedRun() {
	cleanup := tmuxguard.Isolate()
	agent, real, standins := os.Getenv("GUARD_AGENT"), os.Getenv("GUARD_REAL"), os.Getenv("GUARD_STANDINS")
	withPath := func(dir string) string { return "PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH") }

	sock := filepath.Join(os.Getenv("TMUX_TMPDIR"), "s")
	tmux := func(a ...string) ([]byte, error) {
		cmd := exec.Command("tmux", append([]string{"-S", sock}, a...)...)
		cmd.Env = tmuxguard.EnvWithoutTMUX()
		return cmd.CombinedOutput()
	}
	// broke ends a run that couldn't launch: exit 3, not the guard's 1. The
	// server goes first, as it does at the end of a run.
	broke := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, format, a...)
		tmux("kill-server")
		cleanup()
		os.Exit(3)
	}

	pane := func(args ...string) {
		if out, err := tmux(append([]string{"new-session", "-d", "-s", "a", "-e", withPath(standins)}, args...)...); err != nil {
			broke("tmux: %v: %s\n", err, out)
		}
		// The launch's record, or the stand-in's: whichever ran.
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(os.Getenv("GUARD_OUT"), "seen")); err == nil {
				return
			}
		}
		pane, _ := tmux("capture-pane", "-p", "-t", "a")
		broke("no launch recorded:\n%s\n", pane)
	}

	switch os.Getenv("GUARD_WAY") {
	case viaProcessPath:
		// exec.Command looks the name up on this process's PATH.
		os.Setenv("PATH", strings.TrimPrefix(withPath(real), "PATH="))
		exec.Command(agent).Run()
	case viaAbsolute:
		exec.Command(filepath.Join(real, agent)).Run()
	case viaStartLine:
		pane("/bin/sh", "-c", "PATH="+real+":$PATH; "+agent+" go; sleep 60")
	case viaStandIn:
		pane("/bin/sh", "-c", "PATH="+standins+":$PATH; "+agent+" go; sleep 60")
	case viaLoginShell:
		pane(agentrun.AgentCommand(agent + " go; sleep 60")...)
	}
	tmux("kill-server")
	cleanup()
	os.Exit(0)
}

// actsReal is a CLI as measured from Claude Code 2.1.318 and Codex 0.163.0:
// a write into its home at start, before any sign-in, and for codex an
// app-server daemon out of that home, which outlives the pane. It records
// the home it used.
func actsReal(homeVar, fallback string) string {
	return `#!/bin/sh
home=${` + homeVar + `:-$HOME/` + fallback + `}
mkdir -p "$home"
: > "$home/installation_id"
if [ "` + homeVar + `" = CODEX_HOME ]; then
  printf 'while :; do sleep 1; done\n' > "$home/daemon.sh"
  nohup /bin/sh "$home/daemon.sh" </dev/null >/dev/null 2>&1 &
  echo $! > "$GUARD_OUT/daemon"
fi
printf %s "$home" > "$GUARD_OUT/seen.tmp"
mv "$GUARD_OUT/seen.tmp" "$GUARD_OUT/seen"
`
}

// standIn is what a test puts on PATH: it touches no home.
const standIn = `#!/bin/sh
printf stand-in > "$GUARD_OUT/seen.tmp"
mv "$GUARD_OUT/seen.tmp" "$GUARD_OUT/seen"
`

type guardRun struct {
	out, stderr string
	failed      bool
}

// seen is the home the launched CLI recorded ("stand-in" for a stand-in).
func (r guardRun) seen(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.out, "seen"))
	if err != nil {
		t.Fatalf("nothing launched: %v\n%s", err, r.stderr)
	}
	return string(b)
}

// runGuarded runs the binary as a run of its own, with this machine's PATH
// (and any real CLI on it) behind the two test folders, and extra env last.
func runGuarded(t *testing.T, agent, way string, env ...string) guardRun {
	t.Helper()
	root := t.TempDir()
	real, standins, home, out := filepath.Join(root, "real"), filepath.Join(root, "standins"), filepath.Join(root, "home"), filepath.Join(root, "out")
	for _, d := range []string{real, standins, home, out} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string, mode os.FileMode) {
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(real, "claude"), actsReal("CLAUDE_CONFIG_DIR", ".claude"), 0o755)
	write(filepath.Join(real, "codex"), actsReal("CODEX_HOME", ".codex"), 0o755)
	write(filepath.Join(standins, agent), standIn, 0o755)
	write(filepath.Join(home, ".profile"), "PATH="+real+":$PATH; export PATH\n", 0o644)
	t.Cleanup(func() {
		if b, err := os.ReadFile(filepath.Join(out, "daemon")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

	cmd := exec.Command(os.Args[0])
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k != "CHECK_REAL_CODEX" && k != "CODEX_HOME" && k != "CLAUDE_CONFIG_DIR" {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, guardRunVar+"=1", "GUARD_AGENT="+agent, "GUARD_WAY="+way, "GUARD_REAL="+real,
		"GUARD_STANDINS="+standins, "GUARD_OUT="+out, "HOME="+home, "SHELL=/bin/sh", "ENV=")
	cmd.Env = append(cmd.Env, env...)
	stderr, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		t.Fatalf("run broke: %v\n%s", err, stderr)
	}
	return guardRun{out: out, stderr: string(stderr), failed: err != nil}
}

// A run that launches a real CLI fails and names it, whichever way PATH was
// set, and no daemon of its stays behind. The real thing here is a script
// that acts like one: this test must not start what it forbids. Each run is
// a child binary with its own folders, private tmux server and /tmp/tmg-
// folder, and nothing here changes this process's env, so they run at once.
func TestNoTestReachesARealAgentCLI(t *testing.T) {
	t.Parallel()
	for _, agent := range []string{"claude", "codex"} {
		for _, way := range []string{viaProcessPath, viaLoginShell, viaStartLine, viaAbsolute} {
			t.Run(agent+"/"+way, func(t *testing.T) {
				t.Parallel()
				r := runGuarded(t, agent, way)
				if got := r.seen(t); got == "stand-in" {
					t.Fatal("the stand-in ran: this way doesn't reach the real one, so it proves nothing")
				}
				if !r.failed {
					t.Fatalf("the run passed:\n%s", r.stderr)
				}
				if !strings.Contains(r.stderr, "reached the real "+agent) {
					t.Errorf("the failure doesn't name %s:\n%s", agent, r.stderr)
				}
				if agent == "codex" {
					waitDaemonGone(t, r.out)
				}
			})
		}
	}
}

// The stand-in a test means to run passes: the guard reads what a real CLI
// does, not the name in the pane.
func TestStandInsPassTheGuard(t *testing.T) {
	t.Parallel()
	r := runGuarded(t, "codex", viaStandIn)
	if got := r.seen(t); got != "stand-in" {
		t.Fatalf("launched %q, want the stand-in", got)
	}
	if r.failed {
		t.Fatalf("a stand-in failed the run:\n%s", r.stderr)
	}
}

// CHECK_REAL_CODEX=1 gives codex the owner's home back, so its login works
// and a TestRealCodex* run passes; without it the same launch never sees that
// home. The switch opens codex only: claude stays guarded.
func TestRealCLIRunsNeedTheirSwitch(t *testing.T) {
	t.Parallel()
	owner := filepath.Join(t.TempDir(), "owner-codex")
	if err := os.MkdirAll(owner, 0o755); err != nil {
		t.Fatal(err)
	}
	login := filepath.Join(owner, "installation_id")

	r := runGuarded(t, "codex", viaLoginShell, "CODEX_HOME="+owner)
	if got := r.seen(t); got == owner || !r.failed {
		t.Fatalf("no switch: home %q, failed %v, want a guarded home and a failed run", got, r.failed)
	}
	if _, err := os.Stat(login); err == nil {
		t.Fatal("no switch: the owner's home was written")
	}

	r = runGuarded(t, "codex", viaLoginShell, "CODEX_HOME="+owner, "CHECK_REAL_CODEX=1")
	if got := r.seen(t); got != owner {
		t.Fatalf("with the switch codex used %q, want the owner's %q", got, owner)
	}
	if r.failed {
		t.Fatalf("with the switch the run failed:\n%s", r.stderr)
	}
	if _, err := os.Stat(login); err != nil {
		t.Fatalf("the real codex never reached its home: %v", err)
	}

	r = runGuarded(t, "claude", viaLoginShell, "CHECK_REAL_CODEX=1")
	if !r.failed || !strings.Contains(r.stderr, "reached the real claude") {
		t.Fatalf("CHECK_REAL_CODEX let claude through: failed %v\n%s", r.failed, r.stderr)
	}
}

func waitDaemonGone(t *testing.T, out string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(out, "daemon"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	t.Fatalf("the daemon (pid %d) is still running", pid)
}

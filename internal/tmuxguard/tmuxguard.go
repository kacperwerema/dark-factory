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
// root) fails on a test package without it. The worker's issue-cli env goes
// too (ISSUE_CLI_LOG, ISSUE_VIEWER_*): a test's calls never land in the
// worker's clilog or pick up its issue slug. Every git the binary starts
// runs with maintenance.auto and gc.auto off (quietGit).
//
// The real agent CLIs are kept off the owner's subscription the same way.
// PATH can't do it: a pane's login shell rebuilds PATH from the owner's rc
// files, so a stand-in set with `tmux -e PATH` loses to the real codex. The
// environment past PATH survives, so each CLI's home points at an empty
// folder here: it finds no login and stops at sign-in, and anything it writes
// at start shows up in that folder, which fails the run (agentCLIs).
package tmuxguard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Isolate drops TMUX and TMUX_PANE from this process's environment and
// points TMUX_TMPDIR at a fresh folder under /tmp (not os.TempDir: under
// macOS's deep temp dir a socket path passes the ~104-byte limit). The
// cleanup it returns kills that folder's server by its -S socket, with TMUX
// removed from the kill's environment, and removes the folder. It exits the
// binary with status 1 when a test reached a real agent CLI, whatever m.Run
// returned: call it after m.Run.
func Isolate() (cleanup func()) {
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")
	dropWorkerEnv()
	quietGit()
	SweepStale("/tmp", time.Now())
	dir, err := os.MkdirTemp("/tmp", "tmg-")
	if err != nil {
		// No private folder: still never the developer's server. A tmux
		// started now finds no TMUX and the default socket of an unused name.
		none := filepath.Join("/tmp", "tmg-none-"+strconv.Itoa(os.Getpid()))
		os.Setenv("TMUX_TMPDIR", none)
		guardAgentCLIs(none)
		return func() {}
	}
	os.Setenv("TMUX_TMPDIR", dir)
	guarded := guardAgentCLIs(dir)
	return func() {
		sock := Socket(dir)
		if _, err := os.Stat(sock); err == nil {
			kill := exec.Command("tmux", "-S", sock, "kill-server")
			kill.Env = EnvWithoutTMUX()
			_ = kill.Run()
		}
		reached := reachedAgentCLIs(guarded)
		if len(reached) > 0 {
			stopProcessesIn(dir)
		}
		_ = os.RemoveAll(dir)
		if len(reached) > 0 {
			fmt.Fprintf(os.Stderr, "tmuxguard: a test reached the real %s (no login, so nothing was spent). "+
				"A stand-in in a pane needs PATH inside its start command (PATH=<bin>:$PATH; trap : INT; codex go): "+
				"tmux -e PATH doesn't survive the pane's login shell.\n", strings.Join(reached, " and the real "))
			os.Exit(1)
		}
	}
}

// agentCLIs are the real CLIs a test must not reach: the variable that names
// each one's home, and the switch that lets a test run through to the real
// one (no switch: nothing may, and adding one is a decision).
var agentCLIs = []struct{ name, homeVar, realSwitch string }{
	{name: "claude", homeVar: "CLAUDE_CONFIG_DIR"},
	{name: "codex", homeVar: "CODEX_HOME", realSwitch: "CHECK_REAL_CODEX"},
}

// guardedCLI is one CLI whose home Isolate replaced.
type guardedCLI struct{ name, home string }

// guardAgentCLIs points each CLI's home at an empty folder under dir and
// returns them. The folder has to exist: codex refuses a CODEX_HOME that
// doesn't, and would then write nothing to see.
func guardAgentCLIs(dir string) []guardedCLI {
	var guarded []guardedCLI
	for _, cli := range agentCLIs {
		if cli.realSwitch != "" && os.Getenv(cli.realSwitch) != "" {
			continue
		}
		home := filepath.Join(dir, "agent-cli", cli.name)
		_ = os.MkdirAll(home, 0o700)
		os.Setenv(cli.homeVar, home)
		guarded = append(guarded, guardedCLI{cli.name, home})
	}
	return guarded
}

// reachedAgentCLIs names the CLIs that wrote into their guarded home, and
// what they wrote. Both
// write there within a second of starting, before any sign-in (read from
// Claude Code 2.1.318 and Codex 0.163.0). A test's own transcripts go under
// a t.Setenv home, never this one.
func reachedAgentCLIs(guarded []guardedCLI) []string {
	var reached []string
	for _, g := range guarded {
		if entries, err := os.ReadDir(g.home); err == nil && len(entries) > 0 {
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.Name()
			}
			reached = append(reached, g.name+" (it wrote "+strings.Join(names, ", ")+")")
		}
	}
	return reached
}

// stopProcessesIn stops what a real CLI left running from a guarded folder:
// codex starts an app-server daemon out of its home, which outlives the pane
// and its tmux server.
func stopProcessesIn(dir string) {
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		pid, command, _ := strings.Cut(strings.TrimSpace(line), " ")
		n, err := strconv.Atoi(pid)
		if err != nil || n == os.Getpid() || !strings.Contains(command, dir) {
			continue
		}
		_ = syscall.Kill(n, syscall.SIGTERM)
	}
}

// quietGitConfig is what every git a test binary starts reads, as if from
// its config: git commit, merge and fetch detach a `git maintenance run
// --auto` (and gc --auto) that can still be writing .git/objects when
// t.TempDir's cleanup runs ("directory not empty").
var quietGitConfig = [][2]string{{"maintenance.auto", "false"}, {"gc.auto", "0"}}

// quietGit adds quietGitConfig to this process's environment through
// GIT_CONFIG_COUNT / GIT_CONFIG_KEY_n / GIT_CONFIG_VALUE_n, after any
// entries the caller already set, so every child git (a test helper's, the
// code under test's, a script's) inherits it.
func quietGit() {
	n, err := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	if err != nil || n < 0 {
		n = 0
	}
	for _, kv := range quietGitConfig {
		os.Setenv("GIT_CONFIG_KEY_"+strconv.Itoa(n), kv[0])
		os.Setenv("GIT_CONFIG_VALUE_"+strconv.Itoa(n), kv[1])
		n++
	}
	os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(n))
}

// dropWorkerEnv removes what a worker's shell carries for issue-cli:
// ISSUE_CLI_LOG (a test's calls would land in the worker's clilog) and every
// ISSUE_VIEWER_* (its issue slug, lead scope, project and viewer config).
// A test that wants one sets it itself.
func dropWorkerEnv() {
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if WorkerEnv(k) {
			os.Unsetenv(k)
		}
	}
}

// WorkerEnv reports whether an environment variable is one dropWorkerEnv
// removes.
func WorkerEnv(key string) bool {
	return key == "ISSUE_CLI_LOG" || strings.HasPrefix(key, "ISSUE_VIEWER_")
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

// staleFolders are the private tmux folders the test helpers make under
// /tmp: this package's, isolateTmux's (internal/agentrun, internal/viewer)
// and the check-script library's private_tmux (marked by its file). Each one
// is removed on every normal exit, but a run that was killed (a go test
// timeout, SIGKILL, a closed terminal) leaves its server running for good:
// seven such servers lived 8 to 14 days on the owner's Mac. Age is how old
// a folder must be before a later run takes it for left behind; no test
// binary runs for hours, and a guided check script may wait on its owner
// for a while, so its folders get a day.
var staleFolders = []struct {
	prefix, marker string
	age            time.Duration
}{
	{prefix: "tmg-", age: 2 * time.Hour},
	{prefix: "tmx-", age: 2 * time.Hour},
	{prefix: "ckt-", marker: ".checks-private-tmux", age: 24 * time.Hour},
}

// SweepStale kills the tmux server of each stale folder under root (by its
// -S socket, TMUX removed from the kill's environment; never the default
// server) and removes the folder. Only folders this user owns, older than
// their age, and carrying their marker when they have one. It returns the
// folders it removed.
func SweepStale(root string, now time.Time) []string {
	var removed []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		for _, f := range staleFolders {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), f.prefix) {
				continue
			}
			dir := filepath.Join(root, e.Name())
			info, err := os.Lstat(dir)
			if err != nil || !info.IsDir() || now.Sub(info.ModTime()) < f.age {
				continue
			}
			if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
				continue
			}
			if f.marker != "" {
				if _, err := os.Stat(filepath.Join(dir, f.marker)); err != nil {
					continue
				}
			}
			if sock := Socket(dir); fileExists(sock) {
				kill := exec.Command("tmux", "-S", sock, "kill-server")
				kill.Env = EnvWithoutTMUX()
				_ = kill.Run()
			}
			if os.RemoveAll(dir) == nil {
				removed = append(removed, dir)
			}
		}
	}
	return removed
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

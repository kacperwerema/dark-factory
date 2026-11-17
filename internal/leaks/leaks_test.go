package leaks

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
	"github.com/kacperwerema/dark-factory/internal/restrace"
	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// noHomes clears the CLI homes the test binary got from tmuxguard (temp
// folders), so a stand-in is no agent-home finding of its own.
var noHomes = []string{"CODEX_HOME=", "CLAUDE_CONFIG_DIR="}

// daemon starts the detached stand-in with env and args and returns its
// pid once its parent is launchd and its environment is readable.
func daemon(t *testing.T, args []string, env ...string) int {
	t.Helper()
	pidfile := filepath.Join(t.TempDir(), "pid")
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(append(os.Environ(), "LEAKS_HELPER=daemon", "LEAKS_PIDFILE="+pidfile), env...)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	var pid int
	for i := 0; i < 200 && pid == 0; i++ {
		if b, err := os.ReadFile(pidfile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		} else {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if pid == 0 {
		t.Fatal("no daemon pid")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	// Its environment is readable once its exec is done.
	for i := 0; i < 200 && procs.Getenv(pid, "LEAKS_HELPER") != "sleep"; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	return pid
}

func find(fs []Finding, pid int) *Finding {
	for i := range fs {
		if fs[i].PID == pid {
			return &fs[i]
		}
	}
	return nil
}

// TestDoctorFindsADetachedDaemonOfATempHome: a codex daemon left from a
// sandbox's home is listed with its pid, age and stop command, and is not
// stopped until Stop is called.
func TestDoctorFindsADetachedDaemonOfATempHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "real", "chome")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	pid := daemon(t, nil, "CODEX_HOME="+home)
	// Seconds old: a live test's helper looks the same.
	r := Find(Options{TmpRoot: t.TempDir(), TracePath: filepath.Join(t.TempDir(), "none.jsonl")})
	if find(r.Leaks, pid) != nil {
		t.Fatalf("a daemon %v old counted as left behind", procs.DetachedMinAge)
	}
	prev := procs.DetachedMinAge
	procs.DetachedMinAge = 0
	t.Cleanup(func() { procs.DetachedMinAge = prev })
	r = Find(Options{TmpRoot: t.TempDir(), TracePath: filepath.Join(t.TempDir(), "none.jsonl")})
	f := find(r.Leaks, pid)
	if f == nil {
		t.Fatalf("daemon %d not listed: %+v", pid, r.Leaks)
	}
	if f.Kind != KindAgentHome || f.StopCommand != "kill "+strconv.Itoa(pid) || !strings.Contains(f.What, home) {
		t.Errorf("finding = %+v", f)
	}
	if !procs.Alive(pid) {
		t.Fatal("Find stopped the daemon: only --stop may")
	}
	if err := f.Stop(); err != nil || procs.Alive(pid) {
		t.Fatalf("Stop: %v, alive %v", err, procs.Alive(pid))
	}
}

// TestUnderFindsEveryProcessOfASandboxHome: a check script's sandbox at
// its exit takes every process of its homes, whatever its parent.
func TestUnderFindsEveryProcessOfASandboxHome(t *testing.T) {
	sandbox := t.TempDir()
	pid := daemon(t, nil, "CLAUDE_CONFIG_DIR="+filepath.Join(sandbox, "claude"))
	other := daemon(t, nil, "CODEX_HOME=", "CLAUDE_CONFIG_DIR="+filepath.Join(t.TempDir(), "claude"))
	r := Find(Options{Under: sandbox})
	if find(r.Leaks, pid) == nil || find(r.Leaks, other) != nil {
		t.Fatalf("under %s: %+v (want %d, not %d)", sandbox, r.Leaks, pid, other)
	}
}

// TestDoctorFindsAnOrphanPlaytestChrome: a "Chrome" whose profile names a
// dead runner.
func TestDoctorFindsAnOrphanPlaytestChrome(t *testing.T) {
	profile := filepath.Join(os.TempDir(), procs.PlaytestProfilePrefix+strconv.Itoa(1<<30)+"-leakstest")
	pid := daemon(t, []string{"--no-first-run", "--user-data-dir=" + profile, "--headless"}, noHomes...)
	r := Find(Options{TmpRoot: t.TempDir(), TracePath: filepath.Join(t.TempDir(), "none.jsonl")})
	f := find(r.Leaks, pid)
	if f == nil || f.Kind != KindChrome || !strings.Contains(f.Why, "runner") {
		t.Fatalf("orphan Chrome %d not listed: %+v", pid, r.Leaks)
	}
}

// TestDoctorFindsAKilledTestsTmuxServer: a tmg- folder older than a run
// lasts, its server still up.
func TestDoctorFindsAKilledTestsTmuxServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	root, err := os.MkdirTemp("/tmp", "lk-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := filepath.Join(root, "tmg-killed")
	sock := tmuxguard.Socket(dir)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	start := exec.Command("tmux", "-S", sock, "new-session", "-d", "-s", "x", "sleep 120")
	start.Env = tmuxguard.EnvWithoutTMUX()
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("tmux: %v %s", err, out)
	}
	t.Cleanup(func() {
		k := exec.Command("tmux", "-S", sock, "kill-server")
		k.Env = tmuxguard.EnvWithoutTMUX()
		_ = k.Run()
	})
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	r := Find(Options{TmpRoot: root, TracePath: filepath.Join(t.TempDir(), "none.jsonl")})
	var f *Finding
	for i := range r.Leaks {
		if r.Leaks[i].Kind == KindTmux {
			f = &r.Leaks[i]
		}
	}
	if f == nil || f.PID == 0 || !strings.Contains(f.StopCommand, "env -u TMUX tmux -S "+sock+" kill-server") {
		t.Fatalf("stale server not listed right: %+v", r.Leaks)
	}
	if err := f.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the stale folder is still there after Stop")
	}
}

// TestDoctorListsTracedResourcesStillAlive: with a trace, a start with no
// stop whose pid lives is listed; a stopped one and a reused pid are not.
func TestDoctorListsTracedResourcesStillAlive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.jsonl")
	pid := daemon(t, nil, noHomes...)
	stopped := daemon(t, nil, noHomes...)
	now := time.Now()
	for _, e := range []restrace.Event{
		{At: now.Add(-time.Hour), Event: "start", Kind: restrace.KindChild, PID: pid, Owner: "issue-cli ci run", Detail: "ci runner"},
		{At: now.Add(-time.Hour), Event: "start", Kind: restrace.KindChild, PID: stopped, Owner: "x"},
		{At: now.Add(-time.Hour), Event: "stop", Kind: restrace.KindChild, PID: stopped},
		// Started "after" the process: its pid was reused.
		{At: now.Add(time.Hour), Event: "start", Kind: restrace.KindChild, PID: os.Getppid(), Owner: "y"},
	} {
		if err := restrace.Append(path, e); err != nil {
			t.Fatal(err)
		}
	}
	r := Find(Options{TmpRoot: t.TempDir(), TracePath: path})
	if !r.TraceRead || len(r.Traced) != 1 || r.Traced[0].PID != pid || !r.Traced[0].Traced || !strings.Contains(r.Traced[0].Why, "issue-cli ci run") {
		t.Fatalf("traced = %+v", r.Traced)
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		12*24*time.Hour + 19*time.Hour: "12d19h", 10*time.Hour + 27*time.Minute: "10h27m", 5 * time.Minute: "5m", 40 * time.Second: "40s",
	} {
		if got := Age(d); got != want {
			t.Errorf("Age(%v) = %s, want %s", d, got, want)
		}
	}
}

// TestDoctorFindsAProcessWhoseTempHomeIsGone: no age needed when the home
// folder is gone (the sandbox or scratchpad was removed).
func TestDoctorFindsAProcessWhoseTempHomeIsGone(t *testing.T) {
	home := filepath.Join(t.TempDir(), "chome")
	pid := daemon(t, nil, "CODEX_HOME="+home, "CLAUDE_CONFIG_DIR=")
	r := Find(Options{TmpRoot: t.TempDir(), TracePath: filepath.Join(t.TempDir(), "none.jsonl")})
	if f := find(r.Leaks, pid); f == nil || !strings.Contains(f.Why, "no longer exists") {
		t.Fatalf("daemon %d of a gone home not listed: %+v", pid, r.Leaks)
	}
}

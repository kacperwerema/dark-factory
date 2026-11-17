// Package procs reads this machine's processes: the list (ps), one
// process's environment and whether a pid is still alive, and stops one.
// It is how the factory finds what it started and left running: a playtest
// Chrome whose runner died, a codex app-server daemon whose CODEX_HOME was a
// test's temp home (internal/leaks, internal/tmuxguard, cmd/playtest).
//
// macOS's ps no longer prints another process's environment (ps -E shows
// none on Darwin 25), so Env reads it from the kernel: sysctl
// kern.procargs2 on macOS, /proc/<pid>/environ on Linux. macOS hands it
// over only for this user's processes that aren't Apple's own binaries
// (/bin/sh, /bin/sleep come back with no environment); codex, claude and
// node are not Apple's, so their homes are readable.
package procs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Proc is one process as ps lists it.
type Proc struct {
	PID, PPID, UID int
	// Age is how long it has run (ps etime).
	Age time.Duration
	// Command is the full command line, arguments included.
	Command string
}

// ErrNoEnv: the process's environment can't be read here (another user's,
// an Apple binary on macOS, or a system with neither sysctl nor /proc).
var ErrNoEnv = errors.New("environment not readable")

// ps runs ps; a var for tests.
var ps = func() (string, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=", "-o", "ppid=", "-o", "uid=", "-o", "etime=", "-o", "command=").Output()
	return string(out), err
}

// List is every process on the machine.
func List() ([]Proc, error) {
	out, err := ps()
	if err != nil {
		return nil, err
	}
	return ParsePS(out), nil
}

// Mine is List narrowed to this user's processes, without this one.
func Mine() ([]Proc, error) {
	all, err := List()
	if err != nil {
		return nil, err
	}
	uid, self := os.Getuid(), os.Getpid()
	var out []Proc
	for _, p := range all {
		if p.UID == uid && p.PID != self {
			out = append(out, p)
		}
	}
	return out, nil
}

// ParsePS reads `ps -o pid= -o ppid= -o uid= -o etime= -o command=`.
// Lines that don't parse are skipped.
func ParsePS(out string) []Proc {
	var procs []Proc
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		uid, err3 := strconv.Atoi(f[2])
		age, ok := ParseEtime(f[3])
		if err1 != nil || err2 != nil || err3 != nil || !ok {
			continue
		}
		// The command is the rest of the line as ps printed it.
		rest := strings.TrimLeft(line, " \t")
		for i := 0; i < 4; i++ {
			rest = strings.TrimLeft(rest[len(strings.Fields(rest)[0]):], " \t")
		}
		procs = append(procs, Proc{PID: pid, PPID: ppid, UID: uid, Age: age, Command: rest})
	}
	return procs
}

// ParseEtime reads ps's elapsed time, [[dd-]hh:]mm:ss.
func ParseEtime(s string) (time.Duration, bool) {
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	secs := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, false
		}
		secs = secs*60 + n
	}
	return time.Duration(days)*24*time.Hour + time.Duration(secs)*time.Second, true
}

// Env is pid's environment as KEY=value entries.
func Env(pid int) ([]string, error) { return readEnv(pid) }

// Getenv is one variable of pid's environment, "" when it is unset or the
// environment can't be read.
func Getenv(pid int, key string) string {
	env, err := Env(pid)
	if err != nil {
		return ""
	}
	return Lookup(env, key)
}

// Lookup finds key in KEY=value entries; the last one wins, as in exec.
func Lookup(env []string, key string) string {
	val := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			val = v
		}
	}
	return val
}

// Alive reports whether pid is a live process (one of another user's
// counts too).
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Stop sends pid SIGTERM, waits up to grace for it to go and then sends
// SIGKILL. It reports whether the process is gone.
func Stop(pid int, grace time.Duration) bool {
	if pid <= 1 || pid == os.Getpid() {
		return false
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return !Alive(pid)
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !Alive(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	for i := 0; i < 20 && Alive(pid); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	return !Alive(pid)
}

// HomeVars are the variables that name an agent CLI's home.
var HomeVars = []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"}

// UnderHome reports whether pid's CODEX_HOME or CLAUDE_CONFIG_DIR is dir or
// a folder inside it, and which variable matched.
func UnderHome(env []string, dir string) (string, bool) {
	dir = strings.TrimRight(dir, "/")
	if dir == "" {
		return "", false
	}
	for _, v := range HomeVars {
		h := strings.TrimRight(Lookup(env, v), "/")
		if h != "" && (h == dir || strings.HasPrefix(h, dir+"/")) {
			return v, true
		}
	}
	return "", false
}

// StopHomes stops every process of this user whose CODEX_HOME or
// CLAUDE_CONFIG_DIR is inside one of dirs, or whose command line names one
// (a CLI started with its home as an argument). It returns the pids it
// stopped. A daemon an agent CLI detached (codex app-server-daemon) has
// its home only in its environment, so the command line alone misses it.
// This process's ancestors are never stopped: the shell that ran
// `CODEX_HOME=<dir> go test …` names the folder on its own command line
// and carries it in its environment.
func StopHomes(dirs []string, grace time.Duration) []int {
	mine, err := Mine()
	if err != nil {
		return nil
	}
	all, _ := List()
	skip := Ancestors(all, os.Getpid())
	var targets []int
	for _, p := range mine {
		if !skip[p.PID] && matchesHome(p, dirs) {
			targets = append(targets, p.PID)
		}
	}
	var stopped []int
	for _, pid := range targets {
		if Stop(pid, grace) {
			stopped = append(stopped, pid)
		}
	}
	return stopped
}

// Ancestors is pid's parent chain in procs (pid itself included).
func Ancestors(procs []Proc, pid int) map[int]bool {
	parent := map[int]int{}
	for _, p := range procs {
		parent[p.PID] = p.PPID
	}
	seen := map[int]bool{}
	for pid > 1 && !seen[pid] {
		seen[pid] = true
		pid = parent[pid]
	}
	return seen
}

func matchesHome(p Proc, dirs []string) bool {
	env, envErr := Env(p.PID)
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if strings.Contains(p.Command, d) {
			return true
		}
		if envErr == nil {
			if _, ok := UnderHome(env, d); ok {
				return true
			}
		}
	}
	return false
}

// TempPlace reports whether path is a throwaway folder: under the system
// temp folders (/tmp, /private/tmp, /var/folders, $TMPDIR) or an agent
// session's scratchpad. A CLI home there belongs to a test, a check script
// or a hand-made sandbox, never to the owner's own login.
func TempPlace(path string) bool {
	if path == "" {
		return false
	}
	p := filepath.Clean(path)
	roots := []string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders"}
	if t := strings.TrimRight(os.TempDir(), "/"); t != "" {
		roots = append(roots, t)
	}
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return strings.Contains(p, "/scratchpad/") || strings.HasSuffix(p, "/scratchpad")
}

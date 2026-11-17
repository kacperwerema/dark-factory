package procs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PlaytestProfilePrefix starts the name of every Chrome profile folder the
// playtest runner makes (cmd/playtest): dark-factory-playtest-<runner
// pid>-<random>, in the temp folder. The pid tells a live run's Chrome from
// one whose runner was killed.
const PlaytestProfilePrefix = "dark-factory-playtest-"

// ChromedpProfilePrefix is chromedp's own name for a profile it makes,
// which the runner used before it named its own: it carries no runner pid.
const ChromedpProfilePrefix = "chromedp-runner"

// DetachedMinAge is how long a process must have run, parented by launchd
// (pid 1), before it counts as left behind when nothing else says its
// owner is gone: a live test's Chrome and CLI helpers detach too (Chrome's
// crashpad handler), for seconds. The codex daemon this was first for had
// run 10 days. It guards a chromedp Chrome here and a temp CLI home's
// process in internal/leaks. A var for tests.
var DetachedMinAge = 10 * time.Minute

// UserDataDir is the --user-data-dir of a Chrome command line, "" when it
// has none.
func UserDataDir(command string) string {
	const flag = "--user-data-dir="
	i := strings.Index(command, flag)
	if i < 0 {
		return ""
	}
	v := command[i+len(flag):]
	if j := strings.Index(v, " --"); j >= 0 {
		v = v[:j]
	}
	return strings.Trim(strings.TrimSpace(v), `"'`)
}

// ChromeMain reports whether command is a Chrome browser process, not one
// of its helpers (renderer, GPU, utility: those carry --type=).
func ChromeMain(command string) bool {
	return UserDataDir(command) != "" && !strings.Contains(command, " --type=")
}

// PlaytestRunner is the runner pid a playtest profile folder names.
func PlaytestRunner(profile string) (int, bool) {
	base := filepath.Base(profile)
	if !strings.HasPrefix(base, PlaytestProfilePrefix) {
		return 0, false
	}
	pid, _, ok := strings.Cut(strings.TrimPrefix(base, PlaytestProfilePrefix), "-")
	n, err := strconv.Atoi(pid)
	if !ok || err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ChromeOrphan is a playtest Chrome whose runner is gone.
type ChromeOrphan struct {
	Proc
	Profile string
	// Why says how it was told apart from a live run's.
	Why string
}

// OrphanChromes are the playtest Chromes in procs whose runner is gone:
// a profile under PlaytestProfilePrefix whose runner pid is dead, or one of
// chromedp's own profiles (an older runner's) whose parent has been launchd
// (pid 1) for DetachedMinAge or more. A live run's Chrome is the child of
// its runner and names it.
func OrphanChromes(procs []Proc) []ChromeOrphan {
	var out []ChromeOrphan
	for _, p := range procs {
		if !ChromeMain(p.Command) {
			continue
		}
		profile := UserDataDir(p.Command)
		base := filepath.Base(profile)
		switch {
		case strings.HasPrefix(base, PlaytestProfilePrefix):
			if runner, ok := PlaytestRunner(profile); ok && !Alive(runner) {
				out = append(out, ChromeOrphan{Proc: p, Profile: profile, Why: "its playtest runner (pid " + strconv.Itoa(runner) + ") is gone"})
			}
		case strings.HasPrefix(base, ChromedpProfilePrefix) && p.PPID == 1 && p.Age >= DetachedMinAge:
			out = append(out, ChromeOrphan{Proc: p, Profile: profile, Why: "a chromedp profile whose runner is gone (parent is pid 1 for " + p.Age.Round(time.Second).String() + ")"})
		}
	}
	return out
}

// StaleProfiles are playtest profile folders in dir whose runner is dead
// and which no live Chrome in procs uses.
func StaleProfiles(dir string, procs []Proc) []string {
	inUse := map[string]bool{}
	for _, p := range procs {
		if d := UserDataDir(p.Command); d != "" {
			inUse[filepath.Clean(d)] = true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), PlaytestProfilePrefix) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if runner, ok := PlaytestRunner(path); ok && !Alive(runner) && !inUse[filepath.Clean(path)] {
			out = append(out, path)
		}
	}
	return out
}

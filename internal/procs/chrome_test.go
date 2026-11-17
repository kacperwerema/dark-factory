package procs_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/procs"
)

const chromeBin = "/Apps/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"

func TestUserDataDirAndChromeMain(t *testing.T) {
	main := chromeBin + " --no-first-run --user-data-dir=/tmp/dark-factory-playtest-12-abc --headless=new"
	helper := chromeBin + " --type=renderer --user-data-dir=/tmp/dark-factory-playtest-12-abc --lang=en"
	if got := procs.UserDataDir(main); got != "/tmp/dark-factory-playtest-12-abc" {
		t.Errorf("UserDataDir = %q", got)
	}
	if !procs.ChromeMain(main) || procs.ChromeMain(helper) || procs.ChromeMain("sleep 5") {
		t.Error("ChromeMain picks the wrong processes")
	}
	if pid, ok := procs.PlaytestRunner("/tmp/dark-factory-playtest-12-abc"); !ok || pid != 12 {
		t.Errorf("PlaytestRunner = %d %v", pid, ok)
	}
	if _, ok := procs.PlaytestRunner("/tmp/chromedp-runner123"); ok {
		t.Error("a chromedp profile names no runner")
	}
}

// TestOrphanChromes: a playtest profile whose runner is dead, and an old
// chromedp profile parented by launchd, are orphans; a live runner's
// Chrome and its helpers are not.
func TestOrphanChromes(t *testing.T) {
	self := os.Getpid()
	dead := 1 << 30 // no such pid
	list := []procs.Proc{
		{PID: 101, PPID: 1, Command: chromeBin + " --user-data-dir=/tmp/dark-factory-playtest-" + itoa(dead) + "-x --headless"},
		{PID: 102, PPID: 101, Command: chromeBin + " --type=gpu-process --user-data-dir=/tmp/dark-factory-playtest-" + itoa(dead) + "-x"},
		{PID: 103, PPID: self, Command: chromeBin + " --user-data-dir=/tmp/dark-factory-playtest-" + itoa(self) + "-y --headless"},
		{PID: 104, PPID: 1, Age: time.Hour, Command: chromeBin + " --user-data-dir=/var/folders/T/chromedp-runner2725150430 --headless"},
		{PID: 105, PPID: 555, Command: chromeBin + " --user-data-dir=/var/folders/T/chromedp-runner999 --headless"},
	}
	got := procs.OrphanChromes(list)
	if len(got) != 2 || got[0].PID != 101 || got[1].PID != 104 {
		t.Fatalf("orphans = %+v, want 101 and 104", got)
	}
}

// TestChromedpOrphanNeedsAge: a chromedp Chrome parented by launchd counts
// as an orphan only once it has been detached DetachedMinAge: a live test's
// Chrome detaches to pid 1 too, for seconds (crashpad).
func TestChromedpOrphanNeedsAge(t *testing.T) {
	cmd := chromeBin + " --user-data-dir=/var/folders/T/chromedp-runner42 --headless"
	young := procs.Proc{PID: 201, PPID: 1, Age: 5 * time.Second, Command: cmd}
	edge := procs.Proc{PID: 202, PPID: 1, Age: procs.DetachedMinAge - time.Second, Command: cmd}
	old := procs.Proc{PID: 203, PPID: 1, Age: procs.DetachedMinAge, Command: cmd}
	got := procs.OrphanChromes([]procs.Proc{young, edge, old})
	if len(got) != 1 || got[0].PID != 203 {
		t.Fatalf("orphans = %+v, want only 203 (detached %v)", got, procs.DetachedMinAge)
	}
	if !strings.Contains(got[0].Why, "pid 1 for 10m0s") {
		t.Errorf("Why = %q, want the age it has been detached", got[0].Why)
	}
}

// TestChromedpPrefixDefinedOnce: chromedp's profile name is spelled in one
// place, procs.ChromedpProfilePrefix; other code uses the constant.
func TestChromedpPrefixDefinedOnce(t *testing.T) {
	root := filepath.Join("..", "..")
	var hits []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".worktrees", "issues", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), `"`+procs.ChromedpProfilePrefix+`"`) {
			hits = append(hits, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.HasSuffix(hits[0], "internal/procs/chrome.go") {
		t.Fatalf("the %q literal is in %v, want only internal/procs/chrome.go: use procs.ChromedpProfilePrefix", procs.ChromedpProfilePrefix, hits)
	}
}

func TestStaleProfiles(t *testing.T) {
	dir := t.TempDir()
	deadP := filepath.Join(dir, "dark-factory-playtest-"+itoa(1<<30)+"-a")
	liveP := filepath.Join(dir, "dark-factory-playtest-"+itoa(os.Getpid())+"-b")
	for _, d := range []string{deadP, liveP, filepath.Join(dir, "other")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got := procs.StaleProfiles(dir, nil)
	if len(got) != 1 || got[0] != deadP {
		t.Fatalf("stale = %v, want %s", got, deadP)
	}
}

func TestTempPlace(t *testing.T) {
	for p, want := range map[string]bool{
		"/tmp/tmg-1/agent-cli/codex":                         true,
		"/private/var/folders/v_/x/T/TestX/001":              true,
		"/home/someone/repo/x/.claude/scratchpad/real/chome": true,
		"/home/someone/.codex":                               false,
		"":                                                   false,
	} {
		if procs.TempPlace(p) != want {
			t.Errorf("TempPlace(%q) = %v", p, !want)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

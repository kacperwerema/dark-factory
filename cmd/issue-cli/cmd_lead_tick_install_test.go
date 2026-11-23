package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/leaks"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// installLagProject is a tick project on an engine-shaped repository (it
// has cmd/issue-cli) whose build is behind commits behind main with fixes,
// the running issue-cli in /opt/df/bin and no leaks.
func installLagProject(t *testing.T, behind *int, fixes *[]string) *tracker.Project {
	t.Helper()
	proj, _, _ := tickProject(t)
	root := filepath.Dir(proj.IssueDir)
	queueGit(t, root, "init", "-q", "-b", "main")
	queueGit(t, root, "config", "maintenance.auto", "false")
	queueGit(t, root, "config", "gc.auto", "0")
	queueGit(t, root, "commit", "-q", "--allow-empty", "-m", "base")
	if err := os.MkdirAll(filepath.Join(root, "cmd", "issue-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	origBehind, origFixes, origExe, origLeaks, origVersion := tracker.BuildBehind, installLagFixes, installLagExe, findLeaks, tracker.BuildVersion
	tracker.BuildVersion = "v0.62.0-rc.1-174-g27afdabb"
	tracker.BuildBehind = func(string, string, string) int { return *behind }
	installLagFixes = func(string, string, string) []string { return *fixes }
	installLagExe = func() string { return "/opt/df/bin/issue-cli" }
	findLeaks = func(leaks.Options) leaks.Report { return leaks.Report{} }
	t.Cleanup(func() {
		tracker.BuildBehind, installLagFixes, installLagExe, findLeaks, tracker.BuildVersion = origBehind, origFixes, origExe, origLeaks, origVersion
	})
	return proj
}

// stripTickTime drops the "[15:04] " a --exit-on-news watch puts in front
// of a line it held from an earlier tick.
func stripTickTime(line string) string {
	if len(line) > 8 && line[0] == '[' && line[6:8] == "] " {
		return line[8:]
	}
	return line
}

func notes(out string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(stripTickTime(line), "note: the installed issue-cli") {
			got = append(got, line)
		}
	}
	return got
}

// More than 10 commits behind main: one note a local day naming the count,
// up to 5 fix subjects (+N more) and the install command; 10 or fewer, or
// a project that isn't the engine, says nothing.
func TestLeadTickNotesAnInstallLagOncePerDay(t *testing.T) {
	behind := 10
	fixes := []string{"fix(lead): a", "fix(cli): b", "fix: c", "fix(tracker): d", "fix(jobs): e", "fix(lint): f", "fix(procs): g"}
	proj := installLagProject(t, &behind, &fixes)
	stubStart(t)
	day := time.Date(2026, 11, 16, 9, 0, 0, 0, time.Local)

	out, err := leadCmd(t, proj, day, "tick")
	if err != nil || len(notes(out)) != 0 {
		t.Fatalf("10 behind: %v\n%s", err, out)
	}

	behind = 37
	out, _ = leadCmd(t, proj, day.Add(5*time.Hour), "tick")
	got := notes(out)
	if len(got) != 1 {
		t.Fatalf("37 behind, want one note:\n%s", out)
	}
	root := filepath.Dir(proj.IssueDir)
	real, _ := filepath.EvalSymlinks(root)
	want := "note: the installed issue-cli (v0.62.0-rc.1-174-g27afdabb) is 37 commits behind main; 7 shipped fixes wait on an install: " +
		"fix(lead): a; fix(cli): b; fix: c; fix(tracker): d; fix(jobs): e (+2 more); install: cd " + tildeHome(real) + " && GOBIN=/opt/df/bin make install && issue-cli viewer restart"
	if got[0] != want {
		t.Fatalf("note:\n got %s\nwant %s", got[0], want)
	}

	// Same day: not again. Next day: again, and with no fix among them
	// the count and the command still go out.
	if out, _ := leadCmd(t, proj, day.Add(8*time.Hour), "tick"); len(notes(out)) != 0 {
		t.Fatalf("twice in a day:\n%s", out)
	}
	fixes = nil
	out, _ = leadCmd(t, proj, day.AddDate(0, 0, 1), "tick")
	if got := notes(out); len(got) != 1 || !strings.Contains(got[0], "37 commits behind main; no shipped fix to issue-cli among them; install: cd ") {
		t.Fatalf("next day:\n%s", out)
	}

	// Not the engine's repository: nothing.
	os.RemoveAll(filepath.Join(root, "cmd"))
	if out, _ := leadCmd(t, proj, day.AddDate(0, 0, 2), "tick"); len(notes(out)) != 0 {
		t.Fatalf("no cmd/issue-cli:\n%s", out)
	}
}

// Under --exit-on-news the note is no news: a watch whose ticks have
// nothing else keeps going, and the first news goes out with the note
// above it. The note names the install and runs nothing.
func TestInstallLagNoteIsHeldWithTheNextNews(t *testing.T) {
	behind := 37
	fixes := []string{"fix(lead): the tick exits on news only"}
	proj := installLagProject(t, &behind, &fixes)
	stubStart(t)
	for i := 0; i < 7; i++ {
		// Enough open items that the queue says nothing; the first start
		// is held, the gap holds the rest.
		name := fmt.Sprintf("more-%d", i)
		writeIssue(t, proj, name, "backlog")
		mustQ(t, proj, "add", name, "--quote", queueWords)
	}
	clearGap()
	root := filepath.Dir(proj.IssueDir)
	ticks := 0
	orig := leadTickSleep
	leadTickSleep = func(time.Duration) bool {
		ticks++
		switch ticks {
		case 1:
		case 2:
			wf := "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\nci:\n  run:\n    - \"true\"\n"
			os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644)
			ciDir := filepath.Join(root, ".ci")
			os.MkdirAll(ciDir, 0o755)
			os.WriteFile(filepath.Join(ciDir, "state.json"), []byte(`{"branch":"main","last_sha":"deadbeefcafe","red_since":"deadbeefcafe","failed":"make validate","state":"red"}`), 0o644)
		default:
			t.Errorf("tick %d: should have exited on the red CI", ticks)
			return false
		}
		return true
	}
	t.Cleanup(func() { leadTickSleep = orig })
	out := tick(t, proj, "--watch", "--exit-on-news", "--every", "1m")
	if ticks != 2 {
		t.Fatalf("exited after %d sleeps (the note woke it?):\n%s", ticks, out)
	}
	note, ci := strings.Index(out, "note: the installed issue-cli"), strings.Index(out, "ci: main red since deadbee: make validate")
	if len(notes(out)) != 1 || ci < 0 || note > ci {
		t.Fatalf("want the note once, above the news:\n%s", out)
	}
	assertContains(t, out, "ci: main red since deadbee: make validate")
	assertContains(t, notes(out)[0], "1 shipped fix waits on an install: fix(lead): the tick exits on news only; install: ")
	if _, err := os.Stat(filepath.Join(root, "Makefile")); err == nil {
		t.Fatal("a Makefile appeared")
	}
}

// installLagFixes reads main's history: fix commits the build lacks that
// touch issue-cli's code, newest first; a feat, a viewer fix, a merge and
// a fix the build already has stay out.
func TestInstallLagFixesReadTheHistory(t *testing.T) {
	root := t.TempDir()
	queueGit(t, root, "init", "-q", "-b", "main")
	queueGit(t, root, "config", "maintenance.auto", "false")
	queueGit(t, root, "config", "gc.auto", "0")
	commit := func(path, msg string) {
		t.Helper()
		p := filepath.Join(root, path)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(msg), 0o644)
		queueGit(t, root, "add", path)
		queueGit(t, root, "commit", "-q", "-m", msg)
	}
	commit("internal/tracker/a.go", "fix(tracker): already installed")
	queueGit(t, root, "tag", "v1.0.0")
	commit("cmd/issue-cli/a.go", "fix(cli): one")
	commit("internal/viewer/a.go", "fix(viewer): needs a restart")
	commit("internal/tracker/b.go", "feat(tracker): not a fix")
	commit("docs/a.md", "fix(docs): not code")
	queueGit(t, root, "checkout", "-q", "-b", "side")
	commit("internal/lint/a.go", "fix: two")
	queueGit(t, root, "checkout", "-q", "main")
	queueGit(t, root, "merge", "-q", "--no-ff", "side", "-m", "fix(merge): a merge")

	fixes := installLagFixes(root, "v1.0.0-dirty", "main")
	sort.Strings(fixes) // one second apart at most: the date order may tie
	if got := strings.Join(fixes, " | "); got != "fix(cli): one | fix: two" {
		t.Fatalf("fixes: %s", strings.Join(fixes, " | "))
	}
}

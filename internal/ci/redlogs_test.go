package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// flaky fails the first time it runs and passes after, like a test that
// fails only under load: the marker lives outside the CI worktree.
func flaky(t *testing.T) string {
	mark := filepath.Join(t.TempDir(), "ran")
	return "if [ -f " + mark + " ]; then echo fine; else touch " + mark + "; echo '--- FAIL: TestBackgroundWait'; exit 1; fi"
}

// A red run on a head, re-tested green on the same head (a manual ci run),
// used to overwrite .ci/runs/<sha>.log: the failure's log was gone.
func TestRedLogSurvivesTheNextGreenOnTheSameHead(t *testing.T) {
	root := newRepo(t)
	c := cfg(cmd(flaky(t)))
	if runs, _, err := Execute(Options{Root: root, Config: c}); err != nil || runs[0].State != Red {
		t.Fatalf("first run: %v %v", runs, err)
	}
	if runs, _, err := Execute(Options{Root: root, Config: c}); err != nil || runs[0].State != Green {
		t.Fatalf("second run: %v %v", runs, err)
	}
	st := mustState(t, root)
	if st.State != Green || st.LastRedLog == "" {
		t.Fatalf("state = %+v", st)
	}
	data, err := os.ReadFile(filepath.Join(root, st.LastRedLog))
	if err != nil || !strings.Contains(string(data), "TestBackgroundWait") {
		t.Fatalf("kept log %s: %v\n%s", st.LastRedLog, err, data)
	}
	if st.LastRedSha == "" || st.LastRedAt.IsZero() {
		t.Errorf("last red sha/at missing: %+v", st)
	}
	kept := RedLogs(root)
	if len(kept) != 1 || !kept[0].First || kept[0].Path != st.LastRedLog {
		t.Errorf("RedLogs = %+v", kept)
	}
}

// A green run on the next commit keeps the red one's log too, and the run
// record names its kept copy.
func TestRedLogSurvivesAGreenOnTheNextCommit(t *testing.T) {
	root := newRepo(t)
	c := cfg(cmd(flaky(t)))
	runs, _, _ := Execute(Options{Root: root, Config: c})
	red := runs[0]
	commit(t, root, "next")
	if runs, _, err := Execute(Options{Root: root, Config: c}); err != nil || runs[0].State != Green {
		t.Fatalf("green run: %v %v", runs, err)
	}
	st := mustState(t, root)
	if st.LastRedSha != red.Sha {
		t.Errorf("last red sha = %q, want %q", st.LastRedSha, red.Sha)
	}
	var rec *Run
	for _, r := range RecentRuns(root, 0) {
		if r.Sha == red.Sha {
			rec = &r
		}
	}
	if rec == nil || rec.KeptLog == "" || rec.KeptLog != st.LastRedLog {
		t.Fatalf("red run record = %+v, state log %q", rec, st.LastRedLog)
	}
	if _, err := os.Stat(filepath.Join(root, rec.KeptLog)); err != nil {
		t.Error(err)
	}
}

// Kept logs go by count and by age; the newest first red of a streak stays
// past the count, but not past the age.
func TestRedLogsPrunedByCountAndAge(t *testing.T) {
	root := newRepo(t)
	if err := ensureDir(root); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	keep := func(i int, at time.Time, first bool) string {
		t.Helper()
		sha := fmt.Sprintf("%07x0", i+1)
		run := &Run{Sha: sha, Tier: defaultTier, Finished: at, Log: RelLog(sha, defaultTier)}
		writeFile(t, filepath.Join(root, run.Log), "log "+sha)
		rel, err := keepRedLog(root, run, first)
		if err != nil {
			t.Fatal(err)
		}
		return rel
	}
	old := keep(0, now.Add(-(maxRedLogAge + time.Hour)), false)
	firstRed := keep(1, now.Add(-10*24*time.Hour), true)
	var newest []string
	for i := 0; i < keepRedLogs+3; i++ {
		newest = append(newest, keep(2+i, now.Add(-time.Duration(keepRedLogs+3-i)*time.Hour), false))
	}
	pruneRedLogs(root, now)

	exists := func(rel string) bool { _, err := os.Stat(filepath.Join(root, rel)); return err == nil }
	if exists(old) {
		t.Error("a log past the age limit was kept")
	}
	if !exists(firstRed) {
		t.Error("the newest first red was pruned by count")
	}
	for i, rel := range newest {
		want := i >= 3 // the three oldest of the newest batch go by count
		if exists(rel) != want {
			t.Errorf("%s exists=%v, want %v", rel, !want, want)
		}
	}
	if got := len(RedLogs(root)); got != keepRedLogs+1 {
		t.Errorf("kept %d logs, want %d", got, keepRedLogs+1)
	}

	// Past the age limit the first red goes too.
	pruneRedLogs(root, now.Add(maxRedLogAge))
	if exists(firstRed) {
		t.Error("the first red outlived the age limit")
	}
}

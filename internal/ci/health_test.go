package ci

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/testresults"
)

// storeRun writes a CI run record with results of kind, as Execute would;
// tests != nil also keeps the per-test list.
func storeRun(t *testing.T, root, sha string, finished time.Time, kind string, sum testresults.Summary, tests []testresults.Test) {
	t.Helper()
	if tests != nil {
		list := filepath.Join(".ci", "results", sha, kind+".tests.json")
		storeJSON(t, filepath.Join(root, list), tests)
		sum.List = list
	}
	run := Run{Sha: sha, Branch: "main", Tier: "default", Started: finished.Add(-time.Minute), Finished: finished, State: Green,
		Results: map[string]testresults.Summary{kind: sum}}
	storeJSON(t, filepath.Join(RunsDir(root), sha+".json"), run)
}

func storeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, _ := json.Marshal(v)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// What health's tests family gets from local CI: the newest run at or before
// the snapshot's time that wrote results of the kind, with its test list.
func TestHealthTestRunIsTheNewestResultsUpToNow(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	tests := []testresults.Test{{Name: "TestA", Part: "cmd/playtest", Area: "cmd", Seconds: 39, Outcome: "pass"}}
	storeRun(t, root, "aaa", now.Add(-48*time.Hour), "unit", testresults.Summary{Tests: 3, Passed: 2, Failed: 1}, tests)
	storeRun(t, root, "bbb", now.Add(time.Hour), "unit", testresults.Summary{Tests: 99}, nil) // after now: ignored

	run, ok := healthTestRun(root, "unit", now)
	if !ok {
		t.Fatal("no run found")
	}
	if run.Summary.Tests != 3 || run.Finished.Unix() != now.Add(-48*time.Hour).Unix() {
		t.Errorf("run = %+v, want aaa's 3 tests finished 48 h ago", run)
	}
	if len(run.Tests) != 1 || run.Tests[0].Name != "TestA" {
		t.Errorf("tests = %+v, want the kept list", run.Tests)
	}
	if _, ok := healthTestRun(root, "e2e", now); ok {
		t.Error("no e2e results were recorded")
	}
}

// The flaky reader reads the main checkout's runs and local CI's worktree
// runs: a step red in one and green in the other on one build is flaky.
func TestHealthE2ERunsReadsCheckoutAndCIWorktree(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	started := now.Add(-time.Hour).Format(time.RFC3339)
	for dir, status := range map[string]string{
		filepath.Join(root, ".playtest", "runs"):               "fail",
		filepath.Join(WorktreePath(root), ".playtest", "runs"): "pass",
	} {
		p := filepath.Join(dir, "board", "20261115-100000.000-auto")
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "session: board\nmode: auto\nbuild: abc1234\nstarted: \"" + started + "\"\nsteps:\n  - {id: TP1, status: " + status + "}\n"
		if err := os.WriteFile(filepath.Join(p, "results.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runs, steps, flaky, ok := healthE2ERuns(root, now.AddDate(0, 0, -14), now)
	if !ok || runs != 2 || steps != 1 || len(flaky) != 1 || flaky[0] != "board/TP1" {
		t.Errorf("healthE2ERuns = %d runs, %d steps, %v, %v; want 2, 1, [board/TP1], true", runs, steps, flaky, ok)
	}
	if _, _, _, ok := healthE2ERuns(t.TempDir(), now.AddDate(0, 0, -14), now); ok {
		t.Error("no runs on disk must be no data")
	}
}

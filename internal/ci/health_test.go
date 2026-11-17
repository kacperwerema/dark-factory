package ci

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/testresults"
	"github.com/kacperwerema/dark-factory/internal/tracker"
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

	runs := healthTestRuns(root, "unit", now, 5)
	if len(runs) != 1 {
		t.Fatalf("runs = %+v, want aaa only", runs)
	}
	run := runs[0]
	if run.Summary.Tests != 3 || run.Finished.Unix() != now.Add(-48*time.Hour).Unix() {
		t.Errorf("run = %+v, want aaa's 3 tests finished 48 h ago", run)
	}
	if len(run.Tests) != 1 || run.Tests[0].Name != "TestA" {
		t.Errorf("tests = %+v, want the kept list", run.Tests)
	}
	if got := healthTestRuns(root, "e2e", now, 5); len(got) != 0 {
		t.Error("no e2e results were recorded")
	}
}

// The best wall reads up to n runs, newest first, and only the newest
// carries its per-test list.
func TestHealthTestRunsAreTheNewestN(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	tests := []testresults.Test{{Name: "TestA", Seconds: 1, Outcome: "pass"}}
	for i, wall := range []float64{200, 150, 300} { // i=0 newest
		storeRun(t, root, string(rune('a'+i))+"00", now.Add(-time.Duration(i+1)*time.Hour), "unit", testresults.Summary{Tests: 1, WallS: wall}, tests)
	}
	runs := healthTestRuns(root, "unit", now, 2)
	if len(runs) != 2 || runs[0].Summary.WallS != 200 || runs[1].Summary.WallS != 150 {
		t.Fatalf("runs = %+v, want walls 200, 150", runs)
	}
	if len(runs[0].Tests) != 1 || runs[1].Tests != nil {
		t.Errorf("only the newest run reads its test list: %d, %d", len(runs[0].Tests), len(runs[1].Tests))
	}
}

// AC1: a CI run reads the load before and after the command that writes
// results, keeps the higher with the cores on the summary, says it in the
// results: line, and the snapshot taken from that run has tests.load and
// tests.cores beside tests.wall_seconds.
func TestSnapshotRecordsLoadWithUnitWall(t *testing.T) {
	readings := []Load{{Avg: 9.2, Cores: 14}, {Avg: 16.4, Cores: 14}}
	old := readLoad
	readLoad = func() Load { r := readings[0]; readings = readings[1:]; return r }
	t.Cleanup(func() { readLoad = old })

	root := newRepo(t)
	write := `printf '%s\n' '{"Time":"2026-10-23T10:00:00Z","Action":"pass","Package":"m","Test":"TestA","Elapsed":0.5}' '{"Time":"2026-10-23T10:00:02Z","Action":"pass","Package":"m","Elapsed":2}' > "$CI_RESULTS_DIR/unit.json"`
	unit := tracker.CICommand{Cmd: write, Results: []tracker.CIResult{{Kind: "unit", Format: "go-test-json", File: "unit.json"}}}
	var out strings.Builder
	runs, _, err := Execute(Options{Root: root, Config: cfg(cmd("true"), unit), Out: &out})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%d err=%v", len(runs), err)
	}
	if len(readings) != 0 {
		t.Errorf("want exactly two readings (before and after the results command, none for a command without results), %d left", len(readings))
	}
	u := runs[0].Results["unit"]
	if u.Load != 16.4 || u.Cores != 14 || !u.Loaded() {
		t.Errorf("unit summary load = %v on %d, want the higher reading 16.4 on 14", u.Load, u.Cores)
	}
	if !strings.Contains(out.String(), "2.0 s wall, load 16.4 on 14 cores (unit.json)") {
		t.Errorf("results line lacks the load: %q", out.String())
	}

	proj := &tracker.Project{Slug: "ci", WorkDir: root}
	env, err := health.NewEnv(proj, root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	snap, _, err := health.Take(context.Background(), env, health.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	k := map[string]string{"kind": "unit"}
	for name, want := range map[string]float64{"tests.wall_seconds": 2, "tests.load": 16.4, "tests.cores": 14} {
		if got, ok := snap.Value(name, k); !ok || got != want {
			t.Errorf("%s{kind=unit} = %v (present %v), want %v", name, got, ok, want)
		}
	}
}

// Without a load reading (an OS with no reader) the summary has none and
// the line says nothing about load.
func TestNoLoadReadingRecordsNoLoad(t *testing.T) {
	old := readLoad
	readLoad = func() Load { return Load{} }
	t.Cleanup(func() { readLoad = old })
	root := newRepo(t)
	write := `printf '%s\n' '{"Time":"2026-10-23T10:00:00Z","Action":"pass","Package":"m","Test":"TestA","Elapsed":0.5}' > "$CI_RESULTS_DIR/unit.json"`
	unit := tracker.CICommand{Cmd: write, Results: []tracker.CIResult{{Kind: "unit", Format: "go-test-json", File: "unit.json"}}}
	var out strings.Builder
	runs, _, err := Execute(Options{Root: root, Config: cfg(unit), Out: &out})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%d err=%v", len(runs), err)
	}
	if u := runs[0].Results["unit"]; u.Load != 0 || u.Cores != 0 || u.Loaded() {
		t.Errorf("summary = %+v, want no load", u)
	}
	if strings.Contains(out.String(), "load") {
		t.Errorf("results line names a load it never read: %q", out.String())
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

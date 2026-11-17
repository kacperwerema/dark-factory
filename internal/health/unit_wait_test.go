package health

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/testresults"
)

// fakeRuns stands in for local CI's recent runs of one kind, newest first,
// honouring n as internal/ci does.
func fakeRuns(t *testing.T, kind string, runs []TestRun) {
	t.Helper()
	SetTestResults(func(root, k string, now time.Time, n int) []TestRun {
		if k != kind {
			return nil
		}
		if len(runs) > n {
			return runs[:n]
		}
		return runs
	})
	t.Cleanup(func() { SetTestResults(nil) })
}

func runTestsFamily(t *testing.T, now time.Time) *Snapshot {
	t.Helper()
	root, proj := healthRepo(t, now)
	env, err := NewEnv(proj, root, now)
	if err != nil {
		t.Fatal(err)
	}
	snap, _, err := take(context.Background(), env, TriggerManual, []registered{{"tests", testsFamily}})
	if err != nil {
		t.Fatal(err)
	}
	if e := snap.Families["tests"].Error; e != "" {
		t.Fatal(e)
	}
	return snap
}

// AC2: the newest run was taken under load (294 s) but one of the last 5
// ran in 198 s: unit_wait scores 198 s. A sixth, older and faster run is
// outside the window. A snapshot from before the best row existed still
// scores its one wall.
func TestUnitWaitScoresTheBestRecentRun(t *testing.T) {
	now := time.Now()
	var runs []TestRun
	for i, wall := range []float64{294, 250, 198, 0, 240, 50} { // newest first; 0: a run with no wall
		runs = append(runs, TestRun{Finished: now.Add(-time.Duration(i+1) * time.Hour), Summary: testresults.Summary{Tests: 10, WallS: wall}})
	}
	fakeRuns(t, "unit", runs)
	snap := runTestsFamily(t, now)
	for id, want := range map[string]float64{
		"tests.wall_seconds{kind=unit}":                   294,
		"tests.wall_seconds_best{kind=unit,window=5runs}": 198,
		"tests.wall_runs{kind=unit}":                      5,
	} {
		if got, ok := value(snap, id); !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", id, got, ok, want)
		}
	}
	unit := DefaultComponents()[0]
	if unit.Name != "unit_wait" {
		t.Fatalf("first component = %s", unit.Name)
	}
	rows := scoreRows(Derive(snap, nil, []Component{unit}))
	if got := rows["score.component.value{component=unit_wait}"]; got != 198 {
		t.Errorf("unit_wait value = %v, want the best wall 198", got)
	}
	if got := rows["score.component{component=unit_wait}"]; got != unit.Sub(198) {
		t.Errorf("unit_wait sub = %v, want %v", got, unit.Sub(198))
	}

	old := snapOf(map[string]float64{"tests.wall_seconds{kind=unit}": 150})
	if got := scoreRows(Derive(old, nil, []Component{unit}))["score.component.value{component=unit_wait}"]; got != 150 {
		t.Errorf("an old snapshot without the best row scores %v, want its wall 150", got)
	}
}

// AC3: a run whose load was above the core count marks each slowest-test
// row; the row id stays the same, so recorded reviews' evidence still
// resolves. A run at or under its cores carries no mark.
func TestSlowestTestsRowsCarryTheLoad(t *testing.T) {
	now := time.Now()
	tests := []testresults.Test{{Name: "TestA", Part: "cmd/playtest", Area: "cmd", Seconds: 38.67, Outcome: "pass"}}
	for _, c := range []struct {
		load   float64
		cores  int
		marked bool
	}{{16.4, 14, true}, {14, 14, false}, {3.1, 14, false}, {0, 0, false}} {
		fakeRuns(t, "unit", []TestRun{{Finished: now.Add(-time.Hour), Summary: testresults.Summary{Tests: 1, WallS: 40, Load: c.load, Cores: c.cores}, Tests: tests}})
		snap := runTestsFamily(t, now)
		rows := snap.Rows("tests.slowest.seconds")
		if len(rows) != 1 || rows[0].ID != "tests.slowest.seconds{kind=unit,test=cmd/playtest.TestA}" {
			t.Fatalf("load %v: slowest rows = %+v", c.load, rows)
		}
		mark := "run under load 16.4 on 14 cores: times inflated"
		if got := strings.Contains(rows[0].Name, "under load"); got != c.marked {
			t.Errorf("load %v on %d: row help %q, marked %v, want %v", c.load, c.cores, rows[0].Name, got, c.marked)
		}
		if c.marked && !strings.Contains(rows[0].Name, mark) {
			t.Errorf("row help %q lacks %q", rows[0].Name, mark)
		}
		// The Health view's Slowest tests subtitle and the review pack read it from the rows.
		if got := snap.RunLoad("unit"); (got != "") != c.marked || (c.marked && got != mark) {
			t.Errorf("load %v on %d: RunLoad = %q, marked %v", c.load, c.cores, got, c.marked)
		}
		if _, ok := value(snap, "tests.load{kind=unit}"); ok != (c.cores > 0) {
			t.Errorf("load %v on %d: tests.load present %v", c.load, c.cores, ok)
		}
	}
}

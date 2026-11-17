package bench

import (
	"slices"
	"testing"
	"time"
)

func TestForecastPricesUnpricedTasksWithTodaysPrices(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	b := New("fc")
	b.Tasks = []Task{{Project: "p", Slug: "a"}, {Project: "p", Slug: "b"}}
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	r := &Run{ID: "r1", Benchmark: "fc", Started: at, Ended: at.Add(time.Hour), Stop: StopDone}
	recs := []TaskRecord{
		{Task: "p/a", Outcome: OutcomePass, WallSec: 600, Usage: Usage{Known: true, Model: "m-1", Input: 2_000_000}},
		{Task: "p/b", Outcome: OutcomePass, WallSec: 600, Usage: Usage{Known: true, Model: "other", Input: 2_000_000}},
	}
	if err := Save(r, recs); err != nil {
		t.Fatal(err)
	}
	f, ok := ForecastRun(b, 1, map[string]Price{"m": {In: 3}})
	if !ok || f.Run != "r1" || f.Tasks != 2 || f.Tokens != 4_000_000 || f.Wall != 20*time.Minute {
		t.Fatalf("forecast %+v", f)
	}
	// m-1 is priced by its prefix ($6); other has no price, so USD is a floor.
	if f.Priced != 1 || f.USD != 6 || f.CostKnown() || !slices.Equal(f.Unpriced, []string{"other"}) {
		t.Fatalf("pricing %+v", f)
	}
	two := f.Times(2)
	if two.Tasks != 4 || two.Tokens != 8_000_000 || two.Wall != 40*time.Minute || two.USD != 12 {
		t.Fatalf("times 2 %+v", two)
	}
}

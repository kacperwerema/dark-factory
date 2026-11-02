package viewer

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// nightAt is a stand-in night: passed of 4, median wall in minutes and
// median tokens in millions; no bench run behind it.
func nightAt(at time.Time, passed int, wallMin float64, tokensM float64) bench.Night {
	return bench.Night{
		Date: at.Format("2006-01-02"), At: at, Benchmark: "factory-v1", Project: "orchard",
		JobRun: "product/nightly-improve-" + at.Format("2006-01-02"), MainSHA: "abcdef1234567",
		Run: "run-" + at.Format("0102"), Stop: bench.StopDone,
		PassRate: float64(passed) / 4, Passed: passed, Scored: 4,
		MedianWallSec: wallMin * 60, MedianTokens: int64(tokensM * 1e6), TokensKnown: 4,
	}
}

func nightlyBench(t *testing.T) *tracker.Project {
	t.Helper()
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	b := bench.New("factory-v1")
	b.Tasks = []bench.Task{{Project: "orchard", Slug: "a"}}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{Slug: "orchard", Name: "Orchard"}
}

// AC2: two nights of stand-in runs: the card shows better / same / worse
// per number and the trend; status.md has the one line.
func TestNightlyCardTwoNights(t *testing.T) {
	proj := nightlyBench(t)
	card := reportNightly(proj, "/p/orchard")
	if card == nil || len(card.Rows) != 1 || !card.Rows[0].NoNight || !strings.Contains(card.Cmd, "job auto on product/nightly-improve") {
		t.Fatalf("no night yet: %+v", card)
	}
	if nightlyStatusLine(proj) != "" {
		t.Fatal("status line without nights")
	}
	d1 := time.Date(2026, 11, 1, 2, 0, 0, 0, time.Local)
	for _, n := range []bench.Night{
		nightAt(d1, 3, 10, 12),
		nightAt(d1.AddDate(0, 0, 1), 2, 7, 12.5), // one task fewer: worse; -30% time: better; +4% tokens: same
	} {
		if err := bench.AppendNight(n); err != nil {
			t.Fatal(err)
		}
	}
	row := reportNightly(proj, "/p/orchard").Rows[0]
	if row.NoNight || row.Date != "2026-11-02" || row.RunURL != "/p/orchard/issue/product/nightly-improve-2026-11-02" || row.RunCmd != "issue-cli bench report factory-v1/run-1102" {
		t.Fatalf("row %+v", row)
	}
	want := map[string][3]string{
		"pass":   {"2/4", "3/4", bench.Worse},
		"time":   {"7m", "10m", bench.Better},
		"tokens": {"12.5M", "12.0M", bench.Same},
	}
	for _, n := range row.Numbers {
		w := want[n.Key]
		if n.Now != w[0] || n.Before != w[1] || n.Mark != w[2] || n.Spark == "" || n.Range == "" {
			t.Errorf("%s: %+v, want %v with a trend", n.Key, n, w)
		}
	}
	if len(row.Numbers) != 3 {
		t.Fatalf("numbers %+v", row.Numbers)
	}
	line := nightlyStatusLine(proj)
	if line != "Last night: factory-v1 2026-11-02: pass 2/4 (worse) · median 7m (better) · 12.5M tokens (same)" {
		t.Fatalf("status line %q", line)
	}

	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&out, "wb/nightly", reportNightly(proj, "/p/orchard")); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{`data-testid="report.nightly"`, `data-testid="report.nightly.pass.mark">worse`, `data-testid="report.nightly.time.mark">better`,
		`data-testid="report.nightly.tokens.mark">same`, `data-testid="report.nightly.pass.trend"`, "no experiment queued", `href="/p/orchard/issue/product/nightly-improve-2026-11-02"`} {
		if !strings.Contains(html, want) {
			t.Errorf("card lacks %q:\n%s", want, html)
		}
	}
}

// A held night shows its reason above the last measured numbers.
func TestNightlyCardHeldNight(t *testing.T) {
	proj := nightlyBench(t)
	d1 := time.Date(2026, 11, 1, 2, 0, 0, 0, time.Local)
	bench.AppendNight(nightAt(d1, 3, 10, 12))
	bench.AppendNight(bench.Night{Date: "2026-11-02", At: d1.AddDate(0, 0, 1), Benchmark: "factory-v1", Held: "memory 62% ≥ 50%"})
	row := reportNightly(proj, "/p/orchard").Rows[0]
	if row.Held != "memory 62% ≥ 50%" || len(row.Numbers) != 3 || row.Numbers[0].Mark != "" {
		t.Fatalf("held row %+v", row)
	}
	if line := nightlyStatusLine(proj); !strings.Contains(line, "2026-11-02 held: memory 62% ≥ 50% · last measured 2026-11-01:") {
		t.Fatalf("line %q", line)
	}
}

// AC4: a pass rate under last week's median puts a Needs you row up; with
// fewer than 3 nights before it, none.
func TestNightlyRegressionNeedsYou(t *testing.T) {
	proj := nightlyBench(t)
	d1 := time.Date(2026, 11, 1, 2, 0, 0, 0, time.Local)
	now := d1.AddDate(0, 0, 5)
	bench.AppendNight(nightAt(d1, 3, 10, 12))
	bench.AppendNight(nightAt(d1.AddDate(0, 0, 1), 3, 10, 12))
	bench.AppendNight(nightAt(d1.AddDate(0, 0, 2), 2, 10, 12))
	if rows := nightlyNeedsYou(proj, "", "/p/orchard", now); len(rows) != 0 {
		t.Fatalf("2 nights before the drop: %+v", rows)
	}
	bench.AppendNight(nightAt(d1.AddDate(0, 0, 3), 4, 10, 12))
	bench.AppendNight(nightAt(d1.AddDate(0, 0, 4), 2, 10, 12)) // under the median 75%
	rows := nightlyNeedsYou(proj, "", "/p/orchard", now)
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
	r := rows[0]
	if r.Kind != "nightly" || r.URL != "/p/orchard/issue/product/nightly-improve-2026-11-05" ||
		!strings.Contains(r.Note, "passed 50%, under last week's median 75% (4 nights)") {
		t.Fatalf("row %+v", r)
	}
	// It is in the one list every waiting count is the length of.
	found := false
	for _, x := range needsYouRowsWith(proj, "", "/p/orchard", nil, now) {
		found = found || x.Kind == "nightly"
	}
	if !found {
		t.Fatal("needsYouRows lacks the nightly row")
	}
	// Recovering clears it.
	bench.AppendNight(nightAt(d1.AddDate(0, 0, 5), 3, 10, 12))
	if rows := nightlyNeedsYou(proj, "", "/p/orchard", now); len(rows) != 0 {
		t.Fatalf("recovered: %+v", rows)
	}
}

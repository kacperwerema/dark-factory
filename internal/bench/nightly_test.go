package bench

import (
	"testing"
	"time"
)

// The night's numbers are over the non-holdout scored tasks; tokens over
// the known ones only.
func TestFillNightMedians(t *testing.T) {
	r := &Run{ID: "r1", Stop: StopDone}
	recs := []TaskRecord{
		{Task: "p/a", Outcome: OutcomePass, WallSec: 600, Usage: Usage{Known: true, CacheRead: 10_000_000}},
		{Task: "p/b", Outcome: OutcomeFail, WallSec: 900, Usage: Usage{Known: true, CacheRead: 20_000_000}},
		{Task: "p/c", Outcome: OutcomePass, WallSec: 1200},
		{Task: "p/d", Outcome: OutcomeBroken, WallSec: 99999},
		{Task: "p/h", Outcome: OutcomePass, WallSec: 99999, Holdout: true},
	}
	Summarize(r, recs)
	r.Tasks = recs[:4]
	var n Night
	FillNight(&n, r)
	if n.Passed != 2 || n.Scored != 3 || n.MedianWallSec != 900 || n.MedianTokens != 15_000_000 || n.TokensKnown != 2 || n.Holdout.Total != 1 {
		t.Fatalf("night %+v", n)
	}
}

// Same is under one task's worth for the pass rate, 10% for tokens, 25% for
// time; a held night is skipped when comparing.
func TestMorningMarks(t *testing.T) {
	at := time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)
	night := func(d int, passed, scored int, wall float64, tok int64) Night {
		return Night{Date: at.AddDate(0, 0, d).Format("2006-01-02"), At: at.AddDate(0, 0, d), Run: "r", Passed: passed, Scored: scored,
			PassRate: float64(passed) / float64(scored), MedianWallSec: wall, MedianTokens: tok, TokensKnown: 1}
	}
	m := MorningOf("b", []Night{
		night(0, 6, 8, 1000, 10_000_000),
		{Date: "2026-11-02", At: at.AddDate(0, 0, 1), Held: "memory 60% ≥ 50%"},
		night(2, 6, 9, 1240, 11_100_000), // -0.08 < 1/9: same; +24%: same; +11%: worse
	})
	if m.Pass.Mark != Same || m.Wall.Mark != Same || m.Tokens.Mark != Worse || len(m.Pass.Trend) != 2 {
		t.Fatalf("marks %q %q %q trend %v", m.Pass.Mark, m.Wall.Mark, m.Tokens.Mark, m.Pass.Trend)
	}
	if m.Last.Held != "" || m.Before.Date != "2026-11-01" {
		t.Fatalf("last %+v before %+v", m.Last, m.Before)
	}
	if m := MorningOf("b", []Night{night(0, 6, 8, 1000, 1), night(1, 8, 8, 600, 1)}); m.Pass.Mark != Better || m.Wall.Mark != Better {
		t.Fatalf("better: %q %q", m.Pass.Mark, m.Wall.Mark)
	}
}

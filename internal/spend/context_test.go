package spend

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// compact is a compaction record at at.
func compact(at time.Time) string {
	return fmt.Sprintf(`{"type":"system","subtype":"compact_boundary","content":"Conversation compacted","timestamp":%q,"compactMetadata":{"preTokens":300}}`, at.Format(time.RFC3339Nano))
}

func TestLoadContextSums(t *testing.T) {
	f := newFixture(t)
	at := func(h int) time.Time { return time.Date(2026, 10, 29, h, 0, 0, 0, time.UTC) }
	wt := f.worktree("seo/page-refresh")
	// A Marketing worker: floor 100 (in 60 + read 30 + write 10; output is
	// not context), then 300 and 200; m2 written twice (one line per
	// content block) counts once; a compaction; a subagent that is no turn.
	f.write(wt, "a.jsonl", "",
		msg("m1", at(9), "claude-test", 60, 999, 30, 10),
		msg("m2", at(10), "claude-test", 0, 5, 300, 0),
		msg("m2", at(10), "claude-test", 0, 5, 300, 0),
		compact(at(10).Add(time.Minute)),
		msg("m3", at(11), "claude-test", 0, 5, 200, 0))
	f.write(wt, "a/subagents/x.jsonl", "", msg("s1", at(10), "claude-test", 7777, 0, 0, 0))
	// The same worker resumed: m3 carried over, one new turn of 400. No
	// floor of its own.
	f.write(wt, "b.jsonl", "",
		msg("m3", at(11), "claude-test", 0, 5, 200, 0),
		msg("m4", at(12), "claude-test", 0, 5, 400, 0))
	// Product's worker: floor 50, one more turn of 150.
	f.write(f.worktree("ui/button"), "c.jsonl", "",
		msg("p1", at(9), "claude-test", 50, 0, 0, 0),
		msg("p2", at(9).Add(time.Minute), "claude-test", 0, 0, 150, 0))
	root := f.p.Root()
	// The owner's own, a turn too old for the 7 days, and another
	// project's lead (skipped).
	f.write(root, "owner.jsonl", "", msg("o1", at(8), "claude-test", 20, 0, 0, 0))
	f.write(root, "old.jsonl", "lead-acme", msg("c0", longAgo, "claude-test", 5000, 0, 0, 0), msg("c1", at(8), "claude-test", 0, 0, 900, 0))
	f.write(root, "other.jsonl", "lead-other", msg("x1", at(8), "claude-test", 77777, 0, 0, 0))

	rep := LoadContext(f.p, f.issues, now, 7, Options{ProjectsDir: f.projects})
	if want := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC); !rep.Since.Equal(want) {
		t.Errorf("since = %v, want %v", rep.Since, want)
	}
	mkt := rep.Find("marketing")
	want := ContextStats{Sessions: 2, Floored: 1, Resumed: 1, Turns: 4, Floor: 100, Median: 200, Max: 400,
		Compactions: 1, Context: 1000, FloorTokens: 300, FloorShare: 0.5}
	if mkt.ContextStats != want {
		t.Errorf("marketing = %+v\nwant        %+v", mkt.ContextStats, want)
	}
	if len(mkt.Top) != 2 || mkt.Top[0].Context != 600 || mkt.Top[0].Floor != 100 || mkt.Top[1].Floor != 0 || !mkt.Top[1].Resumed || mkt.Top[0].Issue != "seo/page-refresh" {
		t.Errorf("marketing top = %+v", mkt.Top)
	}
	// GTM rolls Marketing up; HQ every box: + Product (50, 150) + the lead
	// (its first turn before the window: no floor).
	if g := rep.Find("gtm"); g.Context != 1000 || g.Sessions != 2 {
		t.Errorf("gtm = %+v", g.ContextStats)
	}
	hq := rep.Find("hq")
	if hq.Sessions != 4 || hq.Turns != 7 || hq.Floored != 2 || hq.Floor != 50 || hq.Context != 2100 || hq.Max != 900 || hq.FloorTokens != 300+100 {
		t.Errorf("hq = %+v", hq.ContextStats)
	}
	if u := rep.Unattributed; u.Sessions != 1 || u.Floor != 20 || u.FloorShare != 1 {
		t.Errorf("unattributed = %+v", u)
	}
	if rep.Workers.Sessions != 3 || rep.All.Sessions != 5 || rep.All.Context != 2120 {
		t.Errorf("workers = %+v, all = %+v", rep.Workers, rep.All)
	}
	if rep.Top[0].Agent != "lead-acme" || rep.Top[0].Box != HQ {
		t.Errorf("top = %+v", rep.Top[0])
	}
	// Numbers only: no message text reaches the report.
	b, _ := json.Marshal(rep)
	if strings.Contains(string(b), "secret") {
		t.Error("message text in the report")
	}
}

func TestContextMetricsCountOnlyTheDaysWorkers(t *testing.T) {
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	s := func(start time.Time, worker bool, floor int64, turns ...int64) ContextSession {
		cs := ContextSession{Start: start, Worker: worker, Floor: floor, Turns: len(turns), turns: turns}
		for _, c := range turns {
			cs.Context += c
		}
		return cs
	}
	rows := ContextMetrics([]ContextSession{
		s(day.Add(time.Hour), true, 80, 80, 120),
		s(day.Add(2*time.Hour), true, 90, 90, 200, 300),
		s(day.Add(-time.Hour), true, 10, 10),  // the day before
		s(day.Add(time.Hour), false, 5, 5),    // a lead
		s(day.Add(3*time.Hour), true, 0, 400), // resumed: no floor
	}, day)
	got := map[string]float64{}
	for _, r := range rows {
		got[r.Name] = r.Value
		if r.Labels["role"] != "worker" {
			t.Errorf("%s labels = %v", r.Name, r.Labels)
		}
	}
	if got["context.floor.sessions"] != 2 || got["context.floor.p50"] != 80 || got["context.turn.p50"] != 120 {
		t.Errorf("rows = %v", got)
	}
	if rows := ContextMetrics(nil, day); len(rows) != 1 || rows[0].Value != 0 {
		t.Errorf("no sessions: %+v", rows)
	}
}

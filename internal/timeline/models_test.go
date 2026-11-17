package timeline

import (
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestModelRowsCompareTypesByModel(t *testing.T) {
	done := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	rep := Report{Issues: []IssueStat{
		{Slug: "a", Type: "docs", Model: "claude-sonnet-5-5", DoneAt: done, Cycle: time.Hour, RedRounds: 2, Backward: 1},
		{Slug: "b", Type: "docs", Model: "claude-sonnet-5-5", DoneAt: done, Cycle: time.Hour},
		{Slug: "c", Type: "docs", Model: tracker.ModelUnnamed, DoneAt: done, Cycle: time.Hour, RedRounds: 1},
		{Slug: "d", Type: "feature", Model: tracker.ModelUnnamed, DoneAt: done, Cycle: 30 * 24 * time.Hour},
		{Slug: "e", Type: "feature", DoneAt: done}, // never dispatched: no row
	}}
	rep.summarize(nil)
	rep = rep.WithTokens(map[string]int64{"a": 100, "b": 300, "d": 5000}, done.AddDate(0, 0, -7))
	row := func(typ, model string) ModelRow {
		for _, m := range rep.Models {
			if m.Type == typ && m.Model == model {
				return m
			}
		}
		t.Fatalf("no row %s/%s in %+v", typ, model, rep.Models)
		return ModelRow{}
	}
	s := row("docs", "claude-sonnet-5-5")
	if s.N != 2 || s.Reopened != 1 || s.ReopenRate() != 0.5 || s.RedRounds != 2 || s.TokensKnown != 2 || s.Tokens != 100 { // nearest-rank p50
		t.Errorf("docs on sonnet = %+v", s)
	}
	if d := row("docs", tracker.ModelUnnamed); d.N != 1 || d.TokensKnown != 0 {
		t.Errorf("docs on default = %+v (no transcript: unknown, never 0)", d)
	}
	// d started 30 days before it was done, before the transcripts' window.
	if f := row("feature", tracker.ModelUnnamed); f.N != 1 || f.TokensKnown != 0 {
		t.Errorf("feature = %+v: work older than the window must stay unknown", f)
	}
	if a := row(All, "claude-sonnet-5-5"); a.N != 2 {
		t.Errorf("all on sonnet = %+v", a)
	}
	if !rep.HasModels() {
		t.Error("HasModels is false with a sonnet row")
	}
	if rep.Models[0].Type != All {
		t.Errorf("all rows come first: %+v", rep.Models)
	}
}

func TestWorkerModelAndRedRounds(t *testing.T) {
	w := tracker.AgentSessionName("x")
	cases := []struct {
		ds   []tracker.Dispatch
		want string
	}{
		{nil, ""},
		{[]tracker.Dispatch{{Session: w}}, tracker.ModelUnnamed},
		{[]tracker.Dispatch{{Session: w, Model: "m1"}, {Session: "postmortem-x", Model: "m2"}, {Session: w, Model: "m1"}}, "m1"},
		{[]tracker.Dispatch{{Session: w, Model: "m1"}, {Session: w}}, tracker.ModelMixed},
	}
	for i, c := range cases {
		if got := tracker.WorkerModel(tracker.StatsStore{Dispatches: c.ds}, "x"); got != c.want {
			t.Errorf("case %d: %q, want %q", i, got, c.want)
		}
	}
	rounds := []tracker.RoundResult{
		{Check: "gate:a", Red: true, Head: "h1"},
		{Check: "gate:a", Red: true, Head: "h1"}, // a retry: same round
		{Check: "gate:a", Red: true, Head: "h2"},
		{Check: "gate:b", Red: true},
		{Check: "gate:a", Red: false, Head: "h3"},
	}
	if got := tracker.RedRoundCount(rounds); got != 3 {
		t.Errorf("RedRoundCount = %d, want 3", got)
	}
}

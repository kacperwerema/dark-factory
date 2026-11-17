package autostart

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// machine runs leads over one shared budget: each tick, the projects ask in
// the given order (the first one ticks first), the way WithShare would.
type machine struct {
	share *Share
	last  *Start
	now   time.Time
	order []string // who started, in order
}

func newMachine() *machine { return &machine{share: &Share{}, now: t0} }

type lead struct {
	name   string
	weight int
	ready  bool
	state  *State
}

func (m *machine) ask(l lead) Verdict {
	st := l.state
	if st == nil {
		st = onState()
	}
	in := Input{Project: l.name, State: st, Memory: okMemory(20), Last: m.last, Now: m.now, Share: m.share, Weight: l.weight}
	if l.ready {
		in.Candidates = []Candidate{{Slug: l.name + "/next"}}
	}
	v := Decide(in)
	m.share.Note(l.name, l.weight, v, m.now)
	if v.Start {
		m.share.Started(l.name, v.Slug, v.Contenders, m.now)
		m.last = &Start{Project: l.name, Slug: v.Slug, At: m.now}
		m.order = append(m.order, l.name)
	}
	return v
}

// gaps runs n gaps: at each, every lead asks in order, then the clock
// moves one gap on.
func (m *machine) gaps(n int, leads ...lead) {
	for i := 0; i < n; i++ {
		for _, l := range leads {
			m.ask(l)
		}
		m.now = m.now.Add(5 * time.Minute)
	}
}

func count(order []string) map[string]int {
	c := map[string]int{}
	for _, p := range order {
		c[p]++
	}
	return c
}

// Weights 3 and 1 give 3 of every 4 starts and 1, though dark-factory
// always ticks first (first come, first served gave it every start).
func TestShareThreeToOne(t *testing.T) {
	m := newMachine()
	df, atlas := lead{"dark-factory", 1, true, nil}, lead{"atlas", 3, true, nil}
	m.gaps(1, df, atlas) // the first gap: atlas hasn't asked yet
	m.order = nil
	m.gaps(16, df, atlas)
	for i := 0; i+4 <= len(m.order); i += 4 {
		c := count(m.order[i : i+4])
		if c["atlas"] != 3 || c["dark-factory"] != 1 {
			t.Fatalf("starts %d..%d = %v, want atlas 3 and dark-factory 1 (all: %v)", i, i+4, c, m.order)
		}
	}
	if len(m.order) != 16 {
		t.Fatalf("one start per gap: got %d in 16 gaps (%v)", len(m.order), m.order)
	}
}

// No waiting project waits more than (sum of the weights) starts.
func TestShareNoStarvation(t *testing.T) {
	for _, weights := range [][]int{{3, 1}, {5, 2, 1}, {1, 1}, {9, 1}} {
		var leads []lead
		sum := 0
		for i, w := range weights {
			leads = append(leads, lead{fmt.Sprintf("p%d", i), w, true, nil})
			sum += w
		}
		// The lightest ticks first: the worst case for the others.
		for i, j := 0, len(leads)-1; i < j; i, j = i+1, j-1 {
			leads[i], leads[j] = leads[j], leads[i]
		}
		m := newMachine()
		m.gaps(1, leads...)
		m.order = nil
		m.gaps(sum*6, leads...)
		for i := 0; i+sum <= len(m.order); i++ {
			c := count(m.order[i : i+sum])
			for _, l := range leads {
				if c[l.name] == 0 {
					t.Fatalf("weights %v: %s didn't start in starts %d..%d: %v", weights, l.name, i, i+sum, m.order)
				}
			}
		}
		total := count(m.order)
		for _, l := range leads {
			if want := l.weight * 6; total[l.name] != want {
				t.Errorf("weights %v: %s started %d, want %d", weights, l.name, total[l.name], want)
			}
		}
	}
}

// A project whose ask is stale, that its own limits hold, or whose own
// memory line today's reading is over, holds no one up.
func TestShareIdleProjectSkipped(t *testing.T) {
	ready := []Candidate{{Slug: "df/one"}}
	in := func(s *Share) Input {
		return Input{Project: "dark-factory", State: onState(), Memory: okMemory(40), Now: t0, Share: s, Weight: 1, Candidates: ready}
	}
	waiting := Verdict{Waiting: true, Below: 50, gap: 5}
	cases := map[string]Ask{
		"stale (11 min, gap 5)": func() Ask { a := waiting.Ask(t0.Add(-11 * time.Minute)); a.Weight = 3; return a }(),
		"held by its own cap":   {At: t0.Add(-time.Minute), Weight: 3, Waiting: false, Below: 50, GapMinutes: 5},
		"memory over its line":  {At: t0.Add(-time.Minute), Weight: 3, Waiting: true, Below: 30, GapMinutes: 5},
		"load over its line":    {At: t0.Add(-time.Minute), Weight: 3, Waiting: true, Below: 50, MaxLoad: 2, GapMinutes: 5},
	}
	for name, a := range cases {
		s := &Share{Asks: map[string]Ask{"atlas": a}}
		x := in(s)
		x.CPU = CPU{Found: true, Avg1: 3, Avg5: 3, Cores: 14}
		if v := Decide(x); !v.Start {
			t.Errorf("%s: dark-factory held: %s", name, v.Reason)
		}
	}
	// A fresh waiting ask under the reading holds it, for atlas' turn.
	s := &Share{Asks: map[string]Ask{"atlas": {At: t0.Add(-9 * time.Minute), Weight: 3, Waiting: true, Below: 50, GapMinutes: 5}}}
	if v := Decide(in(s)); v.Start || !strings.Contains(v.Reason, "atlas' turn") {
		t.Errorf("fresh waiting atlas: %+v", v)
	}
}

// A project back after a long idle stretch gets its share from then on,
// not the turns it didn't use.
func TestShareNothingSavedUp(t *testing.T) {
	m := newMachine()
	df, atlas := lead{"dark-factory", 1, true, nil}, lead{"atlas", 3, true, nil}
	m.gaps(2, df, atlas)
	idleAtlas := lead{"atlas", 3, false, nil} // nothing ready: not waiting
	m.gaps(12, df, idleAtlas)
	m.gaps(4, df)        // and not asking at all
	m.gaps(1, df, atlas) // atlas asks again; dark-factory, first, had the gap
	m.order = nil
	m.gaps(8, df, atlas)
	if c := count(m.order); c["dark-factory"] != 2 || c["atlas"] != 6 {
		t.Fatalf("after atlas came back: %v, want atlas 6 and dark-factory 2: %v", c, m.order)
	}
	if c := count(m.order[:4]); c["atlas"] > 3 {
		t.Fatalf("atlas took %d of the first 4 starts back: saved-up turns (%v)", c["atlas"], m.order)
	}
}

// The refusal names the holder, its weight, and when this project goes.
func TestTurnRefusalText(t *testing.T) {
	cs := []Contender{{Project: "dark-factory", Weight: 1}, {Project: "atlas", Weight: 3}}
	if got, want := TurnHold(cs, "dark-factory"), "atlas' turn (weight 3; dark-factory next in 2 starts)"; got != want {
		t.Errorf("TurnHold = %q, want %q", got, want)
	}
	if got := TurnHold(cs, "atlas"); got != "" {
		t.Errorf("atlas' own turn holds: %q", got)
	}
	step(cs, 1)
	step(cs, 1)
	if got := TurnHold(cs, "atlas"); got != "dark-factory's turn (weight 1; atlas next in 1 start)" {
		t.Errorf("after two atlas starts: %q", got)
	}
	if got := TurnHold(cs[:1], "dark-factory"); got != "" {
		t.Errorf("alone: %q", got)
	}
	// Decide says it as a hold, and the ask counts as waiting.
	s := &Share{Asks: map[string]Ask{"atlas": {At: t0, Weight: 3, Waiting: true, Below: 50, GapMinutes: 5}}}
	v := Decide(Input{Project: "dark-factory", State: onState(), Memory: okMemory(20), Now: t0, Share: s, Weight: 1, Candidates: []Candidate{{Slug: "df/x"}}})
	if v.Start || v.Reason != "atlas' turn (weight 3; dark-factory next in 2 starts)" || !v.Waiting {
		t.Errorf("Decide = %+v", v)
	}
	// Nothing ready says so, not the turn.
	v = Decide(Input{Project: "dark-factory", State: onState(), Memory: okMemory(20), Now: t0, Share: s, Weight: 1})
	if v.Reason != "nothing ready to start" || v.Waiting {
		t.Errorf("nothing ready = %+v", v)
	}
}

func TestShareLine(t *testing.T) {
	s := &Share{}
	if got := ShareLine(s.Rows("dark-factory", 1, t0)); got != "" {
		t.Errorf("empty share line = %q", got)
	}
	s.Note("atlas", 3, Verdict{Waiting: true, gap: 5}, t0)
	s.Started("atlas", "m/1", nil, t0)
	s.Started("atlas", "m/2", nil, t0)
	s.Starts = append(s.Starts, ShareStart{Project: "dark-factory", Slug: "old", At: t0.Add(-25 * time.Hour)})
	s.Started("dark-factory", "d/1", nil, t0)
	if got, want := ShareLine(s.Rows("dark-factory", 1, t0)), "atlas ×3 2 starts · dark-factory ×1 1 start"; got != want {
		t.Errorf("share line = %q, want %q", got, want)
	}
}

func TestWithShareRecordsAsksAndStarts(t *testing.T) {
	dir := t.TempDir()
	v := Verdict{Start: true, Slug: "m/1", Waiting: true, Below: 50, gap: 5,
		Contenders: []Contender{{Project: "dark-factory", Weight: 1}, {Project: "atlas", Weight: 3}}}
	err := WithShare(dir, func(b *Budget) (*Start, error) {
		b.Decided("atlas", 3, v, t0)
		b.Started("atlas", "m/1", v, t0)
		return &Start{Project: "atlas", Slug: "m/1", At: t0}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := LoadShare(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a := s.Asks["atlas"]; !a.Waiting || a.Weight != 3 || a.Below != 50 {
		t.Errorf("ask = %+v", a)
	}
	if len(s.Starts) != 1 || s.Starts[0].Slug != "m/1" {
		t.Errorf("starts = %+v", s.Starts)
	}
	if s.Credit["atlas"] != -1 {
		t.Errorf("credit = %v (atlas 3 - 4 = -1; dark-factory, with no ask, dropped)", s.Credit)
	}
	if last, _ := LastStart(dir); last == nil || last.Slug != "m/1" {
		t.Errorf("last = %+v", last)
	}
	// WithBudget keeps working, and a look (nothing Decided) writes no share.
	if err := WithBudget(dir, func(*Start) (*Start, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
}

// TestShareLinePrefixNamesTheWindow: the share line's window is ShareWindow,
// not a literal beside it (the viewer and autostart status both print it).
func TestShareLinePrefixNamesTheWindow(t *testing.T) {
	want := fmt.Sprintf("share of the start gap (%dh, by start_weight:): ", int(ShareWindow/time.Hour))
	if got := ShareLinePrefix(); got != want || !strings.Contains(got, "(24h,") {
		t.Errorf("ShareLinePrefix() = %q, want %q", got, want)
	}
}

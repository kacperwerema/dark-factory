package trace

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clilog"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var t0 = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

func call(min, exit int, args ...string) clilog.Record {
	e := exit
	return clilog.Record{TS: at(min).Format(time.RFC3339), Args: append([]string{"--project", "p"}, args...), Exit: &e}
}

var statuses = []string{"idea", "in design", "backlog", "in progress", "testing", "done"}

// A status entered twice is two spans; the gate's tries count the refused
// transitions to the same status; a move back marks both spans.
func TestBuildSpanPerVisit(t *testing.T) {
	in := In{
		Statuses: statuses,
		Status:   "testing",
		Start:    at(0),
		End:      at(100),
		Open:     true,
		Transitions: []tracker.TransitionStat{
			{From: "in progress", To: "testing", TS: at(30)},
			{From: "testing", To: "in progress", TS: at(40)},
			{From: "in progress", To: "testing", TS: at(70)},
		},
		Dispatches: []tracker.Dispatch{{At: at(0), Session: "agent-x", Agent: "claude"}},
		Calls: []clilog.Record{
			call(1, 0, "start", "x"),
			call(20, 1, "transition", "x", "--to", "testing"),
			call(25, 1, "transition", "x", "--to", "testing", "--dry-run"),
			call(29, 0, "transition", "x", "--to", "testing"),
			call(39, 0, "transition", "x", "--to", "in progress"),
			call(69, 0, "transition", "x", "--to", "testing"),
			call(80, 0, "show", "x"),
		},
		CallsKnown: true,
	}
	spans := Build(in)
	want := []string{"in progress", "testing", "in progress", "testing"}
	if len(spans) != len(want) {
		t.Fatalf("got %d spans, want %d: %+v", len(spans), len(want), spans)
	}
	for i, s := range spans {
		if s.Status != want[i] {
			t.Errorf("span %d status %q, want %q", i, s.Status, want[i])
		}
	}
	first := spans[0]
	if first.WallSec != 30*60 || first.CLICalls != 4 || first.CLIRefused != 2 {
		t.Errorf("first span: wall %v calls %d refused %d", first.WallSec, first.CLICalls, first.CLIRefused)
	}
	if g := first.Gate; g == nil || g.To != "testing" || g.Tries != 2 || g.FirstTry == nil || *g.FirstTry {
		t.Errorf("first gate %+v, want 2 tries, not first try", g)
	}
	if !spans[1].Back || spans[1].EnteredBack || !spans[2].EnteredBack || spans[2].Back {
		t.Errorf("back marks: %+v %+v", spans[1], spans[2])
	}
	if g := spans[2].Gate; g == nil || g.FirstTry == nil || !*g.FirstTry {
		t.Errorf("third gate %+v, want first try", g)
	}
	last := spans[3]
	if !last.Open || last.Gate != nil || !last.To.Equal(at(100)) || last.CLICalls != 1 {
		t.Errorf("open span %+v", last)
	}
	for _, s := range spans {
		if len(s.Agents) != 1 || s.Agents[0] != "claude" {
			t.Errorf("%s agents %v, want claude (the running dispatch)", s.Status, s.Agents)
		}
	}
}

// Tokens go to the visit their message is timed in; without a transcript
// they are unknown, not zero; a Codex visit's are unknown.
func TestBuildTokensByVisit(t *testing.T) {
	in := In{
		Statuses:    statuses,
		Status:      "testing",
		Start:       at(0),
		End:         at(60),
		Transitions: []tracker.TransitionStat{{From: "in progress", To: "testing", TS: at(30)}},
		Messages: []Message{
			{At: at(-5), Session: "s1", Model: "m", Input: 1, Output: 2},
			{At: at(10), Session: "s1", Model: "m", Input: 10, Output: 20, CacheRead: 100, CacheWrite: 5},
			{At: at(45), Session: "s2", Model: "n", Input: 7},
		},
		TokensKnown: true,
	}
	spans := Build(in)
	if len(spans) != 1 {
		t.Fatalf("closed at testing with no open span: %d spans", len(spans))
	}
	in.Open = true
	spans = Build(in)
	if got := spans[0].Tokens; !got.Known || got.In != 11 || got.Out != 22 || got.CacheRead != 100 || got.CacheWrite != 5 {
		t.Errorf("first span tokens %+v", got)
	}
	if got := spans[1].Tokens; got.In != 7 || strings.Join(spans[1].Sessions, ",") != "s2" || strings.Join(spans[1].Models, ",") != "n" {
		t.Errorf("second span %+v", spans[1])
	}
	in.TokensKnown, in.Messages = false, nil
	for _, s := range Build(in) {
		if s.Tokens.Known || TokensText(s.Tokens) != Unknown {
			t.Errorf("no transcript: %s tokens %+v shown %q", s.Status, s.Tokens, TokensText(s.Tokens))
		}
	}
	in.TokensKnown = true
	in.Dispatches = []tracker.Dispatch{{At: at(0), Agent: "codex"}}
	for _, s := range Build(in) {
		if s.Tokens.Known {
			t.Errorf("codex visit %s: tokens known", s.Status)
		}
	}
}

// The briefing of a visit is the first one sent in it; calls unknown
// without a clilog leave the gate's first try unknown.
func TestBuildBriefingAndUnknownCalls(t *testing.T) {
	in := In{
		Statuses:    statuses,
		Status:      "in design",
		End:         at(60),
		Open:        true,
		Transitions: []tracker.TransitionStat{{From: "idea", To: "in design", TS: at(10)}},
		Briefings: []tracker.Briefing{
			tracker.NewBriefing(tracker.BriefingDispatch, "idea", "agent-x", strings.Repeat("a", 4000), []string{"m-1"}, []string{"x#D-1"}, at(0)),
			tracker.NewBriefing(tracker.BriefingStart, "in design", "agent-x", strings.Repeat("b", 800), nil, nil, at(11)),
			tracker.NewBriefing(tracker.BriefingReprompt, "in design", "agent-x", "c", nil, nil, at(20)),
		},
	}
	spans := Build(in)
	if len(spans) != 2 {
		t.Fatalf("%d spans", len(spans))
	}
	if b := spans[0].Briefing; b == nil || b.Bytes != 4000 || b.Tokens != 1000 || b.Memories[0] != "m-1" || b.Decisions[0] != "x#D-1" {
		t.Errorf("first briefing %+v", b)
	}
	if spans[1].Briefing.Kind != tracker.BriefingStart || spans[1].Briefings != 2 {
		t.Errorf("second span briefing %+v (%d)", spans[1].Briefing, spans[1].Briefings)
	}
	if !spans[0].From.Equal(at(0)) {
		t.Errorf("start from the first record: %v", spans[0].From)
	}
	if g := spans[0].Gate; g.FirstTry != nil || GateText(spans[0]) != "→ in design "+Unknown || CallsText(spans[0]) != Unknown {
		t.Errorf("unknown calls: gate %+v, %q, %q", g, GateText(spans[0]), CallsText(spans[0]))
	}
}

// A trace holds sizes and ids, never the briefing's text: the file of a
// trace whose briefing held a marker word has no trace of it.
func TestTraceStoresNoText(t *testing.T) {
	const marker = "SECRETMARKERWORD"
	b := tracker.NewBriefing(tracker.BriefingDispatch, "idea", "agent-x", "brief "+marker+" text", nil, nil, at(0))
	spans := Build(In{Status: "idea", End: at(5), Open: true, Briefings: []tracker.Briefing{b}})
	path := Path(t.TempDir(), "Sys/x")
	if err := Write(path, spans); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte(marker)) {
		t.Fatalf("the trace stores briefing text:\n%s", data)
	}
	got, err := Read(path)
	if err != nil || len(got) != 1 || got[0].Briefing.Bytes != len("brief "+marker+" text") {
		t.Fatalf("read back %+v %v", got, err)
	}
	if filepath.Base(filepath.Dir(path)) != "Sys" {
		t.Errorf("path %s", path)
	}
}

func TestByStageAndTable(t *testing.T) {
	yes, no := true, false
	spans := []Span{
		{Status: "in progress", WallSec: 60, CLICalls: 3, Tokens: Tokens{Known: true, In: 10}, Gate: &Gate{To: "testing", Tries: 1, FirstTry: &yes}},
		{Status: "testing", WallSec: 30, Back: true, Gate: &Gate{To: "in progress", Tries: 2, FirstTry: &no}},
		{Status: "in progress", WallSec: 60, CLICalls: 1, Tokens: Tokens{Known: true, In: 5}, EnteredBack: true, Open: true},
	}
	st := ByStage(spans)
	if len(st) != 2 || st[0].Visits != 2 || st[0].Tokens != 15 || st[0].Gates != 1 || st[0].FirstTry != 1 || st[1].Back != 1 {
		t.Fatalf("by stage %+v", st)
	}
	var buf bytes.Buffer
	Table(&buf, spans)
	for _, want := range []string{"STATUS", "✓ first try", "✗ 2 tries", "went back", "came back", "open"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("table lacks %q:\n%s", want, buf.String())
		}
	}
}

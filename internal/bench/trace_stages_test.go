package bench

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clilog"
	"github.com/kacperwerema/dark-factory/internal/trace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func span(status string, tokens int64, wall float64, calls int, first *bool) trace.Span {
	s := trace.Span{Status: status, WallSec: wall, CLICalls: calls, CallsKnown: true, Tokens: trace.Tokens{Known: tokens > 0, In: tokens}}
	if first != nil {
		s.Gate = &trace.Gate{To: "next", Tries: 1, FirstTry: first}
	}
	return s
}

// AC5: compare by stage, per status A against B over the tasks both runs
// traced; a run without traces (before v0.63) gives no section.
func TestCompareByStage(t *testing.T) {
	yes, no := true, false
	a := &Run{Benchmark: "b", ID: "a", Stop: StopDone, Tasks: []TaskRecord{
		{Task: "p/one", Outcome: OutcomePass, Trace: []trace.Span{span("in design", 1000, 60, 4, &yes), span("in progress", 9000, 600, 20, &no)}},
		{Task: "p/two", Outcome: OutcomePass, Trace: []trace.Span{span("in design", 3000, 120, 6, &yes)}},
	}}
	b := &Run{Benchmark: "b", ID: "b", Stop: StopDone, Tasks: []TaskRecord{
		{Task: "p/one", Outcome: OutcomePass, Trace: []trace.Span{span("in design", 500, 30, 2, &yes), span("in progress", 4000, 300, 10, &yes)}},
		{Task: "p/two", Outcome: OutcomePass, Trace: []trace.Span{span("in design", 1500, 60, 4, &no)}},
	}}
	c := Compare(a, b)
	if len(c.Stages) != 2 || c.Stages[0].Status != "in design" || c.Stages[1].Status != "in progress" {
		t.Fatalf("stages %+v", c.Stages)
	}
	d := c.Stages[0]
	if d.A.Tasks != 2 || d.A.Tokens != 2000 || d.A.WallSec != 90 || d.A.Calls != 5 || d.A.FirstTry != 1 {
		t.Errorf("in design A %+v", d.A)
	}
	if d.B.Tokens != 1000 || d.B.FirstTry != 0.5 {
		t.Errorf("in design B %+v", d.B)
	}
	if p := c.Stages[1]; p.A.FirstTry != 0 || p.B.FirstTry != 1 || p.A.Tasks != 1 {
		t.Errorf("in progress %+v", p)
	}
	// An old run has no trace: no stages, the rest compares as before.
	old := &Run{Benchmark: "b", ID: "old", Stop: StopDone, Tasks: []TaskRecord{{Task: "p/one", Outcome: OutcomePass}, {Task: "p/two", Outcome: OutcomePass}}}
	oc := Compare(old, b)
	if oc.Stages != nil || !oc.Comparable || oc.Regressed() {
		t.Errorf("old run: stages %v comparable %v", oc.Stages, oc.Comparable)
	}
}

// AC5: a task's trace comes from the sandbox's stats sidecar and clilog and
// the worker's transcript messages; its first span carries the prompt's
// size, never its text.
func TestTaskTrace(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	issue := filepath.Join(dir, "tracker", "x.md")
	os.MkdirAll(filepath.Dir(issue), 0o755)
	wfPath := filepath.Join(dir, "workflow.yaml")
	os.WriteFile(wfPath, []byte("statuses:\n  - name: \"in progress\"\n  - name: \"testing\"\n  - name: \"done\"\ntransitions:\n  - from: \"in progress\"\n    to: \"testing\"\n  - from: \"testing\"\n    to: \"done\"\n"), 0o644)
	wf, err := tracker.LoadWorkflow(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	stats := tracker.StatsStore{Transitions: []tracker.TransitionStat{
		{From: "in progress", To: "testing", TS: start.Add(10 * time.Minute)},
		{From: "testing", To: "done", TS: start.Add(15 * time.Minute)},
	}}
	if err := tracker.SaveStats(issue, stats); err != nil {
		t.Fatal(err)
	}
	exit0, exit1 := 0, 1
	os.MkdirAll(filepath.Dir(clilog.ProjectPath(repo)), 0o755)
	for _, r := range []clilog.Record{
		{TS: start.Add(2 * time.Minute).Format(time.RFC3339), Args: []string{"transition", "x", "--to", "testing"}, Exit: &exit1},
		{TS: start.Add(10 * time.Minute).Format(time.RFC3339), Args: []string{"transition", "x", "--to", "testing"}, Exit: &exit0},
		{TS: start.Add(15 * time.Minute).Format(time.RFC3339), Args: []string{"transition", "x", "--to", "done"}, Exit: &exit0},
	} {
		if err := clilog.Append(clilog.ProjectPath(repo), r); err != nil {
			t.Fatal(err)
		}
	}
	sb := &sandbox{repo: repo, issue: issue, wf: wf, status: "in progress", refs: []string{"p/a#D-1"}}
	tr := Transcript{Messages: []trace.Message{{At: start.Add(time.Minute), Input: 100, Output: 10}, {At: start.Add(12 * time.Minute), Input: 50}}}
	const prompt = "the prompt PROMPTMARKER"
	rec := TaskRecord{Started: start, WallSec: 16 * 60, FinalStatus: "done"}
	spans := sb.trace(rec, tr, "claude", prompt)
	if len(spans) != 2 || spans[0].Status != "in progress" || spans[1].Status != "testing" {
		t.Fatalf("spans %+v", spans)
	}
	s := spans[0]
	if s.Briefing == nil || s.Briefing.Bytes != len(prompt) || s.Briefing.Decisions[0] != "p/a#D-1" || s.Tokens.In != 100 || s.Gate.Tries != 2 || *s.Gate.FirstTry {
		t.Errorf("first span %+v briefing %+v gate %+v", s, s.Briefing, s.Gate)
	}
	if spans[1].Tokens.In != 50 || !*spans[1].Gate.FirstTry || spans[1].Agents[0] != "claude" {
		t.Errorf("second span %+v", spans[1])
	}
	var buf bytes.Buffer
	if err := trace.Write(filepath.Join(dir, "t.jsonl"), spans); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "t.jsonl"))
	buf.Write(data)
	if strings.Contains(buf.String(), "PROMPTMARKER") {
		t.Error("the trace stores the prompt's text")
	}
}

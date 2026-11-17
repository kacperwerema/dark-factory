package specflow

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/critique"
	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var t0 = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func run(slug, from, to string, at time.Duration, blocks int) *critique.Run {
	r := &critique.Run{ID: slug + at.String(), Slug: slug, From: from, To: to, At: t0.Add(at), Agent: "claude"}
	for i := 0; i < blocks; i++ {
		r.Report.Findings = append(r.Report.Findings, critique.Finding{Severity: critique.Blocks, Path: "a.go", Text: "x"})
	}
	r.Report.Findings = append(r.Report.Findings, critique.Finding{Severity: critique.Should, Path: "a.go", Text: "y"})
	return r
}

// A held gated run's outcome comes from the next run on the same issue and
// edge that didn't fail; a failed run never counts as clear or held, and a
// run by hand is never held.
func TestSpecFlowHeldRunOutcome(t *testing.T) {
	fixedHeld := run("f/fixed", "in design", "backlog", 0, 2)
	fixedFail := run("f/fixed", "in design", "backlog", time.Minute, 0)
	fixedFail.Failed, fixedFail.Error = true, "timed out"
	fixedClear := run("f/fixed", "in design", "backlog", 2*time.Minute, 0)
	otherEdge := run("f/fixed", "testing", "shipping", 90*time.Second, 1) // another edge: not the next run

	again1 := run("f/again", "testing", "shipping", 0, 1)
	again2 := run("f/again", "testing", "shipping", time.Hour, 1)

	over := run("f/over", "in design", "backlog", 0, 1)
	over.Override = &critique.Override{At: t0.Add(time.Hour), Quote: "go"}

	byHand := run("f/hand", "in progress", "", 0, 3)
	problem := run("f/problem", "in design", "backlog", 0, 0)
	problem.Report.Problems = []string{"no Coverage line"}

	runs := []*critique.Run{fixedHeld, fixedFail, fixedClear, otherEdge, again1, again2, over, byHand, problem}
	want := map[*critique.Run]string{
		fixedHeld: Fixed, fixedFail: "", fixedClear: "", otherEdge: StillHolds,
		again1: HeldAgain, again2: StillHolds, over: Overridden, byHand: "", problem: StillHolds,
	}
	for r, w := range want {
		if got := Outcome(runs, r); got != w {
			t.Errorf("%s %s at %s: outcome %q, want %q", r.Slug, r.Edge(), r.At.Format("15:04:05"), got, w)
		}
	}

	rep := Gather(In{Runs: runs})
	tot := rep.Critique.Totals
	if tot.Runs != 9 || tot.Gated != 8 || tot.ByHand != 1 || tot.Failed != 1 {
		t.Errorf("runs: %+v", tot)
	}
	if tot.Held != 6 || tot.Fixed != 1 || tot.HeldAgain != 1 || tot.Overridden != 1 || tot.StillHolds != 3 {
		t.Errorf("held: %+v", tot)
	}
	if tot.Blocks != 2+1+1+1+1+3+1 || tot.Should != 9 {
		t.Errorf("findings: blocks %d should %d", tot.Blocks, tot.Should)
	}
	for _, row := range rep.Critique.Runs {
		if row.Verdict == "failed" && (row.Outcome != "" || row.Verdict == "clear") {
			t.Errorf("failed run counted: %+v", row)
		}
		if row.Issue == "f/hand" && (row.Verdict != "findings" || row.Outcome != "") {
			t.Errorf("by hand: %+v", row)
		}
	}
	// No run was timed: "—", never 0.
	if tot.Timed != 0 || tot.MedianTook() != "—" || rep.Critique.Runs[0].Took() != "—" {
		t.Errorf("untimed: %+v %q", tot, tot.MedianTook())
	}
	fixedHeld.TookMS, again1.TookMS, over.TookMS = 60_000, 180_000, 120_000
	tot = Gather(In{Runs: runs}).Critique.Totals
	if tot.Timed != 3 || tot.MedianTook() != "2m0s" {
		t.Errorf("timed: %d, median %s", tot.Timed, tot.MedianTook())
	}

	// The range bounds the rows, never the next run that decides an outcome.
	rep = Gather(In{Runs: runs, From: t0, To: t0.Add(time.Second)})
	for _, row := range rep.Critique.Runs {
		if row.Issue == "f/fixed" && row.Outcome != Fixed {
			t.Errorf("the next run is out of range but decides: %+v", row)
		}
	}
	if len(rep.Critique.Runs) != 5 {
		t.Errorf("in range: %d rows", len(rep.Critique.Runs))
	}
}

func issue(t *testing.T, slug, body, comments string) *tracker.Issue {
	t.Helper()
	raw := "---\ntitle: \"T " + slug + "\"\nstatus: \"backlog\"\ncreated: \"2026-11-01\"\n---\n\n" + body
	if comments != "" {
		raw += "\n<!-- issue-viewer-comments\n" + comments + "\n-->\n"
	}
	is, err := tracker.ParseIssue(slug+".md", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	is.Slug = slug
	return is
}

func comments(t *testing.T, cs ...tracker.Comment) string {
	t.Helper()
	var lines []string
	for _, c := range cs {
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n")
}

const clarifyBody = `## Clarify

### Q1. One?
1. **A** (recommended) why. Cost: none.
2. **B.** Cost: some.
**Answer:** 2 · 2026-11-12 · owner: "B please" (D-1)

### Q2. Two?
1. **A** (recommended) why. Cost: none.
**Answer:** assumed — took the recommendation

### Q3. Three?
1. **A** (recommended) why. Cost: none.
**Answer:** —
`

// Clarify counts owner, assumed and open per design and Nothing to ask as
// none; red-round questions count raised, open and resolved per check;
// post-mortem actions count from the newest post-mortem's marks.
func TestSpecFlowGather(t *testing.T) {
	q := func(id int, check, date, status string) tracker.Comment {
		return tracker.Comment{ID: id, Date: date, Text: "question: red rounds: " + check + " — red 4 of 4", Status: status}
	}
	issues := []*tracker.Issue{
		issue(t, "f/asked", clarifyBody, comments(t,
			q(1, "make validate", "2026-11-11T10:00:00Z", "done"),
			q(2, "make validate", "2026-11-12T10:00:00Z", "open"),
			q(3, "critique in design → backlog", "2026-11-12T11:00:00Z", "open"),
			tracker.Comment{ID: 4, Date: "2026-11-12T12:00:00Z", Text: "progress: fine", Status: "open"},
		)),
		issue(t, "f/nothing", "## Clarify\n\nNothing to ask: the brief says it all.\n", ""),
		issue(t, "f/none", "## Design\n\nNo round.\n", ""),
	}
	pms := []postmortem.Record{{Issue: "f/asked", Title: "Asked", Date: t0, Path: "retros/x-postmortem-f-asked.md", Actions: []postmortem.Action{
		{ID: "A1", Done: true, Applied: "memory m-1"},
		{ID: "A2", Refused: "not now"},
		{ID: "A3", Overtaken: "f/other"},
		{ID: "A4"},
		{ID: "A5", Done: true},
	}}}
	rep := Gather(In{Issues: issues, PostMortems: pms})

	cl := rep.Clarify
	if len(cl.Rows) != 2 || cl.Totals.Designs != 2 || cl.Totals.Nothing != 1 || cl.Totals.Questions != 3 || cl.Totals.Owner != 1 || cl.Totals.Assumed != 1 || cl.Totals.Open != 1 {
		t.Fatalf("clarify: %+v", cl)
	}
	if cl.Rows[0].Issue != "f/asked" || cl.Rows[0].Date != "2026-11-12" || cl.Rows[1].Date != "2026-11-01" || !cl.Rows[1].Nothing {
		t.Errorf("clarify rows (dated by the newest owner answer, else created): %+v", cl.Rows)
	}
	if cl.Totals.PerDesign() != 1.5 {
		t.Errorf("per design %v", cl.Totals.PerDesign())
	}

	rr := rep.RedRounds
	if rr.Totals.Issues != 1 || rr.Totals.Raised != 3 || rr.Totals.Open != 2 || rr.Totals.Resolved != 1 || len(rr.Rows) != 2 {
		t.Fatalf("red rounds: %+v", rr)
	}
	if r := rr.Rows[1]; r.Check != "make validate" || r.Raised != 2 || r.Open != 1 || r.Resolved != 1 {
		t.Errorf("make validate row: %+v", r)
	}

	pm := rep.PostMortem.Totals
	if pm.PostMortems != 1 || pm.Proposed != 5 || pm.Applied != 2 || pm.Refused != 1 || pm.Overtaken != 1 || pm.Waiting != 1 {
		t.Errorf("post-mortem: %+v", pm)
	}

	// Range: a day counts when any of it is in range.
	from := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	rep = Gather(In{Issues: issues, PostMortems: pms, From: from, To: from.AddDate(0, 0, 1)})
	if len(rep.Clarify.Rows) != 1 || rep.RedRounds.Totals.Raised != 2 || len(rep.PostMortem.Rows) != 0 {
		t.Errorf("ranged: clarify %d, red %d, post-mortems %d", len(rep.Clarify.Rows), rep.RedRounds.Totals.Raised, len(rep.PostMortem.Rows))
	}
	if !Gather(In{}).Empty() || rep.Empty() {
		t.Error("Empty")
	}
}

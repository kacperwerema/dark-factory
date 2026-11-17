package viewer

import (
	"fmt"
	"time"

	"github.com/kacperwerema/dark-factory/internal/specflow"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The Stats page's Spec flow section: the four blocks specflow counts
// (the same numbers as issue-cli stats --spec-flow), one table card each,
// over the page's range. Not cached: one log, the issues the page already
// loaded, and only the *-postmortem-* retros.

// specFlowRows is how many rows a card shows; the CLI lists them all.
const specFlowRows = 40

// WbStatsSpecFlow is the section.
type WbStatsSpecFlow struct {
	Sub    string
	Tables []WbTable
	// Err says what could not be read; the section still renders.
	Err string
}

// loadSpecFlow counts proj's spec-flow records over [from, to).
func loadSpecFlow(proj *tracker.Project, prefix, rangeLabel string, issues []*tracker.Issue, from, to time.Time) WbStatsSpecFlow {
	rep, err := specflow.Load(proj.IssueDir, postmortemRetros(proj), issues, from, to)
	if err != nil {
		return WbStatsSpecFlow{Err: "Could not read the critique log: " + err.Error()}
	}
	return buildSpecFlow(rep, proj.Slug, prefix, rangeLabel)
}

func buildSpecFlow(rep specflow.Report, slug, prefix, rangeLabel string) WbStatsSpecFlow {
	cmd := func(s string) string { return fmt.Sprintf("issue-cli --project %s %s", slug, s) }
	issueURL := func(s string) string { return prefix + "/issue/" + s }
	more := func(t *WbTable, n int) {
		if n > specFlowRows {
			t.Rows = t.Rows[:specFlowRows]
			t.Sub += fmt.Sprintf(" · newest %d of %d shown; all: %s", specFlowRows, n, cmd("stats --spec-flow"))
		}
	}
	sec := WbStatsSpecFlow{Sub: rangeLabel + ": the critic, the clarify round, the red-round cap and post-mortem actions, from their records"}

	ct := rep.Critique.Totals
	crit := WbTable{Key: "specflow-critique", Title: "Critique runs",
		Sub: fmt.Sprintf("%s (%d gated, %d failed) · %d blocks, %d should, %d note · held a gate %d: fixed %d, overridden %d, held again %d, still holds %d · took: median %s (%d of %d timed; tokens not recorded)",
			plural(ct.Runs, "run", "runs"), ct.Gated, ct.Failed, ct.Blocks, ct.Should, ct.Note, ct.Held, ct.Fixed, ct.Overridden, ct.HeldAgain, ct.StillHolds, ct.MedianTook(), ct.Timed, ct.Runs),
		Head:  []WbTH{{Text: "When"}, {Text: "Issue"}, {Text: "Edge"}, {Text: "Agent"}, {Text: "Verdict"}, {Text: "Blocks · should · note", Num: true}, {Text: "Took", Num: true}, {Text: "Cleared"}},
		Wide:  true,
		Empty: &WbEmpty{Text: "No critique run in this range. A fresh critic's run is recorded before a gate that asks for one.", Cmd: cmd("critique <slug>")}}
	for _, r := range rep.Critique.Runs {
		crit.Rows = append(crit.Rows, []WbCell{
			{Text: r.At.Local().Format("Jan 2 15:04")}, {Text: r.Issue, URL: issueURL(r.Issue)}, {Text: r.Edge}, {Text: orDash(r.Agent)}, {Text: r.Verdict},
			{Text: fmt.Sprintf("%d · %d · %d", r.Blocks, r.Should, r.Note), Num: true},
			{Text: r.Took(), Num: true}, {Text: orDash(r.Outcome)},
		})
	}
	more(&crit, len(rep.Critique.Runs))

	t := rep.Clarify.Totals
	clar := WbTable{Key: "specflow-clarify", Title: "Clarify rounds",
		Sub:   fmt.Sprintf("%s (%d with nothing to ask) · %s, %.1f per design: %d owner-answered, %d assumed, %d open", plural(t.Designs, "design", "designs"), t.Nothing, plural(t.Questions, "question", "questions"), t.PerDesign(), t.Owner, t.Assumed, t.Open),
		Head:  []WbTH{{Text: "Date"}, {Text: "Issue"}, {Text: "Questions", Num: true}, {Text: "Owner", Num: true}, {Text: "Assumed", Num: true}, {Text: "Open", Num: true}},
		Empty: &WbEmpty{Text: "No clarify round in this range. The design step writes the owner's questions once, under ## Clarify.", Cmd: cmd("clarify <slug>")}}
	for _, r := range rep.Clarify.Rows {
		q := fmt.Sprint(r.Questions)
		if r.Nothing {
			q = "nothing to ask"
		}
		clar.Rows = append(clar.Rows, []WbCell{{Text: r.Date}, {Text: r.Issue, URL: issueURL(r.Issue)}, {Text: q, Num: true},
			{Text: fmt.Sprint(r.Owner), Num: true}, {Text: fmt.Sprint(r.Assumed), Num: true}, {Text: fmt.Sprint(r.Open), Num: true}})
	}
	more(&clar, len(rep.Clarify.Rows))

	rt := rep.RedRounds.Totals
	red := WbTable{Key: "specflow-redrounds", Title: "Red-round questions",
		Sub:   fmt.Sprintf("%s on %s · %d open, %d resolved", plural(rt.Raised, "question", "questions"), plural(rt.Issues, "issue", "issues"), rt.Open, rt.Resolved),
		Head:  []WbTH{{Text: "Issue"}, {Text: "Check"}, {Text: "Raised", Num: true}, {Text: "Open", Num: true}, {Text: "Resolved", Num: true}},
		Empty: &WbEmpty{Text: "No check went past the red-round cap in this range.", Cmd: cmd("rounds <slug>")}}
	for _, r := range rep.RedRounds.Rows {
		red.Rows = append(red.Rows, []WbCell{{Text: r.Issue, URL: issueURL(r.Issue)}, {Text: r.Check, Code: true},
			{Text: fmt.Sprint(r.Raised), Num: true}, {Text: fmt.Sprint(r.Open), Num: true}, {Text: fmt.Sprint(r.Resolved), Num: true}})
	}
	more(&red, len(rep.RedRounds.Rows))

	pt := rep.PostMortem.Totals
	pm := WbTable{Key: "specflow-postmortem", Title: "Post-mortem actions",
		Sub:   fmt.Sprintf("%s proposed in %s · %d applied, %d refused, %d overtaken, %d waiting", plural(pt.Proposed, "action", "actions"), plural(pt.PostMortems, "post-mortem", "post-mortems"), pt.Applied, pt.Refused, pt.Overtaken, pt.Waiting),
		Head:  []WbTH{{Text: "Date"}, {Text: "Issue"}, {Text: "Proposed", Num: true}, {Text: "Applied", Num: true}, {Text: "Refused", Num: true}, {Text: "Overtaken", Num: true}, {Text: "Waiting", Num: true}},
		Empty: &WbEmpty{Text: "No post-mortem in this range. One is written when an issue closes.", Cmd: cmd("postmortem list")}}
	for _, r := range rep.PostMortem.Rows {
		pm.Rows = append(pm.Rows, []WbCell{{Text: r.Date.Local().Format("Jan 2")}, {Text: r.Issue, URL: issueURL(r.Issue)},
			{Text: fmt.Sprint(r.Proposed), Num: true}, {Text: fmt.Sprint(r.Applied), Num: true}, {Text: fmt.Sprint(r.Refused), Num: true},
			{Text: fmt.Sprint(r.Overtaken), Num: true}, {Text: fmt.Sprint(r.Waiting), Num: true}})
	}
	more(&pm, len(rep.PostMortem.Rows))

	sec.Tables = []WbTable{crit, clar, red, pm}
	for i := range sec.Tables {
		if len(sec.Tables[i].Rows) == 0 {
			sec.Tables[i].Sub = "" // no zeros dressed as data: the empty state says it
		}
	}
	return sec
}

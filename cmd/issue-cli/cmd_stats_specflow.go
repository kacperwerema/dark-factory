package main

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/kacperwerema/dark-factory/internal/period"
	"github.com/kacperwerema/dark-factory/internal/specflow"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// printStatsSpecFlow prints the four spec-flow blocks over the last days
// calendar days, or every record with all.
func printStatsSpecFlow(ctx *Context, proj *tracker.Project, issues []*tracker.Issue, days int, all bool) error {
	if days < 1 {
		return &usageError{msg: "stats --spec-flow: --days must be 1 or more"}
	}
	var from time.Time
	window := "all records"
	if !all {
		from = period.DaysBack(ctx.now(), days)
		window = fmt.Sprintf("since %s (%d days)", from.Format("2006-01-02"), days)
	}
	rep, err := specflow.Load(proj.IssueDir, retrosDir(ctx), issues, from, time.Time{})
	if err != nil {
		return fmt.Errorf("cannot read the critique log: %w", err)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, rep)
	}
	w := ctx.Stdout
	cmd := func(s string) string { return tracker.InjectProject(proj, s) }
	fmt.Fprintf(w, "== Spec flow: %s ==\n", window)

	c := rep.Critique
	ct := c.Totals
	fmt.Fprintf(w, "\nCritique runs: %d (%d gated, %d by hand, %d failed) · findings: %d blocks, %d should, %d note\n", ct.Runs, ct.Gated, ct.ByHand, ct.Failed, ct.Blocks, ct.Should, ct.Note)
	if ct.Runs == 0 {
		fmt.Fprintf(w, "  No critique run in range. A run is recorded by: %s\n", cmd("issue-cli critique <slug>"))
	} else {
		fmt.Fprintf(w, "Held a gate: %d — fixed %d, overridden %d, held again %d, still holds %d · took: median %s (%d of %d timed; tokens not recorded)\n", ct.Held, ct.Fixed, ct.Overridden, ct.HeldAgain, ct.StillHolds, ct.MedianTook(), ct.Timed, ct.Runs)
		table(w, "WHEN\tISSUE\tEDGE\tAGENT\tVERDICT\tBLOCKS\tSHOULD\tNOTE\tTOOK\tCLEARED", func(tw io.Writer) {
			for _, r := range c.Runs {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\n", r.At.Local().Format("2006-01-02 15:04"), r.Issue, r.Edge, dash(r.Agent), r.Verdict, r.Blocks, r.Should, r.Note, r.Took(), dash(r.Outcome))
			}
		})
	}

	cl := rep.Clarify
	t := cl.Totals
	fmt.Fprintf(w, "\nClarify rounds: %d designs (%d with nothing to ask) · %d questions, %.1f per design: %d owner-answered, %d assumed, %d open\n", t.Designs, t.Nothing, t.Questions, t.PerDesign(), t.Owner, t.Assumed, t.Open)
	if t.Designs == 0 {
		fmt.Fprintf(w, "  No clarify round in range. The design step writes one: %s\n", cmd("issue-cli clarify <slug>"))
	} else {
		table(w, "DATE\tISSUE\tQUESTIONS\tOWNER\tASSUMED\tOPEN", func(tw io.Writer) {
			for _, r := range cl.Rows {
				q := fmt.Sprint(r.Questions)
				if r.Nothing {
					q = "nothing to ask"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\n", r.Date, r.Issue, q, r.Owner, r.Assumed, r.Open)
			}
		})
	}

	rr := rep.RedRounds
	fmt.Fprintf(w, "\nRed-round questions: %d raised on %d issues · %d open, %d resolved\n", rr.Totals.Raised, rr.Totals.Issues, rr.Totals.Open, rr.Totals.Resolved)
	if rr.Totals.Raised == 0 {
		fmt.Fprintf(w, "  No check went past the red-round cap in range. %s shows each red check's count.\n", cmd("issue-cli rounds <slug>"))
	} else {
		table(w, "ISSUE\tCHECK\tRAISED\tOPEN\tRESOLVED", func(tw io.Writer) {
			for _, r := range rr.Rows {
				fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\n", r.Issue, r.Check, r.Raised, r.Open, r.Resolved)
			}
		})
	}

	pm := rep.PostMortem
	pt := pm.Totals
	fmt.Fprintf(w, "\nPost-mortem actions: %d proposed in %d post-mortems · %d applied, %d refused, %d overtaken, %d waiting\n", pt.Proposed, pt.PostMortems, pt.Applied, pt.Refused, pt.Overtaken, pt.Waiting)
	if pt.PostMortems == 0 {
		fmt.Fprintf(w, "  No post-mortem in range. One is written when an issue closes; every one: %s\n", cmd("issue-cli postmortem list"))
	} else {
		table(w, "DATE\tISSUE\tPROPOSED\tAPPLIED\tREFUSED\tOVERTAKEN\tWAITING", func(tw io.Writer) {
			for _, r := range pm.Rows {
				fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\n", r.Date.Local().Format("2006-01-02"), r.Issue, r.Proposed, r.Applied, r.Refused, r.Overtaken, r.Waiting)
			}
		})
	}
	return nil
}

func table(w io.Writer, head string, rows func(io.Writer)) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  "+head)
	rows(&indent{tw})
	tw.Flush()
}

// indent puts two spaces before each row a table writes.
type indent struct{ w io.Writer }

func (i *indent) Write(p []byte) (int, error) {
	if _, err := i.w.Write(append([]byte("  "), p...)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// specFlowHint is plain stats' last line: what the spec-flow records hold,
// and the command that counts them; "" when there is none.
func specFlowHint(ctx *Context, proj *tracker.Project, issues []*tracker.Issue) string {
	rep, err := specflow.Load(proj.IssueDir, retrosDir(ctx), issues, time.Time{}, time.Time{})
	if err != nil || rep.Empty() {
		return ""
	}
	return fmt.Sprintf("Spec flow: %d critique runs, %d held a gate · %d clarify rounds · %d post-mortems — %s",
		rep.Critique.Totals.Runs, rep.Critique.Totals.Held, rep.Clarify.Totals.Designs, rep.PostMortem.Totals.PostMortems, tracker.InjectProject(proj, "issue-cli stats --spec-flow"))
}

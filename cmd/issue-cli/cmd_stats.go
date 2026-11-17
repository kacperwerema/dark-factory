package main

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/kacperwerema/dark-factory/internal/period"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tokens"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var statsCommand = &Command{
	Name:      "stats",
	ShortHelp: "Project health overview",
	LongHelp:  "Print issue counts grouped by status, system, and assignee. Pass --json for machine-readable output.\n\n--by-model [--days N] compares the issues done in the last N days (default 30) by type of work and the model their worker ran on (the stats sidecar's dispatches; routing: rules model:): issues, reopen rate (a backward transition), red rounds and tokens (the worker sessions' transcripts, issue-cli spend's 4-week window; older work is unknown).\n\n--spec-flow [--days N | --all] counts what the spec-flow pieces left: critique runs (findings by severity, which blocks held a gate and how each was cleared: fixed, overridden, held again, still holds; how long the critic took), clarify rounds (owner-answered, assumed, open), red-round questions and post-mortem actions (applied, refused, overtaken, waiting). Default: the last 30 calendar days; --json prints the report. The Stats page's Spec flow section shows the same.",
	Run:       runStats,
}

func init() {
	registerCommand(statsCommand)
}

func runStats(ctx *Context, args []string) error {
	fs := newFlagSet("stats", ctx)
	byModel := fs.Bool("by-model", false, "compare done issues by type and the model their worker ran on")
	specFlow := fs.Bool("spec-flow", false, "count critique runs, clarify rounds, red-round questions and post-mortem actions")
	days := fs.Int("days", 30, "with --by-model or --spec-flow: the last N days")
	all := fs.Bool("all", false, "with --spec-flow: every record, no range")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}

	proj := ctx.Project
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	if *byModel {
		return printStatsByModel(ctx, proj, issues, *days)
	}
	if *all && !*specFlow {
		return &usageError{msg: "stats: --all goes with --spec-flow"}
	}
	if *specFlow {
		return printStatsSpecFlow(ctx, proj, issues, *days, *all)
	}

	byStatus := map[string]int{}
	bySystem := map[string]int{}
	byAssignee := map[string]int{}
	for _, issue := range issues {
		byStatus[issue.Status]++
		if issue.System != "" {
			bySystem[issue.System]++
		}
		if issue.Assignee != "" {
			byAssignee[issue.Assignee]++
		}
	}

	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]interface{}{
			"total":       len(issues),
			"by_status":   byStatus,
			"by_system":   bySystem,
			"by_assignee": byAssignee,
		})
	}

	fmt.Fprintf(ctx.Stdout, "== Project Stats (%d issues) ==\n\n", len(issues))

	wf := proj.LoadWorkflow()
	fmt.Fprintln(ctx.Stdout, "By status:")
	for _, s := range wf.GetStatusOrder() {
		if n, ok := byStatus[s]; ok {
			label := s
			if st := wf.GetStatus(s); st != nil && st.Optional {
				label = s + " (optional)"
			}
			fmt.Fprintf(ctx.Stdout, "  %-24s %d\n", label, n)
		}
	}

	fmt.Fprintln(ctx.Stdout, "\nBy system:")
	for sys, n := range bySystem {
		fmt.Fprintf(ctx.Stdout, "  %-15s %d\n", sys, n)
	}

	if len(byAssignee) > 0 {
		fmt.Fprintln(ctx.Stdout, "\nBy assignee:")
		for a, n := range byAssignee {
			fmt.Fprintf(ctx.Stdout, "  %-15s %d\n", a, n)
		}
	}
	if anyModelRecorded(issues) {
		fmt.Fprintf(ctx.Stdout, "\nBy model: %s\n", tracker.InjectProject(proj, "issue-cli stats --by-model"))
	}
	if line := specFlowHint(ctx, proj, issues); line != "" {
		fmt.Fprintf(ctx.Stdout, "\n%s\n", line)
	}
	return nil
}

// anyModelRecorded reports whether some issue's sidecar has a dispatch
// with a model.
func anyModelRecorded(issues []*tracker.Issue) bool {
	for _, issue := range issues {
		store, err := tracker.LoadStats(issue.FilePath)
		if err != nil {
			continue
		}
		for _, d := range store.Dispatches {
			if d.Model != "" {
				return true
			}
		}
	}
	return false
}

// statsModelRow is a ModelRow as --json gives it.
type statsModelRow struct {
	Type        string   `json:"type"`
	Model       string   `json:"model"`
	Issues      int      `json:"issues"`
	Reopened    int      `json:"reopened"`
	ReopenRate  float64  `json:"reopen_rate"`
	RedRounds   int      `json:"red_rounds"`
	RedP50      float64  `json:"red_rounds_p50"`
	TokensP50   *float64 `json:"tokens_p50"`
	TokensKnown int      `json:"tokens_known"`
}

// printStatsByModel prints the done issues of the last days by type and
// model: the project aggregate's model rows, with tokens from the
// transcripts.
func printStatsByModel(ctx *Context, proj *tracker.Project, issues []*tracker.Issue, days int) error {
	if days < 1 {
		return &usageError{msg: "stats --by-model: --days must be 1 or more"}
	}
	now := ctx.now()
	// Calendar days (a DST day is 23 or 25 hours): from midnight days back.
	rep := timeline.Aggregate(context.Background(), proj, nil, issues, now, now.Sub(period.DaysBack(now, days)))
	sp := spend.Load(proj, issues, now, spend.Options{})
	rep = rep.WithTokens(sp.IssueTokens(), sp.Since)
	if ctx.JSONOutput {
		rows := []statsModelRow{}
		for _, m := range rep.Models {
			r := statsModelRow{Type: m.Type, Model: m.Model, Issues: m.N, Reopened: m.Reopened, ReopenRate: m.ReopenRate(), RedRounds: m.RedRounds, RedP50: m.RedP50, TokensKnown: m.TokensKnown}
			if m.TokensKnown > 0 {
				t := m.Tokens
				r.TokensP50 = &t
			}
			rows = append(rows, r)
		}
		return writeJSON(ctx.Stdout, map[string]any{"days": days, "tokens_since": sp.Since, "rows": rows})
	}
	fmt.Fprintf(ctx.Stdout, "== By model: issues done in the last %d days ==\n\n", days)
	if len(rep.Models) == 0 {
		fmt.Fprintln(ctx.Stdout, "No done issue in the window was dispatched with a recorded launch. Dispatches record their model from now on; routing: rules name one with model: (docs/agent-dispatch.md).")
		return nil
	}
	tw := tabwriter.NewWriter(ctx.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tMODEL\tISSUES\tREOPENED\tRED ROUNDS (sum, p50)\tTOKENS p50")
	for _, m := range rep.Models {
		tok := "unknown"
		if m.TokensKnown > 0 {
			tok = tokens.Format(int64(m.Tokens))
			if m.TokensKnown < m.N {
				tok += fmt.Sprintf(" (%d of %d read)", m.TokensKnown, m.N)
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d (%.0f%%)\t%d, %.0f\t%s\n", m.Type, m.Model, m.N, m.Reopened, m.ReopenRate()*100, m.RedRounds, m.RedP50, tok)
	}
	tw.Flush()
	fmt.Fprintf(ctx.Stdout, "\n%s is a worker started without --model (the project's or the CLI's own); %s ran on more than one. Tokens: worker transcripts since %s.\n", tracker.ModelUnnamed, tracker.ModelMixed, sp.Since.Format("2006-01-02"))
	return nil
}

package main

import (
	"fmt"
	"io"

	"strings"

	"text/tabwriter"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/experiment"

	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tokens"
	"github.com/kacperwerema/dark-factory/internal/trace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func runBenchReport(ctx *Context, args []string) error {
	pos, err := parsePositionals(ctx, "bench report", args, 1)
	if err != nil {
		return err
	}
	id := ""
	if len(pos) == 1 {
		id = pos[0]
	} else {
		names := bench.List()
		if len(names) != 1 {
			return &usageError{msg: "usage: issue-cli bench report <benchmark>/<run|latest>"}
		}
		id = names[0] + "/latest"
	}
	if !strings.Contains(id, "/") && bench.ValidName(id) {
		if _, err := bench.Load(id); err == nil {
			id += "/latest"
		}
	}
	r, err := bench.FindRun("", id)
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, r)
	}
	printRun(ctx.Stdout, r)
	printByAgent(ctx, bench.ByAgent(r))
	return nil
}

func runBenchCompare(ctx *Context, args []string) error {
	fs := newFlagSet("bench compare", ctx)
	branch := fs.String("branch", "", "mark the workflows this ref changed since main (the rest are controls)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 && fs.NArg() != 2 {
		return &usageError{msg: "usage: issue-cli bench compare <benchmark>/<run> (by agent) or <benchmark>/<runA> <benchmark>/<runB> [--branch <ref>]"}
	}
	a, err := bench.FindRun("", fs.Arg(0))
	if err != nil {
		return err
	}
	if fs.NArg() == 1 {
		return runBenchCompareAgents(ctx, a)
	}
	b, err := bench.FindRun("", fs.Arg(1))
	if err != nil {
		return err
	}
	c := bench.Compare(a, b)
	c.Routing = bench.CheckRouting(projectRouting(ctx), c.ByAgent)
	if ref := strings.TrimSpace(*branch); ref != "" {
		aff, err := branchAffected(ctx.Project, ".", ref)
		if err != nil {
			return fmt.Errorf("--branch %w", err)
		}
		c.MarkChanged(aff)
	}
	code := 0
	switch {
	case !c.Comparable:
		code = 2
	case c.Regressed():
		code = 1
	}
	if ctx.JSONOutput {
		if err := writeJSON(ctx.Stdout, c); err != nil {
			return err
		}
	} else {
		printComparison(ctx.Stdout, c)
		printByAgent(ctx, c.ByAgent)
	}
	if code != 0 {
		return &exitCodeError{Code: code}
	}
	return nil
}

// runBenchCompareAgents is `bench compare <run>`: one run, by agent.
func runBenchCompareAgents(ctx *Context, r *bench.Run) error {
	t := bench.ByAgent(r)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"run": r.Benchmark + "/" + r.ID, "by_agent": t, "routing": bench.CheckRouting(projectRouting(ctx), t)})
	}
	if t == nil {
		fmt.Fprintf(ctx.Stdout, "%s/%s ran on one agent, so there is nothing to set side by side: issue-cli bench run %s --agents claude,codex\n", r.Benchmark, r.ID, r.Benchmark)
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "== Bench by agent %s/%s ==\nresult: %s\n", r.Benchmark, r.ID, runLine(r))
	printByAgent(ctx, t)
	return nil
}

// projectRouting is the project's routing: rules, nil without a project.
func projectRouting(ctx *Context) *tracker.RoutingConfig {
	if ctx.Project == nil {
		return nil
	}
	if wf := ctx.Project.LoadWorkflow(); wf != nil {
		return wf.Routing
	}
	return nil
}

// printByAgent is the table by agent and type, then what it says of the
// routing: rules and of the prices; nothing for a nil table.
func printByAgent(ctx *Context, t *bench.AgentTable) {
	if t == nil {
		return
	}
	w := ctx.Stdout
	by := "agent"
	for _, a := range t.Agents {
		if strings.Contains(a, " · ") {
			by = "agent and model"
			break
		}
	}
	fmt.Fprintf(w, "\nBy %s (mean per task; the holdout stays in the overall n/m):\n", by)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  type\tagent\tpass\ttime\ttokens\tcost\t")
	for _, r := range t.Rows {
		note := ""
		if r.NotScored > 0 {
			note = fmt.Sprintf("%d not scored (error or budget)", r.NotScored)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Type, r.Agent, r.PassText(), r.WallText(), r.TokensText(), r.CostText(), note)
	}
	tw.Flush()
	if note := spend.PricesNote(t.Unpriced); note != "" {
		fmt.Fprintf(w, "⚠ %s\n", note)
	}
	for _, c := range bench.CheckRouting(projectRouting(ctx), t) {
		fmt.Fprintf(w, "Routing: %s\n", c.Line())
	}
}

func printComparison(w io.Writer, c bench.Comparison) {
	fmt.Fprintf(w, "== Bench compare %s → %s ==\n", c.A, c.B)
	verdict := "no regression"
	switch {
	case !c.Comparable:
		verdict = "not comparable"
	case c.Regressed():
		verdict = "B regresses"
	}
	fmt.Fprintf(w, "verdict: %s\n", verdict)
	fmt.Fprintf(w, "pass rate %.0f%% → %.0f%% (%+.0f points)", c.PassRateA*100, c.PassRateB*100, c.PassRateDelta*100)
	if c.JudgeDelta != 0 {
		fmt.Fprintf(w, " · judge %+.1f", c.JudgeDelta)
	}
	fmt.Fprintf(w, " · tokens %+d · cost %+.2f USD · wall %+s\n", c.TokensDelta, c.USDDelta, (time.Duration(c.WallDelta) * time.Second).String())
	if c.HoldoutA.Total > 0 || c.HoldoutB.Total > 0 {
		fmt.Fprintf(w, "holdout %d/%d → %d/%d\n", c.HoldoutA.Passed, c.HoldoutA.Total, c.HoldoutB.Passed, c.HoldoutB.Total)
	}
	for _, warn := range c.Warnings {
		fmt.Fprintf(w, "⚠ %s\n", warn)
	}
	printByWorkflow(w, c)
	printByStage(w, c)
	printStageScores(w, c)
	fmt.Fprintln(w, "\nTasks (regressions first):")
	for _, t := range c.Tasks {
		mark := " "
		for _, r := range c.Regressions {
			if r == t.Task {
				mark = "✗"
			}
		}
		for _, r := range c.Improved {
			if r == t.Task {
				mark = "✓"
			}
		}
		fmt.Fprintf(w, "  %s %s  %s → %s · tokens %+d · cost %+.2f · wall %+s\n", mark, t.Task, t.A, t.B, t.TokensDelta, t.USDDelta, (time.Duration(t.WallDelta) * time.Second).String())
	}
}

// printByStage is compare's By stage section from the tasks' traces: per
// status, A → B, the mean per task (docs/benchmark.md, "Traces").
func printByStage(w io.Writer, c bench.Comparison) {
	if len(c.Stages) == 0 {
		return
	}
	fmt.Fprintln(w, "\nBy stage (mean per task that visited it, A → B):")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  STATUS\tTASKS\tTOKENS\tWALL\tCALLS\tFIRST TRY")
	for _, s := range c.Stages {
		fmt.Fprintf(tw, "  %s\t%d → %d\t%s → %s\t%s → %s\t%.1f → %.1f\t%s → %s\n", s.Status, s.A.Tasks, s.B.Tasks,
			stageTokens(s.A), stageTokens(s.B), trace.Wall(s.A.WallSec), trace.Wall(s.B.WallSec), s.A.Calls, s.B.Calls,
			stageShare(s.A.FirstTry), stageShare(s.B.FirstTry))
	}
	tw.Flush()
}

// printStageScores is compare's scores per stage (docs/benchmark.md,
// Scores per stage): per stage, A → B, the mean score and how many passed
// of the tasks scored, and the mean spend from the traces.
func printStageScores(w io.Writer, c bench.Comparison) {
	if len(c.StageScores) == 0 {
		return
	}
	fmt.Fprintln(w, "\nBy stage, scores (tasks with stage references in both runs, A → B):")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  STAGE\tSTATUS\tTASKS\tSCORE\tPASSED\tTOKENS\tWALL")
	for _, s := range c.StageScores {
		status := s.Status
		if status == "" {
			status = "—"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%d\t%s → %s\t%s → %s\t%s → %s\t%s → %s\n", s.Stage, status, s.Tasks,
			stageScoreText(s.A), stageScoreText(s.B), stagePassedText(s.A), stagePassedText(s.B),
			stageMean(s.A.Tokens, tokensText), stageMean(s.B.Tokens, tokensText), stageMean(s.A.WallSec, secText), stageMean(s.B.WallSec, secText))
	}
	tw.Flush()
}

func stageTokens(s bench.StageSide) string {
	if s.TokensKnown == 0 {
		return trace.Unknown
	}
	return tokens.Format(int64(s.Tokens))
}

func stageShare(f float64) string {
	if f < 0 {
		return trace.Unknown
	}
	return fmt.Sprintf("%.0f%%", f*100)
}

func stageScoreText(s bench.StageScoreSide) string {
	if s.Score < 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f", s.Score)
}

func stagePassedText(s bench.StageScoreSide) string {
	if s.Scored == 0 {
		return "—"
	}
	return fmt.Sprintf("%d/%d", s.Passed, s.Scored)
}

func stageMean(v float64, format func(float64) string) string {
	if v < 0 {
		return "—"
	}
	return format(v)
}

func tokensText(v float64) string { return tokens.Format(int64(v)) }

// printByWorkflow is compare's per-workflow verdicts (docs/benchmark.md,
// Evals), shown when the runs hold more than one workflow or an eval score.
func printByWorkflow(w io.Writer, c bench.Comparison) {
	show := len(c.Workflows) > 1 || c.Branch != ""
	for _, v := range c.Workflows {
		if v.ScoreA >= 0 || v.ScoreB >= 0 {
			show = true
		}
	}
	if !show {
		return
	}
	fmt.Fprintln(w, "\nBy workflow (A → B):")
	for _, v := range c.Workflows {
		fmt.Fprintf(w, "  %s: %s · %d tasks%s\n", v.Workflow, v.Verdict, v.Tasks, markText(v))
		quality := fmt.Sprintf("pass %.0f%% → %.0f%%", v.PassRateA*100, v.PassRateB*100)
		if v.ScoreA >= 0 || v.ScoreB >= 0 {
			quality += fmt.Sprintf(" · score %s → %s", scoreText(v.ScoreA), scoreText(v.ScoreB))
		}
		fmt.Fprintf(w, "      %s\n", quality)
		fmt.Fprintf(w, "      wall %s → %s (%s) · tokens %s → %s (%s) · cost $%.2f → $%.2f (%s)\n",
			secText(v.WallA), secText(v.WallB), relText(v.WallA, v.WallB),
			tokens.Format(v.TokensA), tokens.Format(v.TokensB), relText(float64(v.TokensA), float64(v.TokensB)),
			v.USDA, v.USDB, relText(v.USDA, v.USDB))
		if len(v.Why) > 0 {
			fmt.Fprintf(w, "      %s\n", strings.Join(v.Why, "; "))
		}
	}
	if c.Branch == "" {
		if len(c.Workflows) > 1 {
			fmt.Fprintf(w, "  Mark the changed ones: issue-cli bench compare %s %s --branch <ref>\n", c.A, c.B)
		}
		return
	}
	fmt.Fprintf(w, "  Branch %s: %d not mapped to a workflow", c.Branch, len(c.NotMapped))
	if len(c.NotMapped) > 0 {
		fmt.Fprintf(w, " (%s)", bench.FilesText(c.NotMapped))
	}
	fmt.Fprintln(w)
	for _, n := range bench.NoisyControls(c.Workflows) {
		fmt.Fprintf(w, "  ⚠ %s\n", n)
	}
}

// markText is a workflow's mark after its verdict: " [changed: files]" or
// " [control]"; "" without a branch.
func markText(v bench.WorkflowVerdict) string {
	switch v.Mark {
	case bench.MarkChanged:
		return " [changed: " + bench.FilesText(v.ChangedBy) + "]"
	case bench.MarkControl:
		return " [control]"
	}
	return ""
}

// branchAffected maps the files ref changed since main in the repository
// at repo (work records dropped, as the experiment guard does) to proj's
// workflows; stamps are read from ref's files.
func branchAffected(proj *tracker.Project, repo, ref string) (bench.Affected, error) {
	top, err := experiment.TopLevel(repo)
	if err != nil {
		return bench.Affected{}, fmt.Errorf("marking the changed workflows needs a git repository: %w", err)
	}
	if _, err := experiment.SHA(top, ref); err != nil {
		return bench.Affected{}, fmt.Errorf("%s: no such ref in %s", ref, top)
	}
	changed, err := experiment.Changed(top, "main", ref)
	if err != nil {
		return bench.Affected{}, fmt.Errorf("%s: %w", ref, err)
	}
	if proj != nil {
		changed = experiment.WorkRecords(changed, trackerDirs(proj, top))
	}
	read := func(rel string) ([]byte, error) { return experiment.FileAt(top, ref, rel) }
	aff := bench.AffectedWorkflows(changed, bench.WorkflowSources(proj, top, read))
	aff.Ref = ref
	return aff, nil
}

func secText(sec float64) string {
	return (time.Duration(sec) * time.Second).Round(time.Second).String()
}

func relText(a, b float64) string {
	if a == 0 {
		if b == 0 {
			return "±0%"
		}
		return "new"
	}
	return fmt.Sprintf("%+.0f%%", (b-a)/a*100)
}

func scoreText(f float64) string {
	if f < 0 {
		return "–"
	}
	return fmt.Sprintf("%.2f", f)
}

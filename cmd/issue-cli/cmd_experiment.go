package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/bench"
	"github.com/michal-franc/issue-viewer/internal/experiment"
	"github.com/michal-franc/issue-viewer/internal/retro"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

var experimentCommand = &Command{
	Name:      "experiment",
	ShortHelp: "The experiment loop: propose a change from retros, measure it on the benchmark, the owner adopts (sub: candidates|init|show|list|run|record|rollback)",
	LongHelp: `The experiment loop (factory/factory-experiment-loop): a change to prompts,
workflow.yaml or a tool, made on an issue's branch and measured on the
benchmark against main. The verdict is advice: the owner adopts the change,
even in YOLO (YOLO passes no gate of an issue labelled experiment).

  1. candidates            what retros keep asking for (read-only)
  2. init <slug> ...       turn a proposal (any status, e.g. idea) into an experiment
  3. run <slug>            benchmark main and the branch, compare, record the verdict
  then the owner adopts it at its gates, or it is parked as rejected.

Subcommands:
  candidates [--min 3] [--all]
      Workflow-bin findings of the retros grouped by a term that comes back
      in --min or more issues, then single findings of the last 30 days.
      Each row names the open experiment that already uses its retros.
  init <slug> --hypothesis "..." --target <metric>:<up|down>:<threshold>
       [--source retros/<file>]... [--change <glob>]... [--benchmark <name>]
      Adds the experiment label when the issue lacks it (other labels kept,
      status unchanged), then writes the experiment block.
      Metrics: pass_rate, gates_first_try, rework, judge (in their units);
      tokens, cost, wall (threshold is a fraction of the baseline).
  show <slug>
      The block and its verdict.
  list
      Every experiment issue and its verdict.
  run <slug> [--ref HEAD] [--benchmark <name>] [--dry-run]
      Guard (no protected path, only --change paths, contains main), then
      the baseline run (reused when one measured main's sha) and the
      candidate run (bench run --setup <ref>), then record. --dry-run prints
      the guard and the plan and runs nothing.
  record <slug> --baseline <run> --candidate <run>
      Judge two finished benchmark runs and record the verdict: the block,
      an experiment: comment, and on promising an "Experiment result"
      section. Beside it, each workflow's verdict (bench compare's By
      workflow), marked changed or control by the files the candidate
      changed since main; advice only, the owner adopts.
  rollback <slug>
      Make branch revert/<slug> reverting the commits on main with the
      trailer "Experiment: <slug>". main is not touched; review and merge it.

The benchmark, its holdout, its budget and the protected paths are the
owner's (bench config outside every repo); nothing here writes them.`,
	Run:         runExperiment,
	Subcommands: []string{"candidates", "init", "show", "list", "run", "record", "rollback"},
}

func init() {
	registerCommand(experimentCommand)
}

func runExperiment(ctx *Context, args []string) error {
	if ctx.Project == nil {
		return fmt.Errorf("experiment needs a project — pass --project <slug> or run from a project root")
	}
	if len(args) == 0 {
		fmt.Fprintln(ctx.Stdout, "The experiment loop: candidates → init → run; the owner adopts.")
		fmt.Fprintln(ctx.Stdout, "  issue-cli experiment candidates         what retros keep asking for")
		fmt.Fprintln(ctx.Stdout, "  issue-cli experiment init <slug> ...    the hypothesis and target on an issue labelled experiment")
		fmt.Fprintln(ctx.Stdout, "  issue-cli experiment run <slug>         benchmark main and the branch, record the verdict")
		fmt.Fprintln(ctx.Stdout, "See issue-cli experiment --help.")
		return nil
	}
	rest := args[1:]
	switch args[0] {
	case "candidates":
		return runExperimentCandidates(ctx, rest)
	case "init":
		return runExperimentInit(ctx, rest)
	case "show":
		return runExperimentShow(ctx, rest)
	case "list":
		return runExperimentList(ctx, rest)
	case "run":
		return runExperimentRun(ctx, rest)
	case "record":
		return runExperimentRecord(ctx, rest)
	case "rollback":
		return runExperimentRollback(ctx, rest)
	}
	return &usageError{msg: fmt.Sprintf("unknown experiment subcommand %q (candidates, init, show, list, run, record, rollback)", args[0])}
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// experimentIssues are the project's issues labelled experiment.
func experimentIssues(ctx *Context) ([]*tracker.Issue, error) {
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return nil, err
	}
	var out []*tracker.Issue
	for _, is := range issues {
		if experiment.Is(is.Labels) {
			out = append(out, is)
		}
	}
	return out, nil
}

func runExperimentCandidates(ctx *Context, args []string) error {
	fs := newFlagSet("experiment candidates", ctx)
	minFlag := fs.Int("min", 3, "issues a term must come back in to make a pattern")
	allFlag := fs.Bool("all", false, "list every single finding, not the first 10")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	entries, err := retro.Load(projectRoot(ctx.Project) + "/retros")
	if err != nil {
		return err
	}
	exps, err := experimentIssues(ctx)
	if err != nil {
		return err
	}
	var open []experiment.Open
	for _, is := range exps {
		if is.Status == "done" || is.Status == "parked" {
			continue
		}
		if b, _ := experiment.Load(is.FilePath); b != nil {
			open = append(open, experiment.Open{Slug: is.Slug, Source: b.Source})
		}
	}
	stop := map[string]bool{"issue-cli": true, strings.ToLower(ctx.Project.Slug): true}
	if all, err := tracker.LoadIssues(ctx.Project.IssueDir); err == nil {
		for _, is := range all {
			if is.System != "" {
				stop[strings.ToLower(strings.ReplaceAll(is.System, " ", "-"))] = true
			}
		}
	}
	patterns, singles := experiment.Candidates(entries, open, stop, *minFlag, ctx.now())
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"patterns": nonNil(patterns), "singles": nonNil(singles)})
	}
	w := ctx.Stdout
	if len(patterns)+len(singles) == 0 {
		fmt.Fprintf(w, "No Workflow findings in %d retros to propose from.\n", len(entries))
		return nil
	}
	fmt.Fprintf(w, "== Experiment candidates (%d retros) ==\n", len(entries))
	if len(patterns) > 0 {
		fmt.Fprintf(w, "\nPatterns (a term in the Workflow findings of %d+ issues):\n", *minFlag)
		for _, c := range patterns {
			fmt.Fprintf(w, "  %s — %d issues: %s\n", c.Term, len(c.Issues), strings.Join(c.Issues, ", "))
			printCandidateTail(w, c)
		}
	}
	if len(singles) > 0 {
		fmt.Fprintln(w, "\nSingle findings (last 30 days):")
		for i, c := range singles {
			if i == 10 && !*allFlag {
				fmt.Fprintf(w, "  … %d more (--all)\n", len(singles)-10)
				break
			}
			fmt.Fprintf(w, "  %s\n", c.Issues[0])
			printCandidateTail(w, c)
		}
	}
	fmt.Fprintln(w, "\nNext: propose one on the issue trail; the lead or the owner files the issue (at idea is fine), then")
	fmt.Fprintln(w, "  issue-cli experiment init <slug> --hypothesis \"...\" --target <metric>:<up|down>:<threshold> --source <retro>")
	return nil
}

func printCandidateTail(w io.Writer, c experiment.Candidate) {
	fmt.Fprintf(w, "      e.g. %s\n", truncateRunes(c.Sample, 160))
	fmt.Fprintf(w, "      retros: %s\n", strings.Join(c.Retros, " "))
	if c.Experiment != "" {
		fmt.Fprintf(w, "      already an experiment: %s\n", c.Experiment)
	}
}

func nonNil(c []experiment.Candidate) []experiment.Candidate {
	if c == nil {
		return []experiment.Candidate{}
	}
	return c
}

// experimentIssue finds slug and refuses an issue without the label.
func experimentIssue(ctx *Context, slug string) (*tracker.Issue, error) {
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return nil, err
	}
	if !experiment.Is(issue.Labels) {
		return nil, fmt.Errorf("%s is not labelled %s: an experiment is an issue with that label; issue-cli experiment init %s --hypothesis \"...\" --target <metric>:<up|down>:<threshold> adds it",
			issue.Slug, experiment.Label, issue.Slug)
	}
	return issue, nil
}

// labelExperiment adds the experiment label to a proposal (any status,
// other labels kept); false when it already had it.
func labelExperiment(issue *tracker.Issue) (bool, error) {
	if experiment.Is(issue.Labels) {
		return false, nil
	}
	labels := append(slices.Clone(issue.Labels), experiment.Label)
	if err := tracker.UpdateIssueFrontmatter(issue.FilePath, tracker.IssueUpdate{Labels: labels}); err != nil {
		return false, err
	}
	issue.Labels = labels
	return true, nil
}

func runExperimentInit(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "experiment init")
	if err != nil {
		return err
	}
	fs := newFlagSet("experiment init", ctx)
	hyp := fs.String("hypothesis", "", "what the change should improve, and why (required)")
	target := fs.String("target", "", "<metric>:<up|down>:<threshold> (required)")
	bench := fs.String("benchmark", "", "the benchmark to measure on (default: the only one in the owner's bench dir)")
	var source, change multiFlag
	fs.Var(&source, "source", "retros/<file> the finding came from (repeatable)")
	fs.Var(&change, "change", "a glob the branch may change (repeatable; default: anything not protected)")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	if strings.TrimSpace(*hyp) == "" || strings.TrimSpace(*target) == "" {
		return &usageError{msg: fmt.Sprintf("experiment init needs --hypothesis and --target\n\nExample:\n  issue-cli experiment init %s --hypothesis \"naming check-script in the guidance cuts refused gates\" --target gates_first_try:up:0.05 --source retros/<file>.md --change workflow.yaml", slug)}
	}
	t, err := experiment.ParseTarget(*target)
	if err != nil {
		return err
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	b, err := experiment.Load(issue.FilePath)
	if err != nil {
		return err
	}
	if b == nil {
		b = &experiment.Block{}
	}
	if b.Verdict != "" && (b.Target != t) {
		return fmt.Errorf("%s already has a verdict on %s: a new target is a new experiment", issue.Slug, b.Target)
	}
	b.Hypothesis, b.Target = normalizeEscapedText(*hyp), t
	if len(source) > 0 {
		b.Source = normalizeSources(source)
	}
	if len(change) > 0 {
		b.Change = change
	}
	if *bench != "" {
		b.Benchmark = *bench
	}
	labelled, err := labelExperiment(issue)
	if err != nil {
		return err
	}
	if err := experiment.Save(issue.FilePath, b); err != nil {
		return err
	}
	if labelled {
		fmt.Fprintf(ctx.Stdout, "✓ Labelled %s %s\n", issue.Slug, experiment.Label)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Experiment block written on %s: %s\n", issue.Slug, t)
	fmt.Fprintf(ctx.Stdout, "Next: make the change on the branch, then issue-cli experiment run %s --dry-run\n", issue.Slug)
	return nil
}

func normalizeSources(src []string) []string {
	var out []string
	for _, s := range src {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.HasPrefix(s, "retros/") {
			s = "retros/" + s
		}
		out = append(out, s)
	}
	return out
}

func runExperimentShow(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "experiment show")
	if err != nil {
		return err
	}
	fs := newFlagSet("experiment show", ctx)
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	issue, err := experimentIssue(ctx, slug)
	if err != nil {
		return err
	}
	b, err := experiment.Load(issue.FilePath)
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"slug": issue.Slug, "experiment": b})
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "== Experiment: %s ==\n", issue.Slug)
	if b == nil {
		fmt.Fprintf(w, "No experiment block yet: issue-cli experiment init %s --hypothesis ... --target ...\n", issue.Slug)
		return nil
	}
	fmt.Fprintf(w, "Hypothesis: %s\n", b.Hypothesis)
	fmt.Fprintf(w, "Target:     %s\n", b.Target)
	if b.Benchmark != "" {
		fmt.Fprintf(w, "Benchmark:  %s\n", b.Benchmark)
	}
	if len(b.Change) > 0 {
		fmt.Fprintf(w, "Change:     %s\n", strings.Join(b.Change, ", "))
	}
	if len(b.Source) > 0 {
		fmt.Fprintf(w, "Source:     %s\n", strings.Join(b.Source, " "))
	}
	if b.Baseline != nil {
		fmt.Fprintf(w, "Baseline:   %s %s run %s\n", b.Baseline.Ref, shortSHA(b.Baseline.SHA), orDash(b.Baseline.Run))
	}
	if b.Candidate != nil {
		fmt.Fprintf(w, "Candidate:  %s %s run %s\n", b.Candidate.Ref, shortSHA(b.Candidate.SHA), orDash(b.Candidate.Run))
	}
	fmt.Fprintf(w, "Verdict:    %s\n", b.VerdictLine())
	for _, r := range b.Reasons {
		fmt.Fprintf(w, "  - %s\n", r)
	}
	printExperimentWorkflows(w, b)
	if b.Adopted != nil && b.Adopted.RolledBack != "" {
		fmt.Fprintf(w, "Rollback:   branch %s prepared\n", b.Adopted.RolledBack)
	}
	return nil
}

func shortSHA(s string) string {
	if len(s) > 9 {
		return s[:9]
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func runExperimentList(ctx *Context, args []string) error {
	fs := newFlagSet("experiment list", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	exps, err := experimentIssues(ctx)
	if err != nil {
		return err
	}
	type row struct {
		Slug    string `json:"slug"`
		Status  string `json:"status"`
		Verdict string `json:"verdict"`
		Line    string `json:"line"`
	}
	var rows []row
	for _, is := range exps {
		b, _ := experiment.Load(is.FilePath)
		r := row{Slug: is.Slug, Status: is.Status, Line: b.VerdictLine()}
		if b != nil {
			r.Verdict = b.Verdict
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Slug < rows[j].Slug })
	if ctx.JSONOutput {
		if rows == nil {
			rows = []row{}
		}
		return writeJSON(ctx.Stdout, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintf(ctx.Stdout, "No experiments yet (issues labelled %s). Start from: issue-cli experiment candidates\n", experiment.Label)
		return nil
	}
	for _, r := range rows {
		fmt.Fprintf(ctx.Stdout, "%-48s %-14s %s\n", r.Slug, r.Status, r.Line)
	}
	return nil
}

// experimentBenchmark picks the benchmark: the flag, the block's, or the
// only one in the owner's bench dir.
func experimentBenchmark(flagVal string, b *experiment.Block, benchDir string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if b.Benchmark != "" {
		return b.Benchmark, nil
	}
	entries, _ := os.ReadDir(benchDir)
	var names []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && !e.IsDir() {
			names = append(names, n)
		}
	}
	if len(names) == 1 {
		return names[0], nil
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no benchmark in %s yet: the owner sets one up (issue-cli bench)", benchDir)
	}
	return "", fmt.Errorf("several benchmarks (%s): pass --benchmark", strings.Join(names, ", "))
}

// experimentPlan is what run does, worked out before anything runs.
type experimentPlan struct {
	Benchmark string
	MainSHA   string
	Ref       string
	RefSHA    string
	Problems  []string
	Baseline  *experiment.Run // reused, nil when it must run
}

func planExperiment(ctx *Context, issue *tracker.Issue, b *experiment.Block, ref, benchFlag string) (*experimentPlan, error) {
	benchDir := experiment.BenchDir()
	name, err := experimentBenchmark(benchFlag, b, benchDir)
	if err != nil {
		return nil, err
	}
	p := &experimentPlan{Benchmark: name, Ref: ref}
	repo := "."
	if p.MainSHA, err = experiment.SHA(repo, "main"); err != nil {
		return nil, err
	}
	if p.RefSHA, err = experiment.SHA(repo, ref); err != nil {
		return nil, err
	}
	if p.RefSHA == p.MainSHA {
		p.Problems = append(p.Problems, ref+" is main itself: make the change on the issue's branch first")
	}
	if !experiment.Contains(repo, ref, p.MainSHA) {
		p.Problems = append(p.Problems, fmt.Sprintf("%s does not contain main (%s): merge main, then run again, so both runs differ only by the change", ref, shortSHA(p.MainSHA)))
	}
	protected, err := experiment.Protected(benchDir, name)
	if err != nil {
		return nil, err
	}
	changed, err := experiment.Changed(repo, "main", ref)
	if err != nil {
		return nil, err
	}
	changed = experiment.WorkRecords(changed, trackerDirs(ctx.Project, repo))
	for _, pr := range experiment.Guard(changed, protected, b.Change) {
		p.Problems = append(p.Problems, pr.String())
	}
	p.Baseline = experiment.Baseline(benchDir, name, p.MainSHA)
	return p, nil
}

// trackerDirs are the project's issue folder and retros/, relative to the
// repository at repo, when they are inside it.
func trackerDirs(proj *tracker.Project, repo string) []string {
	top, err := experiment.TopLevel(repo)
	if err != nil {
		return nil
	}
	if r, err := filepath.EvalSymlinks(top); err == nil {
		top = r
	}
	var out []string
	for _, d := range []string{proj.IssueDir, filepath.Join(projectRoot(proj), "retros")} {
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		if r, err := filepath.EvalSymlinks(abs); err == nil {
			abs = r
		}
		if rel, err := filepath.Rel(top, abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	return out
}

func runExperimentRun(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "experiment run")
	if err != nil {
		return err
	}
	fs := newFlagSet("experiment run", ctx)
	ref := fs.String("ref", "HEAD", "the experiment's branch or commit")
	benchFlag := fs.String("benchmark", "", "the benchmark (default: the block's)")
	dry := fs.Bool("dry-run", false, "print the guard and the plan, run nothing")
	quote := fs.String("quote", "", "the owner's words starting the benchmark runs, passed to bench run")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	issue, err := experimentIssue(ctx, slug)
	if err != nil {
		return err
	}
	b, err := experiment.Load(issue.FilePath)
	if err != nil {
		return err
	}
	if b == nil {
		return fmt.Errorf("%s has no experiment block: issue-cli experiment init %s --hypothesis ... --target ...", issue.Slug, issue.Slug)
	}
	p, err := planExperiment(ctx, issue, b, *ref, *benchFlag)
	if err != nil {
		return err
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "== Experiment run: %s ==\n", issue.Slug)
	fmt.Fprintf(w, "Target: %s · benchmark %s\n", b.Target, p.Benchmark)
	if len(p.Problems) > 0 {
		fmt.Fprintln(w, "Guard: refused")
		for _, pr := range p.Problems {
			fmt.Fprintf(w, "  ✗ %s\n", pr)
		}
		return fmt.Errorf("experiment run refused for %s (%d problem(s) above)", issue.Slug, len(p.Problems))
	}
	fmt.Fprintln(w, "Guard: ok (no protected path, contains main)")
	label := "experiment:" + issue.Slug + "@" + shortSHA(p.RefSHA)
	if p.Baseline != nil {
		fmt.Fprintf(w, "Baseline: reuse run %s (main %s)\n", p.Baseline.ID, shortSHA(p.MainSHA))
	} else {
		fmt.Fprintf(w, "Baseline: bench run %s --setup %s (main)\n", p.Benchmark, shortSHA(p.MainSHA))
	}
	fmt.Fprintf(w, "Candidate: bench run %s --setup %s --label %s\n", p.Benchmark, shortSHA(p.RefSHA), label)
	if *dry {
		fmt.Fprintln(w, "(dry run — nothing was run)")
		return nil
	}
	bench := lookupCommand("bench")
	if bench == nil {
		return fmt.Errorf("this build has no bench command yet (factory/factory-benchmark-and-evals): run the two benchmark runs when it lands, then issue-cli experiment record %s --baseline <run> --candidate <run>", issue.Slug)
	}
	benchDir := experiment.BenchDir()
	runBench := func(sha, label string) (*experiment.Run, error) {
		a := []string{"run", p.Benchmark, "--setup", sha, "--label", label}
		if *quote != "" {
			a = append(a, "--quote", *quote)
		}
		if err := bench.Run(ctx, a); err != nil {
			return nil, err
		}
		r := experiment.Labelled(benchDir, p.Benchmark, label)
		if r == nil {
			return nil, fmt.Errorf("bench run finished but no run labelled %s is in %s", label, experiment.RunsDir(benchDir, p.Benchmark))
		}
		return r, nil
	}
	base := p.Baseline
	if base == nil {
		if base, err = runBench(p.MainSHA, "experiment-baseline@"+shortSHA(p.MainSHA)); err != nil {
			return err
		}
	}
	cand, err := runBench(p.RefSHA, label)
	if err != nil {
		return err
	}
	b.Benchmark = p.Benchmark
	b.Baseline = &experiment.RunRef{Ref: "main", SHA: p.MainSHA, Run: base.ID}
	b.Candidate = &experiment.RunRef{Ref: *ref, SHA: p.RefSHA, Run: cand.ID}
	return recordExperiment(ctx, issue, b, base, cand)
}

func runExperimentRecord(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "experiment record")
	if err != nil {
		return err
	}
	fs := newFlagSet("experiment record", ctx)
	baseFlag := fs.String("baseline", "", "the baseline run id (main)")
	candFlag := fs.String("candidate", "", "the candidate run id (the branch)")
	benchFlag := fs.String("benchmark", "", "the benchmark (default: the block's)")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	if *baseFlag == "" || *candFlag == "" {
		return &usageError{msg: "experiment record needs --baseline <run> and --candidate <run>"}
	}
	issue, err := experimentIssue(ctx, slug)
	if err != nil {
		return err
	}
	b, err := experiment.Load(issue.FilePath)
	if err != nil {
		return err
	}
	if b == nil {
		return fmt.Errorf("%s has no experiment block: issue-cli experiment init %s first", issue.Slug, issue.Slug)
	}
	benchDir := experiment.BenchDir()
	name, err := experimentBenchmark(*benchFlag, b, benchDir)
	if err != nil {
		return err
	}
	base, err := experiment.LoadRun(benchDir, name, *baseFlag)
	if err != nil {
		return fmt.Errorf("baseline run: %w", err)
	}
	cand, err := experiment.LoadRun(benchDir, name, *candFlag)
	if err != nil {
		return fmt.Errorf("candidate run: %w", err)
	}
	b.Benchmark = name
	b.Baseline = &experiment.RunRef{Ref: orValue(base.Setup.Ref, "main"), SHA: base.Setup.SHA, Run: base.ID}
	b.Candidate = &experiment.RunRef{Ref: cand.Setup.Ref, SHA: cand.Setup.SHA, Run: cand.ID}
	return recordExperiment(ctx, issue, b, base, cand)
}

func orValue(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// recordExperiment judges the two runs and writes the verdict: the block,
// the experiment: comment and, when promising, the Experiment result
// section the owner reads at the gate.
func recordExperiment(ctx *Context, issue *tracker.Issue, b *experiment.Block, base, cand *experiment.Run) error {
	verdict, reasons, res := experiment.Judge(base, cand, b.Target)
	b.Verdict, b.Reasons, b.Result = verdict, reasons, res
	b.Workflows, b.WorkflowsNote = experimentWorkflows(ctx.Project, b)
	if err := experiment.Save(issue.FilePath, b); err != nil {
		return err
	}
	comment := fmt.Sprintf("experiment: %s; tokens %+d, cost %+.2f USD, wall %+.1fm; runs %s (baseline) %s (candidate)",
		b.VerdictLine(), res.TokensDelta, res.USDDelta, res.WallDelta/60, base.ID, cand.ID)
	if line := b.WorkflowsLine(); line != "" {
		comment += "; workflows: " + line
	}
	if err := tracker.AddCommentBy(issue.FilePath, 0, comment, "cli", commentAuthor(ctx.Project, issue)); err != nil {
		return fmt.Errorf("verdict saved, comment failed: %w", err)
	}
	if verdict == experiment.Promising {
		section := experimentResultSection(b, base, cand)
		if _, _, err := tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
			return tracker.AppendIssueBody(body, section)
		}); err != nil {
			return fmt.Errorf("verdict saved, result section failed: %w", err)
		}
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "Verdict: %s\n", b.VerdictLine())
	for _, r := range reasons {
		fmt.Fprintf(w, "  - %s\n", r)
	}
	printExperimentWorkflows(w, b)
	switch verdict {
	case experiment.Promising:
		fmt.Fprintln(w, "Next: the owner adopts it at the issue's gates (YOLO does not pass them); the numbers are under ## Experiment result.")
	case experiment.Rejected:
		fmt.Fprintf(w, "Next: park it with the reason: issue-cli transition %s --to parked --field parked_reason=\"experiment rejected: %s\"\n", issue.Slug, reasons[0])
	default:
		fmt.Fprintln(w, "Next: the runs can't be compared; fix the cause above and run again (a fresh baseline if the model or Claude Code changed).")
	}
	return nil
}

func experimentResultSection(b *experiment.Block, base, cand *experiment.Run) string {
	r := b.Result
	var sb strings.Builder
	sb.WriteString("## Experiment result\n\n")
	fmt.Fprintf(&sb, "Verdict: **%s** on benchmark %s. The owner adopts it; YOLO does not.\n\n", b.Verdict, b.Benchmark)
	sb.WriteString("| | baseline (main) | candidate |\n|---|---|---|\n")
	fmt.Fprintf(&sb, "| run | %s | %s |\n", base.ID, cand.ID)
	fmt.Fprintf(&sb, "| %s (target: %s) | %v | %v |\n", b.Target.Metric, b.Target, r.MetricA, r.MetricB)
	fmt.Fprintf(&sb, "| pass rate | %.2f | %.2f |\n", r.PassA, r.PassB)
	if r.HoldoutB != "" {
		fmt.Fprintf(&sb, "| holdout | %s | %s |\n", r.HoldoutA, r.HoldoutB)
	}
	fmt.Fprintf(&sb, "| tokens | %d | %d |\n", base.SpentTokens, cand.SpentTokens)
	fmt.Fprintf(&sb, "| cost (USD) | %.2f | %.2f |\n", base.SpentUSD, cand.SpentUSD)
	fmt.Fprintf(&sb, "\nRegressions: %d · tasks improved: %d.\n", r.Regressions, r.Improved)
	if len(b.Workflows) > 0 {
		sb.WriteString("\n### By workflow\n\n")
		sb.WriteString("Advice beside the verdict above: changed workflows are the ones the branch's files reach, controls the rest.\n\n")
		sb.WriteString("| workflow | mark | verdict | pass rate | score | tokens | cost (USD) | wall |\n|---|---|---|---|---|---|---|---|\n")
		for _, v := range b.Workflows {
			mark := orDash(v.Mark)
			if len(v.ChangedBy) > 0 {
				mark += " (" + bench.FilesText(v.ChangedBy) + ")"
			}
			fmt.Fprintf(&sb, "| %s | %s | %s | %.2f → %.2f | %s → %s | %d → %d | %.2f → %.2f | %s → %s |\n",
				v.Workflow, mark, v.Verdict, v.PassRateA, v.PassRateB, scoreText(v.ScoreA), scoreText(v.ScoreB),
				v.TokensA, v.TokensB, v.USDA, v.USDB, secText(v.WallA), secText(v.WallB))
		}
		for _, n := range noisyResults(b.Workflows) {
			fmt.Fprintf(&sb, "\n⚠ %s.\n", n)
		}
	}
	if b.WorkflowsNote != "" {
		fmt.Fprintf(&sb, "\nBy workflow: %s.\n", b.WorkflowsNote)
	}
	sb.WriteString("\nRolling it back later: `issue-cli experiment rollback <slug>` (the commits carry `Experiment: <slug>`).")
	return sb.String()
}

func runExperimentRollback(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "experiment rollback")
	if err != nil {
		return err
	}
	fs := newFlagSet("experiment rollback", ctx)
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	issue, err := experimentIssue(ctx, slug)
	if err != nil {
		return err
	}
	shas, err := experiment.AdoptedCommits(".", "main", issue.Slug)
	if err != nil {
		return err
	}
	if len(shas) == 0 {
		return fmt.Errorf("no commit on main carries the trailer %q: nothing of %s was adopted", experiment.Trailer+": "+issue.Slug, issue.Slug)
	}
	branch, err := experiment.PrepareRevert(".", "main", issue.Slug, shas)
	if err != nil {
		return err
	}
	b, err := experiment.Load(issue.FilePath)
	if err != nil {
		return err
	}
	if b == nil {
		b = &experiment.Block{}
	}
	if b.Adopted == nil {
		b.Adopted = &experiment.Adopted{Commit: shas[0]}
	}
	b.Adopted.RolledBack = branch
	if err := experiment.Save(issue.FilePath, b); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Branch %s reverts %d commit(s) of %s; main is unchanged.\n", branch, len(shas), issue.Slug)
	fmt.Fprintf(ctx.Stdout, "Next: review it (git log main..%s), then the owner merges it. The experiment's numbers stay in its sidecar.\n", branch)
	return nil
}

// experimentHeaderLine is the "Experiment:" line show and start print for an
// issue labelled experiment: its target and verdict, and that the owner
// adopts it even in YOLO. "" for other issues.
func experimentHeaderLine(proj *tracker.Project, issue *tracker.Issue) string {
	if !experiment.Is(issue.Labels) {
		return ""
	}
	b, _ := experiment.Load(issue.FilePath)
	if b == nil {
		return tracker.InjectProject(proj, fmt.Sprintf("Experiment: no block yet (issue-cli experiment init %s --hypothesis ... --target ...); the owner adopts experiments, even in YOLO", issue.Slug))
	}
	next := ""
	if b.Verdict == "" {
		next = tracker.InjectProject(proj, fmt.Sprintf("; measure it: issue-cli experiment run %s --dry-run", issue.Slug))
	}
	return fmt.Sprintf("Experiment: %s · %s%s; the owner adopts experiments, even in YOLO", b.Target, b.VerdictLine(), next)
}

// experimentWorkflows are the per-workflow verdicts of b's two runs, read
// as internal/bench runs and marked by the files the candidate's sha
// changed since main; the note says why they are missing or unmarked.
func experimentWorkflows(proj *tracker.Project, b *experiment.Block) ([]experiment.WorkflowResult, string) {
	if b.Baseline == nil || b.Candidate == nil || b.Baseline.Run == "" || b.Candidate.Run == "" {
		return nil, "no runs to compare"
	}
	ra, err := bench.LoadRun(b.Benchmark, b.Baseline.Run)
	if err != nil {
		return nil, "baseline run not readable: " + err.Error()
	}
	rb, err := bench.LoadRun(b.Benchmark, b.Candidate.Run)
	if err != nil {
		return nil, "candidate run not readable: " + err.Error()
	}
	vs := bench.CompareByWorkflow(ra, rb)
	if len(vs) == 0 {
		return nil, "the runs have no tasks"
	}
	note := ""
	ref := b.Candidate.SHA
	if ref == "" {
		ref = b.Candidate.Ref // bench run --setup <ref>: the commit it built
	}
	if ref == "" {
		note = "not marked: the candidate run names no commit (bench run --setup <ref>)"
	} else if aff, err := branchAffected(proj, ".", ref); err != nil {
		note = "not marked: " + err.Error()
	} else {
		bench.MarkWorkflows(vs, aff)
	}
	data, err := json.Marshal(vs)
	if err != nil {
		return nil, err.Error()
	}
	var out []experiment.WorkflowResult
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err.Error()
	}
	return out, note
}

// printExperimentWorkflows is the By workflow block of record, run and
// show.
func printExperimentWorkflows(w io.Writer, b *experiment.Block) {
	if len(b.Workflows) == 0 {
		if b.WorkflowsNote != "" {
			fmt.Fprintf(w, "By workflow: %s\n", b.WorkflowsNote)
		}
		return
	}
	fmt.Fprintln(w, "By workflow (advice beside the verdict; the owner adopts):")
	for _, v := range b.Workflows {
		mark := ""
		switch {
		case len(v.ChangedBy) > 0:
			mark = " [changed: " + bench.FilesText(v.ChangedBy) + "]"
		case v.Mark != "":
			mark = " [" + v.Mark + "]"
		}
		fmt.Fprintf(w, "  %s: %s · %d tasks%s\n", v.Workflow, v.Verdict, v.Tasks, mark)
		fmt.Fprintf(w, "      pass %.0f%% → %.0f%% · score %s → %s · tokens %s → %s · cost $%.2f → $%.2f · wall %s → %s\n",
			v.PassRateA*100, v.PassRateB*100, scoreText(v.ScoreA), scoreText(v.ScoreB),
			compactTokens(v.TokensA), compactTokens(v.TokensB), v.USDA, v.USDB, secText(v.WallA), secText(v.WallB))
		if len(v.Why) > 0 {
			fmt.Fprintf(w, "      %s\n", strings.Join(v.Why, "; "))
		}
	}
	if b.WorkflowsNote != "" {
		fmt.Fprintf(w, "  (%s)\n", b.WorkflowsNote)
	}
	for _, n := range noisyResults(b.Workflows) {
		fmt.Fprintf(w, "  ⚠ %s\n", n)
	}
}

// noisyResults is bench.NoisyControls over the stored results.
func noisyResults(rs []experiment.WorkflowResult) []string {
	var vs []bench.WorkflowVerdict
	for _, r := range rs {
		vs = append(vs, bench.WorkflowVerdict{Workflow: r.Workflow, Mark: r.Mark, Verdict: r.Verdict})
	}
	return bench.NoisyControls(vs)
}

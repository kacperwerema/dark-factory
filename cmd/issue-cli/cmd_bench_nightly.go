package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/experiment"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The nightly loop (factory/nightly-self-improvement-loop-…): bench
// nightly runs the benchmark on main's head, then at most one experiment
// the owner queued, judged against that baseline and never adopted, and
// writes the night's numbers. It starts from the owner's shell, an agent
// quoting the owner, or the nightly-improve job's run on the owner's
// standing start.

// NightlyBrief is the bundled brief the nightly job names.
const NightlyBrief = jobs.BuiltinPrefix + "nightly-improve"

// NightlyMemBelow is the memory line: no run starts, and no task, at or
// over it (the issue's Must not; auto-start's default).
const NightlyMemBelow = autostart.DefaultBelow

// nightlyBenchRun runs one bench run; tests swap it for a stand-in that
// writes run.json, so no worker or model ever starts in a test.
var nightlyBenchRun = func(ctx *Context, args []string) error {
	return runBenchRunWith(ctx, args, NightlyMemBelow)
}

func runBenchNightly(ctx *Context, args []string) error {
	fs := newFlagSet("bench nightly", ctx)
	runFlag := fs.String("run", "", "the nightly-improve job's run issue: its standing start's words start the runs")
	quoteFlag := fs.String("quote", "", "the owner's words asking for tonight's runs (an agent session without --run)")
	dry := fs.Bool("dry-run", false, "print who, the benchmark, the machine and the plan; run nothing")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return &usageError{msg: "usage: issue-cli bench nightly [<benchmark>] [--run <job run slug>] [--quote \"<owner's words>\"] [--dry-run]"}
	}
	proj := ctx.Project
	if proj == nil {
		return fmt.Errorf("bench nightly needs a project: pass --project <slug> or run from a project root")
	}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return err
	}
	words, by, runIssue, err := nightlyWords(ctx, issues, *runFlag, strings.TrimSpace(normalizeEscapedText(*quoteFlag)))
	if err != nil {
		return err
	}
	name, err := nightlyBenchmark(proj, fs.Arg(0))
	if err != nil {
		return err
	}
	now := ctx.now()
	date := now.Format("2006-01-02")
	nights := bench.LoadNights(name)
	if n, ok := bench.NightOn(nights, date); ok {
		return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ Not run: tonight (%s) already ran on %s: run %s. One night per benchmark per date.\n\n  issue-cli bench report %s/%s", date, name, n.Run, name, n.Run)}
	}
	// main's head in the checkout this runs in, as experiment run reads it
	// (a worker's worktree shares the repository).
	mainSHA, err := experiment.SHA(".", "main")
	if err != nil {
		return fmt.Errorf("main's head: %w (run it from the project's repository)", err)
	}
	exp, expBlock := nextQueued(issues)
	held := nightlyMachine()

	w := ctx.Stdout
	fmt.Fprintf(w, "== Nightly: %s on main %s (%s) ==\n", name, shortSHA(mainSHA), date)
	fmt.Fprintf(w, "Started by: %s · owner's words: %q\n", by, words)
	if held != "" {
		fmt.Fprintf(w, "Machine: held — %s\n", held)
	} else {
		fmt.Fprintf(w, "Machine: ok (memory under %d%%, load at most the core count)\n", NightlyMemBelow)
	}
	if exp != nil {
		fmt.Fprintf(w, "Experiment: %s (queued %s by %s, ref %s)\n", exp.Slug, expBlock.Queue.At.Format("2006-01-02"), expBlock.Queue.By, expBlock.Queue.Ref)
	} else {
		fmt.Fprintln(w, "Experiment: none queued (the owner queues one: issue-cli experiment queue <slug> --quote \"…\")")
	}
	if *dry {
		fmt.Fprintf(w, "Baseline: bench run %s --setup %s --label %s\n", name, shortSHA(mainSHA), nightlyLabel(date, mainSHA))
		fmt.Fprintln(w, "(dry run — nothing was run)")
		return nil
	}

	night := bench.Night{Date: date, At: now, Benchmark: name, Project: proj.Slug, MainSHA: mainSHA}
	if runIssue != nil {
		night.JobRun = runIssue.Slug
	}
	if held != "" {
		night.Held = held
		return finishNight(ctx, night, nights)
	}
	label := nightlyLabel(date, mainSHA)
	run, err := nightlyRun(ctx, name, mainSHA, label, words)
	if err != nil {
		return err
	}
	bench.FillNight(&night, run)
	if exp != nil {
		night.Experiment = nightlyExperiment(ctx, name, date, run, exp, expBlock, words)
	}
	return finishNight(ctx, night, nights)
}

// nightlyWords are the owner's words the night's runs start on, and who
// starts them. --run: a nightly-improve job's run issue with the job's
// standing start on (a worker only for its own run); else the owner's
// shell, or --quote in an agent session.
func nightlyWords(ctx *Context, issues []*tracker.Issue, runSlug, quote string) (words, by string, run *tracker.Issue, err error) {
	who := agentSession(ctx)
	by = valueOr(who, "owner")
	if runSlug == "" {
		if who != "" && quote == "" {
			return "", "", nil, &usageError{msg: fmt.Sprintf("bench nightly starts workers that spend tokens: in an agent session (%s) it needs --run <the nightly-improve job's run> or --quote with the owner's words", who)}
		}
		return valueOr(quote, "owner's shell"), by, nil, nil
	}
	proj := ctx.Project
	for _, is := range issues {
		if strings.EqualFold(is.Slug, runSlug) {
			run = is
		}
	}
	if run == nil {
		return "", "", nil, fmt.Errorf("no issue %s in %s", runSlug, proj.Slug)
	}
	if me := whoAmI(proj, issues); me.Role == tracker.RoleWorker && me.Slug != "" && !strings.EqualFold(me.Slug, run.Slug) {
		return "", "", nil, fmt.Errorf("%s is not your run: a worker starts only its own issue's night (yours: %s)", run.Slug, me.Slug)
	}
	key := run.Field("job")
	if key == "" {
		return "", "", nil, fmt.Errorf("%s is not a recurring job's run (no job: field)", run.Slug)
	}
	r, err := jobs.Find(proj, key)
	if err != nil {
		return "", "", nil, err
	}
	if strings.TrimSpace(r.Job.Brief) != NightlyBrief {
		return "", "", nil, fmt.Errorf("%s is a run of %s, whose brief is %s, not %s", run.Slug, r.Key, valueOr(r.Job.Brief, "none"), NightlyBrief)
	}
	st, err := jobs.LoadState(yoloRoot(proj), proj.Slug)
	if err != nil {
		return "", "", nil, err
	}
	a, ok := st.For(r.Key)
	if !ok {
		return "", "", nil, &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ Not run: %s has no standing start, so its night waits for the owner. Nothing was run.\n\nThe owner may turn one on:\n  %s",
			r.Key, tracker.InjectProject(proj, fmt.Sprintf("issue-cli job auto on %s --quote \"<owner's words>\"", r.Key)))}
	}
	return a.Words, by, run, nil
}

// nightlyBenchmark is the benchmark to run: the named one, else the only
// one with a task in the project, else the one marked nightly: true.
func nightlyBenchmark(proj *tracker.Project, name string) (string, error) {
	if name != "" {
		if _, err := loadBenchmark(name); err != nil {
			return "", err
		}
		return name, nil
	}
	var mine, marked []string
	for _, n := range bench.List() {
		b, err := bench.Load(n)
		if err != nil {
			continue
		}
		for _, t := range b.Tasks {
			if t.Project == proj.Slug {
				mine = append(mine, n)
				if b.Nightly {
					marked = append(marked, n)
				}
				break
			}
		}
	}
	switch {
	case len(mine) == 1:
		return mine[0], nil
	case len(marked) == 1:
		return marked[0], nil
	case len(mine) == 0:
		return "", fmt.Errorf("no benchmark has a task in %s: the owner adds one (issue-cli bench candidates, then bench task add <benchmark> <slug>)", proj.Slug)
	}
	return "", fmt.Errorf("several benchmarks have tasks in %s (%s): name one, or the owner marks one with nightly: true in its file", proj.Slug, strings.Join(mine, ", "))
}

// nightlyMachine is why the night is held now, "" when it may run: memory
// under the line (mem-guard's reading, which must be there), the 1-minute
// load at most the core count.
func nightlyMachine() string {
	m := autostart.ReadMemory()
	switch {
	case !m.Found:
		return "mem-guard is not on PATH"
	case !m.CanStart:
		return "mem-guard check refuses: " + m.Note
	case m.Used >= NightlyMemBelow:
		return fmt.Sprintf("memory %d%% ≥ %d%%", m.Used, NightlyMemBelow)
	}
	if l := autostart.ReadLoad(); l.Found && l.Cores > 0 && l.Avg1 > float64(l.Cores) {
		return fmt.Sprintf("load %.1f over %d cores", l.Avg1, l.Cores)
	}
	return ""
}

func nightlyLabel(date, sha string) string { return "nightly:" + date + "@" + shortSHA(sha) }

// nightlyRun runs the benchmark on sha with label through bench run's own
// code and returns the run it wrote.
func nightlyRun(ctx *Context, name, sha, label, words string) (*bench.Run, error) {
	args := []string{name, "--setup", sha, "--label", label, "--quote", words}
	runErr := nightlyBenchRun(ctx, args)
	runs := bench.Runs(name)
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Setup.Label == label {
			return runs[i], nil
		}
	}
	if runErr != nil {
		return nil, runErr
	}
	return nil, fmt.Errorf("bench run finished but no run labelled %s is in %s", label, bench.RunsDir(name))
}

// nextQueued is the experiment the owner queued longest ago that no night
// has taken.
func nextQueued(issues []*tracker.Issue) (*tracker.Issue, *experiment.Block) {
	type q struct {
		is *tracker.Issue
		b  *experiment.Block
	}
	var all []q
	for _, is := range issues {
		if !experiment.Is(is.Labels) {
			continue
		}
		b, err := experiment.Load(is.FilePath)
		if err != nil || b == nil || !b.Queue.Waiting() {
			continue
		}
		all = append(all, q{is, b})
	}
	if len(all) == 0 {
		return nil, nil
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].b.Queue.At.Before(all[j].b.Queue.At) })
	return all[0].is, all[0].b
}

// nightlyExperiment runs the queued experiment once against tonight's
// baseline and records its verdict (block, experiment: comment). It never
// adopts, parks or reverts. A baseline that didn't finish, a machine over
// the line or a guard problem skips it; only a guard problem ends its
// queue entry (the owner fixes the branch and queues it again).
func nightlyExperiment(ctx *Context, name, date string, base *bench.Run, issue *tracker.Issue, b *experiment.Block, words string) *bench.NightExperiment {
	ne := &bench.NightExperiment{Slug: issue.Slug}
	skip := func(why string, done bool) *bench.NightExperiment {
		ne.Skipped, ne.Why = true, why
		if done {
			b.Queue.Ran, b.Queue.Why = date, why
			if err := experiment.Save(issue.FilePath, b); err != nil {
				ne.Why += " (queue not saved: " + err.Error() + ")"
			}
			_ = tracker.AddCommentBy(issue.FilePath, 0, fmt.Sprintf("experiment: night %s skipped it: %s. Queue it again once fixed: issue-cli experiment queue %s --quote \"…\"", date, why, issue.Slug), "cli", commentAuthor(ctx.Project, issue))
		}
		fmt.Fprintf(ctx.Stdout, "Experiment %s skipped: %s\n", issue.Slug, why)
		return ne
	}
	if base.Stop != bench.StopDone {
		return skip("the baseline did not finish ("+valueOr(base.Stop, "running")+"); it stays queued", false)
	}
	if held := nightlyMachine(); held != "" {
		return skip("held: "+held+"; it stays queued", false)
	}
	ref := valueOr(b.Queue.Ref, "work/"+issue.Slug)
	plan, err := planExperiment(ctx, issue, b, ref, name)
	if err != nil {
		return skip(err.Error(), true)
	}
	if len(plan.Problems) > 0 {
		return skip("guard: "+strings.Join(plan.Problems, "; "), true)
	}
	label := "experiment:" + issue.Slug + "@" + shortSHA(plan.RefSHA)
	cand, err := nightlyRun(ctx, name, plan.RefSHA, label, words)
	if err != nil {
		return skip("candidate run: "+err.Error()+"; it stays queued", false)
	}
	ne.Run = cand.ID
	benchDir := experiment.BenchDir()
	baseRun, err1 := experiment.LoadRun(benchDir, name, base.ID)
	candRun, err2 := experiment.LoadRun(benchDir, name, cand.ID)
	if err1 != nil || err2 != nil {
		return skip(fmt.Sprintf("runs not readable: %v %v", err1, err2), true)
	}
	b.Benchmark = name
	b.Baseline = &experiment.RunRef{Ref: "main", SHA: plan.MainSHA, Run: base.ID}
	b.Candidate = &experiment.RunRef{Ref: ref, SHA: plan.RefSHA, Run: cand.ID}
	b.Queue.Ran = date
	if err := recordExperiment(ctx, issue, b, baseRun, candRun); err != nil {
		ne.Why = err.Error()
	}
	ne.Verdict = b.Verdict
	if len(b.Reasons) > 0 && ne.Why == "" {
		ne.Why = b.Reasons[0]
	}
	return ne
}

// finishNight records the night, writes its morning report and prints it.
func finishNight(ctx *Context, night bench.Night, before []bench.Night) error {
	if err := bench.AppendNight(night); err != nil {
		return err
	}
	all := append(append([]bench.Night(nil), before...), night)
	m := bench.MorningOf(night.Benchmark, all)
	path := bench.NightReportPath(night.Benchmark, night.Date)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(m.Markdown()), 0o644); err != nil {
		return err
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "\nLast night: %s\n", m.Line())
	fmt.Fprintf(w, "Morning report: %s\n", path)
	if r := bench.RegressionOf(all); r != nil {
		fmt.Fprintf(w, "⚠ pass rate %.0f%% is under last week's median %.0f%% (%d nights): Needs you shows it\n", r.Night.PassRate*100, r.Median*100, r.Nights)
	}
	fmt.Fprintln(w, "\nNext:")
	if night.JobRun != "" {
		fmt.Fprintln(w, "  "+tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli output add --issue %s --kind report --file %s", night.JobRun, path)))
		fmt.Fprintln(w, "  "+tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"report: %s\"", night.JobRun, m.Line())))
	}
	if night.Run != "" {
		fmt.Fprintf(w, "  issue-cli bench report %s/%s\n", night.Benchmark, night.Run)
	}
	return nil
}

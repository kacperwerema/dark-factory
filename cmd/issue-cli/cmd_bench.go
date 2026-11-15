package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"

	"slices"
	"strings"

	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"

	"github.com/kacperwerema/dark-factory/internal/gitdir"

	"github.com/kacperwerema/dark-factory/internal/memguard"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var benchCommand = &Command{
	Name:      "bench",
	ShortHelp: "The factory benchmark: golden tasks re-solved in a sandbox, runs, reports, compare",
	LongHelp: `Measure how well the factory works: done issues with a known good result
(golden tasks) are solved again by a worker in a sandbox from their
starting commit, and the runs of two setups are compared.

The benchmark is the owner's: it lives in ~/.config/ai-workflow-engine/bench/
($XDG_CONFIG_HOME, or $ISSUE_VIEWER_BENCH_DIR), outside every repo. Commands
that change it (task add|remove, holdout, budget) refuse in an agent session.
Holdout tasks run with the rest but every output shows them only as
passed n/m.

  bench                                    list the benchmarks and their last run
  bench candidates [--all] [--limit <n>] [--department <key>] [--replay [<slug>...]]
      Done issues of this project that make good tasks: 20 by default, the
      ones with the fewest risks first, across types (--limit, --all shows
      every one with why). The owner picks about 10. --replay replays each
      proposed one in a throwaway worktree (removed after): base with the
      hidden tests must fail them, base plus the branch's own commits (merges
      from main left out) must pass them. Each gets solvable / needs later
      main / doesn't replay / tests pass on base / not checked, with why.
      Runs go test one candidate at a time after mem-guard; no model call,
      nothing written.
  bench task add <benchmark> <slug> [--base <sha> --merged <sha>] [--start <status>]
                 [--check "<cmd>"] [--holdout] [--snapshot <file>] [--no-verify]
      Finds base, merged and the hidden test files from the issue's stats,
      snapshots the issue as it stood at --start (default "in progress"),
      and checks the merged and base commits once. Creates the benchmark.
      A department's issue (or --output <id>) is an output task: the approved
      output's file is copied as its reference, the eval scores the run's own.
      Also writes a reference per stage beside the snapshot: design.md (the
      issue's ## Design as it left its design status), docs.diff (the docs the
      branch changed) and a docs rubric (bundled lines plus --docs-rubric
      "<line>", repeatable); bench show prints each task's stages line.
  bench task remove <benchmark> <task>
  bench holdout <benchmark> <task> on|off
  bench budget <benchmark> [--tokens <n>] [--usd <x>] [--wall 6h] [--task-wall 45m]
      Tokens count cache reads (about 15M a task here); unset, the cap is
      25M a task. Size --tokens per task: 10 tasks, 250000000.
  bench show <benchmark>                   tasks, budget, worker and judge
  bench fixture init <dir> [--config <projects.yaml>]
      A small project written by us with three done tasks and their known
      solutions (a feature, a tweak and a feature with an option), as git
      history the benchmark reads: <dir>/repo and <dir>/tracker, the same
      SHAs on every machine. Prints the projects.yaml entry (--config adds
      it). Adds no task: the owner picks with bench task add.
  bench run <benchmark> [--agents claude,codex] [--setup <engine ref>] [--engine <dir>]
            [--workflow <file>] [--label <name>] [--resume <run>] [--keep]
            [--quote "<owner's words>"]
      One task at a time: mem-guard check, a sandbox (clone with no remote at
      base, scratch tracker with YOLO on), the worker, then the hidden tests
      from the merged commit and the check. Stops at the budget. An agent
      session needs --quote, the owner's words asking for the run.
      --agents runs every task on each agent, one after the other, each in
      its own sandbox: one run.json, the agent on each record. workers: in
      the benchmark file names an agent's command.
  bench report [<benchmark>/<run>|<benchmark>/latest]
  bench compare <run>
      A run on two agents: pass rate, time, tokens and cost by agent and by
      type, and a Routing: line per type saying which routing: rule the
      numbers support or contradict. Cost without a price is unknown.
  bench compare <runA> <runB> [--branch <ref>]
      exit 0 no regression, 1 B regresses, 2 not comparable. By workflow:
      each workflow's verdict (net positive / net negative / no change / not
      comparable) with pass rate, eval score, wall, tokens and cost, A → B. By
      agent as above when the two runs hold two agents between them.
      --branch marks the workflows the ref changed since main (its own
      file, a department's prompt or folder, a bundled template it is
      stamped with, engine code: all); the rest are controls.
  bench nightly [<benchmark>] [--run <job run slug>] [--quote "<owner's words>"] [--dry-run]
      The night: the benchmark on main's head, then at most one experiment
      the owner queued (experiment queue), judged against that baseline and
      never adopted; then the morning numbers (pass rate, median time and
      tokens per task) vs last night and the 7-night trend, in
      <bench>/runs/<benchmark>/nights.jsonl and nights/<date>.md. Held, and
      nothing run, while memory is ` + defaultBelowPct + `% or more or the load is at or
      over the core count (or auto-start's max_load). --run is the product/nightly-improve job's run issue: its
      standing start's words start the runs.

<task> is <project>/<slug>, or the slug when it is unique.

Examples:
  issue-cli bench candidates
  issue-cli bench task add factory-v1 cli/prove-list-attaches-proves-trailers --check "go vet ./..."
  issue-cli bench budget factory-v1 --tokens 250000000 --usd 40 --wall 6h --task-wall 45m
  issue-cli bench run factory-v1 --label baseline
  issue-cli bench run factory-v1 --agents claude,codex --label a-b
  issue-cli bench compare factory-v1/latest
  issue-cli bench run factory-v1 --setup work/factory/new-prompts --label new-prompts
  issue-cli bench compare factory-v1/20261030-120000 factory-v1/latest`,
	Run:         runBench,
	Subcommands: []string{"candidates", "task", "holdout", "budget", "show", "run", "report", "compare", "nightly", "fixture"},
}

func init() {
	registerCommand(benchCommand)
}

func runBench(ctx *Context, args []string) error {
	if len(args) == 0 {
		return runBenchList(ctx)
	}
	switch args[0] {
	case "list":
		return runBenchList(ctx)
	case "candidates":
		return runBenchCandidates(ctx, args[1:])
	case "task":
		if len(args) > 1 && args[1] == "add" {
			return runBenchTaskAdd(ctx, args[2:])
		}
		if len(args) > 1 && args[1] == "remove" {
			return runBenchTaskRemove(ctx, args[2:])
		}
		if len(args) > 1 {
			if _, err := parsePositionals(ctx, "bench task", args[1:], -1); err != nil {
				return err
			}
		}
		return &usageError{msg: "usage: issue-cli bench task add|remove ... (issue-cli help bench)"}
	case "holdout":
		return runBenchHoldout(ctx, args[1:])
	case "budget":
		return runBenchBudget(ctx, args[1:])
	case "show":
		return runBenchShow(ctx, args[1:])
	case "run":
		return runBenchRun(ctx, args[1:])
	case "report":
		return runBenchReport(ctx, args[1:])
	case "compare":
		return runBenchCompare(ctx, args[1:])
	case "nightly":
		return runBenchNightly(ctx, args[1:])
	case "fixture":
		return runBenchFixture(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown bench command %q: candidates, task, holdout, budget, show, run, report, compare, nightly or fixture", args[0])}
}

// agentSession names the agent session issue-cli runs in ("" for the owner's
// own shell): a lead or worker by its signals, or any Claude Code session.
func agentSession(ctx *Context) string {
	me := whoAmI(ctx.Project, nil)
	if me.Role != tracker.RoleUnmanaged {
		if me.Name != "" {
			return me.Name
		}
		return string(me.Role)
	}
	if os.Getenv("CLAUDECODE") != "" {
		return "a Claude Code session"
	}
	return ""
}

// ownerOnly refuses a change to the benchmark from an agent session: only
// the owner changes the benchmark and its metrics.
func ownerOnly(ctx *Context, verb string) error {
	who := guardAgent(ctx, "bench "+verb)
	if who == "" {
		return nil
	}
	return fmt.Errorf("only the owner changes the benchmark; %s %s changed nothing.\n\nThis is %s. Propose the change on your issue trail instead:\n  %s",
		"bench", verb, who, tracker.InjectProject(ctx.Project, `issue-cli comment <slug> "question: add <task> to the benchmark?"`))
}

func loadBenchmark(name string) (*bench.Benchmark, error) {
	b, err := bench.Load(name)
	if errors.Is(err, bench.ErrNoBenchmark) {
		return nil, fmt.Errorf("%w\n\nStart one with: issue-cli bench task add %s <slug>", err, name)
	}
	return b, err
}

func runBenchList(ctx *Context) error {
	names := bench.List()
	if ctx.JSONOutput {
		type row struct {
			Name  string     `json:"name"`
			Tasks int        `json:"tasks"`
			Last  *bench.Run `json:"last_run,omitempty"`
		}
		var rows []row
		for _, n := range names {
			b, err := bench.Load(n)
			if err != nil {
				continue
			}
			runs := bench.Runs(n)
			r := row{Name: n, Tasks: len(b.Tasks)}
			if len(runs) > 0 {
				r.Last = runs[len(runs)-1]
			}
			rows = append(rows, r)
		}
		return writeJSON(ctx.Stdout, rows)
	}
	if len(names) == 0 {
		fmt.Fprintf(ctx.Stdout, "No benchmark yet (%s).\n\nNext:\n  issue-cli bench candidates               done issues that make good golden tasks\n  issue-cli bench task add <name> <slug>   the owner adds one (creates the benchmark)\n", bench.Dir())
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "== Benchmarks (%s) ==\n", bench.Dir())
	for _, n := range names {
		b, err := bench.Load(n)
		if err != nil {
			fmt.Fprintf(ctx.Stdout, "  %s  (%v)\n", n, err)
			continue
		}
		hold := 0
		for _, t := range b.Tasks {
			if t.Holdout {
				hold++
			}
		}
		line := fmt.Sprintf("  %s  %d tasks (%d holdout)", n, len(b.Tasks), hold)
		if runs := bench.Runs(n); len(runs) > 0 {
			r := runs[len(runs)-1]
			line += "  last run " + r.ID + ": " + runLine(r)
		} else {
			line += "  no runs yet: issue-cli bench run " + n
		}
		fmt.Fprintln(ctx.Stdout, line)
	}
	return nil
}

func runLine(r *bench.Run) string {
	s := r.Summary
	line := fmt.Sprintf("%d/%d passed", s.Passed, s.Scored)
	if r.Holdout.Total > 0 {
		line += fmt.Sprintf(", holdout %d/%d", r.Holdout.Passed, r.Holdout.Total)
	}
	if s.Judge >= 0 {
		line += fmt.Sprintf(", judge %.1f", s.Judge)
	}
	line += fmt.Sprintf(", %s, %s", tokensWord(r.SpentTokens, s.TokensKnown), r.CostText())
	if r.Stop != bench.StopDone {
		line += " (" + orRunning(r.Stop) + ")"
	}
	return line
}

func tokensWord(n int64, known bool) string {
	if !known {
		return fmt.Sprintf("%d+ tokens (some unknown)", n)
	}
	return fmt.Sprintf("%d tokens", n)
}

func orRunning(s string) string {
	if s == "" {
		return "running"
	}
	return s
}

func runBenchCandidates(ctx *Context, args []string) error {
	fs := newFlagSet("bench candidates", ctx)
	all := fs.Bool("all", false, "also list the done issues that are not proposed, with why")
	name := fs.String("benchmark", "", "leave out tasks already in this benchmark")
	replay := fs.Bool("replay", false, "replay each proposed candidate on its base with the hidden tests (runs go test; no model call)")
	limit := fs.Int("limit", bench.DefaultCandidateLimit, "how many to propose, the fewest risks first, across types")
	deptArg := fs.String("department", "", "list output tasks: the department's done issues whose output the owner approved, with the eval that would score each")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 && !*replay {
		return &usageError{msg: "usage: issue-cli bench candidates [--all] [--department <key>] [--replay [<slug>...]] (slugs only pick which to --replay)"}
	}
	dept, err := departmentFlag(ctx.Project, *deptArg)
	if err != nil {
		return err
	}
	if dept != "" && *replay {
		return &usageError{msg: "--replay checks hidden tests, which output tasks have none: drop --replay or --department"}
	}
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	var in *bench.Benchmark
	if *name != "" {
		if in, err = loadBenchmark(*name); err != nil {
			return err
		}
	}
	if dept != "" {
		return printOutputCandidates(ctx, issues, tracker.DepartmentKey(dept), in, *all)
	}
	cands := bench.Candidates(ctx.Project, issues, in)
	proposed := 0
	for _, c := range cands {
		if c.Why == "" {
			proposed++
		}
	}
	if !*all {
		// Named slugs are the caller's own pick: every one is replayed.
		n := *limit
		if *replay && fs.NArg() > 0 {
			n = proposed
		}
		cands = bench.PickCandidates(cands, n)
	}
	if *replay {
		replayCandidates(ctx, cands, fs.Args())
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, cands)
	}
	fmt.Fprintf(ctx.Stdout, "== Golden task candidates: %s ==\n", ctx.Project.Slug)
	if len(cands) == 0 {
		fmt.Fprintln(ctx.Stdout, "  none (a candidate is a done issue with a recorded branch, Go tests in its commits and at most 600 changed lines; --all shows why each is left out)")
		fmt.Fprintln(ctx.Stdout, "  A project with known tasks: issue-cli bench fixture init <dir> writes one (three tasks with a design, hidden tests and docs each).")
		printOutputTasksHint(ctx)
		return nil
	}
	for _, c := range cands {
		if c.Why != "" {
			fmt.Fprintf(ctx.Stdout, "  - %s  %s\n", c.Slug, c.Why)
			continue
		}
		fmt.Fprintf(ctx.Stdout, "  %s  %s · %d lines in %d files · %d hidden test files (%d tests) · %s..%s\n",
			c.Slug, orType(c.Type), c.Lines, c.Files, c.Hidden, c.Tests, c.Base, c.Merged)
		if len(c.Risks) > 0 {
			fmt.Fprintf(ctx.Stdout, "      risk: %s\n", strings.Join(c.Risks, ", "))
		}
		if len(c.Stages) > 0 {
			fmt.Fprintf(ctx.Stdout, "      stages: %s\n", strings.Join(c.Stages, " · "))
		}
		if c.Replay != "" {
			fmt.Fprintf(ctx.Stdout, "      replay: %s — %s\n", c.Replay, c.ReplayWhy)
		}
	}
	if !*replay {
		fmt.Fprintf(ctx.Stdout, "\nSolvability: issue-cli bench candidates --replay [<slug>...] replays each on its base with the hidden tests (go test, no model call).")
	}
	if len(cands) < proposed {
		fmt.Fprintf(ctx.Stdout, "\nShowing %d of %d proposed (--limit <n>, --all for every one).", len(cands), proposed)
	}
	fmt.Fprintf(ctx.Stdout, "\nThe owner picks the set, about 10: issue-cli bench task add <benchmark> <slug> [--holdout]\n")
	fmt.Fprintf(ctx.Stdout, "An agent proposes on its issue trail (issue-cli comment <issue> \"proposal: …\"); only the owner adds, removes or changes a task.\n")
	fmt.Fprintf(ctx.Stdout, "Then both agents on them: issue-cli bench run <benchmark> --agents claude,codex, read with issue-cli bench compare <benchmark>/latest\n")
	printOutputTasksHint(ctx)
	return nil
}

// printOutputTasksHint names `--department` when a department's own
// workflow declares evals: its tasks come from the shelf, not hidden tests.
func printOutputTasksHint(ctx *Context) {
	if keys := bench.EvalDepartments(ctx.Project); len(keys) > 0 {
		fmt.Fprintf(ctx.Stdout, "Output tasks: issue-cli bench candidates --department <key> lists done issues whose output the owner approved, with the eval that would score each (evals in: %s)\n", strings.Join(keys, ", "))
	}
}

// printOutputCandidates is `bench candidates --department <key>`.
func printOutputCandidates(ctx *Context, issues []*tracker.Issue, key string, in *bench.Benchmark, all bool) error {
	cands, err := bench.OutputCandidates(ctx.Project, issues, key, in)
	if err != nil {
		return err
	}
	if !all {
		kept := cands[:0]
		for _, c := range cands {
			if c.Why == "" {
				kept = append(kept, c)
			}
		}
		cands = kept
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, cands)
	}
	fmt.Fprintf(ctx.Stdout, "== Output task candidates: %s · %s ==\n", ctx.Project.Slug, key)
	if len(cands) == 0 {
		fmt.Fprintf(ctx.Stdout, "  none (a candidate is a done %s issue with an output the owner approved and an eval for its type in the %s workflow; --all shows why each is left out)\n", key, key)
		return nil
	}
	for _, c := range cands {
		if c.Why != "" {
			fmt.Fprintf(ctx.Stdout, "  - %s  %s\n", c.Slug, c.Why)
			continue
		}
		title := ""
		if c.OutputTitle != "" {
			title = " (" + c.OutputTitle + ")"
		}
		fmt.Fprintf(ctx.Stdout, "  %s  %s · output %s%s · eval %s (%s)\n", c.Slug, orType(c.Type), c.Output, title, c.Eval, c.EvalScore)
	}
	fmt.Fprintf(ctx.Stdout, "\nThe owner picks the set: issue-cli bench task add <benchmark> <slug> [--holdout] (the reference is the approved output; --output <id> picks another)\n")
	return nil
}

// replayMemGuard is the memory check before each replay's go test; tests
// replace it.
var replayMemGuard = func(log io.Writer) func() (bool, string) { return replayGuard(memGuard(log)) }

// replayGuard blocks at mem-guard's BLOCKED and also at a reading of
// memguard.ResumeAt or more: replays are optional work, started only on a quiet machine.
func replayGuard(check func() (bool, string)) func() (bool, string) {
	if check == nil {
		return nil
	}
	return func() (bool, string) {
		blocked, msg := check()
		if n, ok := memguard.CheckUsed(msg); ok && n >= memguard.ResumeAt {
			blocked = true
		}
		return blocked, msg
	}
}

// replayCandidates runs `bench candidates --replay`: one candidate at a
// time, progress on stderr.
func replayCandidates(ctx *Context, cands []bench.Candidate, only []string) {
	env := bench.ReplayEnv{MemGuard: replayMemGuard(ctx.Stderr)}
	bench.CheckCandidates(context.Background(), cands, only, env, func(i, n int, c bench.Candidate) {
		fmt.Fprintf(ctx.Stderr, "replaying %s (%d/%d) on %s\n", c.Slug, i, n, c.Base)
	})
}

func orType(s string) string {
	if s == "" {
		return "untyped"
	}
	return s
}

func runBenchTaskAdd(ctx *Context, args []string) error {
	fs := newFlagSet("bench task add", ctx)
	base := fs.String("base", "", "the commit the worker starts from (default: found from the issue's stats)")
	merged := fs.String("merged", "", "the known good result (default: the issue's merge or last branch head)")
	start := fs.String("start", "", `the status the worker gets the issue in (default "in progress")`)
	check := fs.String("check", "", "a shell command that must pass besides the hidden tests")
	holdout := fs.Bool("holdout", false, "a holdout task: shown only as passed n/m")
	snapshot := fs.String("snapshot", "", "a file with the issue as it stood at --start (default: from the tracker's git history)")
	noVerify := fs.Bool("no-verify", false, "skip checking the merged and base commits")
	output := fs.String("output", "", "an output task from this shelf output the owner approved (a department's issue: the default is its newest approved one)")
	var docsRubric multiFlag
	fs.Var(&docsRubric, "docs-rubric", "a rubric line for the docs stage, beside the bundled ones (repeatable)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return &usageError{msg: "usage: issue-cli bench task add <benchmark> <slug> [--check \"<cmd>\"] [--holdout] ..."}
	}
	if err := ownerOnly(ctx, "task add"); err != nil {
		return err
	}
	name := fs.Arg(0)
	b, err := bench.Load(name)
	if errors.Is(err, bench.ErrNoBenchmark) {
		b, err = bench.New(name), nil
	}
	if err != nil {
		return err
	}
	issue, _, err := findIssueOrErr(ctx, fs.Arg(1))
	if err != nil {
		return err
	}
	if _, t := b.Task(ctx.Project.Slug + "/" + issue.Slug); t != nil {
		return fmt.Errorf("%s is already in %s", t.ID(), name)
	}
	logf := func(format string, a ...any) { fmt.Fprintf(ctx.Stderr, format+"\n", a...) }
	opt := bench.AddOptions{
		Base: *base, Merged: *merged, Start: *start, Check: *check, Holdout: *holdout,
		Snapshot: *snapshot, Verify: !*noVerify, Log: logf, DocsRubric: docsRubric,
	}
	var t bench.Task
	var warnings []string
	if dept := tracker.DepartmentKey(ctx.Project.DepartmentOf(issue)); *output != "" || (dept != "" && dept != bench.ProductWorkflow) {
		t, warnings, err = bench.NewOutputTask(context.Background(), ctx.Project, issue, *output, opt, ctx.now())
	} else {
		t, warnings, err = bench.NewTask(context.Background(), ctx.Project, issue, opt, ctx.now())
	}
	for _, w := range warnings {
		fmt.Fprintf(ctx.Stderr, "warning: %s\n", w)
	}
	if err != nil {
		return fmt.Errorf("%s not added: %w", issue.Slug, err)
	}
	b.Tasks = append(b.Tasks, t)
	if err := b.Save(); err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, t)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Added %s to %s (%d tasks)\n", t.ID(), name, len(b.Tasks))
	if t.Output != "" {
		fmt.Fprintf(ctx.Stdout, "  output task (%s): base %s, starts at %q; reference: output %s (%s)\n", t.WorkflowKey(), gitdir.ShortSHA(t.Base), t.Start, t.Output, bench.ReferencePath(t))
		fmt.Fprintf(ctx.Stdout, "  scored by the %s workflow's eval for %s; check: %s\n", t.WorkflowKey(), orType(t.Type), orNone(t.Check))
	} else {
		fmt.Fprintf(ctx.Stdout, "  base %s → merged %s, starts at %q\n", gitdir.ShortSHA(t.Base), gitdir.ShortSHA(t.Merged), t.Start)
		fmt.Fprintf(ctx.Stdout, "  hidden: %d test files, %d Go tests; check: %s\n", len(t.Hidden), len(t.HiddenTests), orNone(t.Check))
		fmt.Fprintf(ctx.Stdout, "  stages: %s\n", bench.StagesLine(t))
	}
	if t.Holdout {
		fmt.Fprintln(ctx.Stdout, "  holdout: shown only as passed n/m")
	}
	fmt.Fprintf(ctx.Stdout, "  file: %s\n", b.Path())
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func runBenchTaskRemove(ctx *Context, args []string) error {
	pos, err := parsePositionals(ctx, "bench task remove", args, 2)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return &usageError{msg: "usage: issue-cli bench task remove <benchmark> <task>"}
	}
	if err := ownerOnly(ctx, "task remove"); err != nil {
		return err
	}
	b, err := loadBenchmark(pos[0])
	if err != nil {
		return err
	}
	i, t := b.Task(pos[1])
	if t == nil {
		return fmt.Errorf("no task %s in %s", pos[1], b.Name)
	}
	id := t.ID()
	b.Tasks = append(b.Tasks[:i], b.Tasks[i+1:]...)
	if err := b.Save(); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Removed %s from %s (%d tasks)\n", id, b.Name, len(b.Tasks))
	return nil
}

func runBenchHoldout(ctx *Context, args []string) error {
	pos, err := parsePositionals(ctx, "bench holdout", args, 3)
	if err != nil {
		return err
	}
	if len(pos) != 3 || (pos[2] != "on" && pos[2] != "off") {
		return &usageError{msg: "usage: issue-cli bench holdout <benchmark> <task> on|off"}
	}
	if err := ownerOnly(ctx, "holdout"); err != nil {
		return err
	}
	b, err := loadBenchmark(pos[0])
	if err != nil {
		return err
	}
	_, t := b.Task(pos[1])
	if t == nil {
		return fmt.Errorf("no task %s in %s", pos[1], b.Name)
	}
	t.Holdout = pos[2] == "on"
	if err := b.Save(); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s holdout %s\n", t.ID(), pos[2])
	return nil
}

func runBenchBudget(ctx *Context, args []string) error {
	fs := newFlagSet("bench budget", ctx)
	tokens := fs.Int64("tokens", -1, "the run's token cap (0: none)")
	usd := fs.Float64("usd", -1, "the run's cost cap in USD (0: none)")
	wall := fs.String("wall", "", "the run's wall-time cap, e.g. 6h (0: none)")
	taskWall := fs.String("task-wall", "", "each task's wall-time cap, e.g. 45m")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli bench budget <benchmark> [--tokens <n>] [--usd <x>] [--wall 6h] [--task-wall 45m]"}
	}
	b, err := loadBenchmark(fs.Arg(0))
	if err != nil {
		return err
	}
	changed := *tokens >= 0 || *usd >= 0 || *wall != "" || *taskWall != ""
	if changed {
		if err := ownerOnly(ctx, "budget"); err != nil {
			return err
		}
		for _, d := range []string{*wall, *taskWall} {
			if d != "" && d != "0" {
				if _, err := time.ParseDuration(d); err != nil {
					return &usageError{msg: fmt.Sprintf("bench budget: %q is not a duration (6h, 45m)", d)}
				}
			}
		}
		if *tokens >= 0 {
			b.Budget.Tokens = *tokens
		}
		if *usd >= 0 {
			b.Budget.USD = *usd
		}
		if *wall != "" {
			b.Budget.Wall = strings.TrimPrefix(*wall, "0")
		}
		if *taskWall != "" {
			b.Budget.TaskWall = strings.TrimPrefix(*taskWall, "0")
		}
		if err := b.Save(); err != nil {
			return err
		}
	}
	fmt.Fprintf(ctx.Stdout, "%s budget: %s\n", b.Name, budgetLine(b, len(b.Tasks)))
	fmt.Fprint(ctx.Stdout, lowTokensNote(b.Budget))
	return nil
}

// budgetLine is the budget in words for a run of jobs tasks (tasks × agents).
func budgetLine(bm *bench.Benchmark, jobs int) string {
	b := bm.Budget
	part := func(set bool, s string) string {
		if !set {
			return "none"
		}
		return s
	}
	tokens := fmt.Sprint(b.Tokens)
	if b.Tokens <= 0 {
		tokens = fmt.Sprintf("%d (default: %s a task × %d, cache reads included)", b.TokenCap(jobs), millions(bench.DefaultTaskTokens), max(jobs, 1))
	}
	return fmt.Sprintf("tokens %s, cost %s, wall %s, per task %s",
		tokens, part(b.USD > 0, fmt.Sprintf("$%.2f", b.USD)),
		part(b.WallDur() > 0, b.Wall), b.TaskWallDur())
}

// millions is n in millions, "25M".
func millions(n int64) string { return fmt.Sprintf("%gM", float64(n)/1e6) }

// lowTokensNote warns when a token cap is under one task's default share:
// tokens count cache reads, so such a cap stops the first task part way.
func lowTokensNote(b bench.Budget) string {
	if b.Tokens <= 0 || b.Tokens >= bench.DefaultTaskTokens {
		return ""
	}
	return fmt.Sprintf("note: %s tokens is under one task (%s, cache reads included: about 15M a task here); the run will likely stop in its first task\n",
		millions(b.Tokens), millions(bench.DefaultTaskTokens))
}

func runBenchShow(ctx *Context, args []string) error {
	pos, err := parsePositionals(ctx, "bench show", args, 1)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return &usageError{msg: "usage: issue-cli bench show <benchmark>"}
	}
	b, err := loadBenchmark(pos[0])
	if err != nil {
		return err
	}
	owner := agentSession(ctx) == ""
	var shown []bench.Task
	hold := 0
	for _, t := range b.Tasks {
		if t.Holdout && !owner {
			hold++
			continue
		}
		shown = append(shown, t)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"name": b.Name, "budget": b.Budget, "tasks": shown, "holdout_hidden": hold, "worker": orDefault(b.Worker, bench.DefaultWorker), "workers": b.Workers, "judge": b.Judge})
	}
	fmt.Fprintf(ctx.Stdout, "== Benchmark %s ==\nfile: %s\nbudget: %s\nworker: %s\n", b.Name, b.Path(), budgetLine(b, len(b.Tasks)), orDefault(b.Worker, bench.DefaultWorker))
	for _, a := range slices.Sorted(maps.Keys(b.Workers)) {
		fmt.Fprintf(ctx.Stdout, "worker on %s: %s\n", a, b.Workers[a])
	}
	fmt.Fprintf(ctx.Stdout, "judge: %s\n\nTasks:\n", orNone(b.Judge))
	for _, t := range shown {
		mark := ""
		if t.Holdout {
			mark = " [holdout]"
		}
		fmt.Fprintf(ctx.Stdout, "  %s%s  %s · %s..%s · %d hidden tests · check: %s\n", t.ID(), mark, orType(t.Type), gitdir.ShortSHA(t.Base), gitdir.ShortSHA(t.Merged), len(t.HiddenTests), orNone(t.Check))
		if t.Output == "" {
			fmt.Fprintf(ctx.Stdout, "      stages: %s\n", bench.StagesLine(t))
		}
	}
	if hold > 0 {
		fmt.Fprintf(ctx.Stdout, "  + %d holdout tasks (not shown in an agent session)\n", hold)
	}
	return nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// benchExecute and benchCLIFor are vars so a test sees what a run gets
// without starting a worker, or running its own test binary as issue-cli.
var (
	benchExecute = bench.Execute
	benchCLIFor  = benchIssueCLI
)

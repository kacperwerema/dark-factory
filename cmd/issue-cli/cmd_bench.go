package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/michal-franc/issue-viewer/internal/bench"
	"github.com/michal-franc/issue-viewer/internal/experiment"
	"github.com/michal-franc/issue-viewer/internal/health"
	"github.com/michal-franc/issue-viewer/internal/spend"
	"github.com/michal-franc/issue-viewer/internal/tracker"
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
  bench candidates [--all] [--department <key>] [--replay [<slug>...]]
      Done issues of this project that make good tasks. --replay replays each
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
  bench task remove <benchmark> <task>
  bench holdout <benchmark> <task> on|off
  bench budget <benchmark> [--tokens <n>] [--usd <x>] [--wall 6h] [--task-wall 45m]
      Tokens count cache reads (about 15M a task here); unset, the cap is
      25M a task. Size --tokens per task: 10 tasks, 250000000.
  bench show <benchmark>                   tasks, budget, worker and judge
  bench run <benchmark> [--setup <engine ref>] [--engine <dir>] [--workflow <file>]
            [--label <name>] [--resume <run>] [--keep] [--quote "<owner's words>"]
      One task at a time: mem-guard check, a sandbox (clone with no remote at
      base, scratch tracker with YOLO on), the worker, then the hidden tests
      from the merged commit and the check. Stops at the budget. An agent
      session needs --quote, the owner's words asking for the run.
  bench report [<benchmark>/<run>|<benchmark>/latest]
  bench compare <runA> <runB> [--branch <ref>]
      exit 0 no regression, 1 B regresses, 2 not comparable. By workflow:
      each workflow's verdict (net positive / net negative / no change / not
      comparable) with pass rate, eval score, wall, tokens and cost, A → B.
      --branch marks the workflows the ref changed since main (its own
      file, a department's prompt or folder, a bundled template it is
      stamped with, engine code: all); the rest are controls.

<task> is <project>/<slug>, or the slug when it is unique.

Examples:
  issue-cli bench candidates
  issue-cli bench task add factory-v1 cli/prove-list-attaches-proves-trailers --check "go vet ./..."
  issue-cli bench budget factory-v1 --tokens 250000000 --usd 40 --wall 6h --task-wall 45m
  issue-cli bench run factory-v1 --label baseline
  issue-cli bench run factory-v1 --setup work/factory/new-prompts --label new-prompts
  issue-cli bench compare factory-v1/20261030-120000 factory-v1/latest`,
	Run:         runBench,
	Subcommands: []string{"candidates", "task", "holdout", "budget", "show", "run", "report", "compare"},
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
	}
	return &usageError{msg: fmt.Sprintf("unknown bench command %q: candidates, task, holdout, budget, show, run, report or compare", args[0])}
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
	who := agentSession(ctx)
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
	line += fmt.Sprintf(", %s, $%.2f", tokensWord(r.SpentTokens, s.TokensKnown), r.SpentUSD)
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
	if !*all {
		kept := cands[:0]
		for _, c := range cands {
			if c.Why == "" {
				kept = append(kept, c)
			}
		}
		cands = kept
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
		if c.Replay != "" {
			fmt.Fprintf(ctx.Stdout, "      replay: %s — %s\n", c.Replay, c.ReplayWhy)
		}
	}
	if !*replay {
		fmt.Fprintf(ctx.Stdout, "\nSolvability: issue-cli bench candidates --replay [<slug>...] replays each on its base with the hidden tests (go test, no model call).")
	}
	fmt.Fprintf(ctx.Stdout, "\nThe owner picks the set: issue-cli bench task add <benchmark> <slug> [--holdout]\n")
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

// replayGuard blocks at mem-guard's BLOCKED and also at a reading of 70%
// or more: replays are optional work, started only on a quiet machine.
func replayGuard(check func() (bool, string)) func() (bool, string) {
	if check == nil {
		return nil
	}
	return func() (bool, string) {
		blocked, msg := check()
		if m := memUsed.FindStringSubmatch(msg); m != nil {
			if n, _ := strconv.Atoi(m[1]); n >= 70 {
				blocked = true
			}
		}
		return blocked, msg
	}
}

var memUsed = regexp.MustCompile(`used (\d+)%`)

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
		Snapshot: *snapshot, Verify: !*noVerify, Log: logf,
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
		fmt.Fprintf(ctx.Stdout, "  output task (%s): base %s, starts at %q; reference: output %s (%s)\n", t.WorkflowKey(), shortSHA(t.Base), t.Start, t.Output, bench.ReferencePath(t))
		fmt.Fprintf(ctx.Stdout, "  scored by the %s workflow's eval for %s; check: %s\n", t.WorkflowKey(), orType(t.Type), orNone(t.Check))
	} else {
		fmt.Fprintf(ctx.Stdout, "  base %s → merged %s, starts at %q\n", shortSHA(t.Base), shortSHA(t.Merged), t.Start)
		fmt.Fprintf(ctx.Stdout, "  hidden: %d test files, %d Go tests; check: %s\n", len(t.Hidden), len(t.HiddenTests), orNone(t.Check))
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
	fmt.Fprintf(ctx.Stdout, "%s budget: %s\n", b.Name, budgetLine(b))
	fmt.Fprint(ctx.Stdout, lowTokensNote(b.Budget))
	return nil
}

func budgetLine(bm *bench.Benchmark) string {
	b := bm.Budget
	part := func(set bool, s string) string {
		if !set {
			return "none"
		}
		return s
	}
	tokens := fmt.Sprint(b.Tokens)
	if b.Tokens <= 0 {
		tokens = fmt.Sprintf("%d (default: %s a task × %d, cache reads included)", b.TokenCap(len(bm.Tasks)), millions(bench.DefaultTaskTokens), max(len(bm.Tasks), 1))
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
		return writeJSON(ctx.Stdout, map[string]any{"name": b.Name, "budget": b.Budget, "tasks": shown, "holdout_hidden": hold, "worker": orDefault(b.Worker, bench.DefaultWorker), "judge": b.Judge})
	}
	fmt.Fprintf(ctx.Stdout, "== Benchmark %s ==\nfile: %s\nbudget: %s\nworker: %s\njudge: %s\n\nTasks:\n", b.Name, b.Path(), budgetLine(b), orDefault(b.Worker, bench.DefaultWorker), orNone(b.Judge))
	for _, t := range shown {
		mark := ""
		if t.Holdout {
			mark = " [holdout]"
		}
		fmt.Fprintf(ctx.Stdout, "  %s%s  %s · %s..%s · %d hidden tests · check: %s\n", t.ID(), mark, orType(t.Type), shortSHA(t.Base), shortSHA(t.Merged), len(t.HiddenTests), orNone(t.Check))
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

func runBenchRun(ctx *Context, args []string) error {
	fs := newFlagSet("bench run", ctx)
	setup := fs.String("setup", "", "an engine ref: build issue-cli from it for the run (default: this issue-cli)")
	engine := fs.String("engine", "", "the engine checkout --setup is resolved in (default: the current git repository)")
	var workflows multiFlag
	fs.Var(&workflows, "workflow", "a workflow file for Product's tasks instead of the project's, or <department>=<file> for a department's (repeatable)")
	label := fs.String("label", "", "a name for the setup, shown in reports and the trend")
	resume := fs.String("resume", "", "continue a paused or budget-stopped run")
	keep := fs.Bool("keep", false, "leave each task's sandbox in place")
	quote := fs.String("quote", "", "the owner's words asking for this run (required in an agent session)")
	workerFlag := fs.String("worker", "", "the worker command for this run instead of the benchmark's worker: (e.g. a codex exec command), to compare agents")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli bench run <benchmark> [--setup <ref>] [--workflow <file>] [--label <name>] [--worker <cmd>] [--resume <run>]"}
	}
	who := agentSession(ctx)
	q := strings.TrimSpace(normalizeEscapedText(*quote))
	if who != "" && q == "" {
		return &usageError{msg: fmt.Sprintf("bench run starts workers that spend tokens: in an agent session (%s) it needs --quote with the owner's words asking for this run", who)}
	}
	b, err := loadBenchmark(fs.Arg(0))
	if err != nil {
		return err
	}
	if len(b.Tasks) == 0 {
		return fmt.Errorf("benchmark %s has no tasks: issue-cli bench task add %s <slug>", b.Name, b.Name)
	}
	if w := strings.TrimSpace(*workerFlag); w != "" {
		b.Worker = w
	}
	workflow, byBox, err := parseWorkflowFlags(workflows)
	if err != nil {
		return err
	}
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tmp := filepath.Join(os.TempDir(), "awe-bench")
	cli, ref, err := benchIssueCLI(runCtx, *setup, *engine, tmp, ctx.Stderr)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	env := bench.Env{
		Projects: func(slug string) *tracker.Project {
			for i := range ctx.AllProjects {
				if ctx.AllProjects[i].Slug == slug {
					return &ctx.AllProjects[i]
				}
			}
			return nil
		},
		IssueCLI: cli, Home: home, TmpRoot: tmp, Keep: *keep,
		MemGuard: memGuard(ctx.Stderr), MemWait: 30 * time.Minute,
		Log: ctx.Stderr, Now: ctx.now,
	}
	startedBy := "owner"
	if who != "" {
		startedBy = who
	}
	opt := bench.Options{
		Setup:    bench.Setup{Ref: ref, IssueCLI: cli, IssueCLIVersion: cliVersion(cli), Workflow: strings.Join(workflows, ","), Label: *label, Agent: bench.WorkerAgent(orDefault(b.Worker, bench.DefaultWorker))},
		Workflow: workflow, Workflows: byBox, Resume: *resume, StartedBy: startedBy, Quote: q,
	}
	fmt.Fprintf(ctx.Stderr, "bench run %s: %d tasks, budget %s\n", b.Name, len(b.Tasks), budgetLine(b))
	fmt.Fprintf(ctx.Stderr, "estimate: %s\n", bench.Estimate(b))
	// Cost from prices.yaml for the models the benchmark doesn't price, as
	// the rooms' spend does.
	if b.Prices == nil {
		b.Prices = map[string]bench.Price{}
	}
	for model, price := range spend.LoadPrices() {
		if _, ok := b.Prices[model]; !ok {
			b.Prices[model] = price
		}
	}
	run, err := bench.Execute(runCtx, b, env, opt)
	if run != nil && run.Finished() {
		benchSnapshots(ctx, b)
	}
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, run)
	}
	printRun(ctx.Stdout, run)
	prev := previousRun(run)
	fmt.Fprintf(ctx.Stdout, "\nNext:\n  issue-cli bench report %s/%s\n", run.Benchmark, run.ID)
	if prev != nil {
		fmt.Fprintf(ctx.Stdout, "  issue-cli bench compare %s/%s %s/%s\n", prev.Benchmark, prev.ID, run.Benchmark, run.ID)
	}
	if run.Stop == bench.StopPaused || run.Stop == bench.StopBudget {
		fmt.Fprintf(ctx.Stdout, "  issue-cli bench run %s --resume %s   continue it\n", run.Benchmark, run.ID)
	}
	return nil
}

// parseWorkflowFlags splits --workflow values: a bare file is Product's,
// <key>=<file> a department's (its box key); files become absolute.
func parseWorkflowFlags(vals []string) (string, map[string]string, error) {
	var product string
	byBox := map[string]string{}
	for _, v := range vals {
		key, file, ok := strings.Cut(v, "=")
		if !ok {
			key, file = bench.ProductWorkflow, v
		}
		key = tracker.DepartmentKey(strings.TrimSpace(key))
		if strings.TrimSpace(file) == "" {
			return "", nil, &usageError{msg: "usage: --workflow <file> or --workflow <department>=<file>"}
		}
		if abs, err := filepath.Abs(file); err == nil {
			file = abs
		}
		if key == bench.ProductWorkflow {
			product = file
		}
		byBox[key] = file
	}
	return product, byBox, nil
}

// benchIssueCLI is the issue-cli under test: this binary, or one built
// from ref in a throwaway clone of the engine.
func benchIssueCLI(ctx context.Context, ref, engine, tmp string, log io.Writer) (cli, resolved string, err error) {
	if ref == "" {
		self, err := os.Executable()
		if err != nil {
			return "", "", err
		}
		return self, "", nil
	}
	if engine == "" {
		engine, _ = os.Getwd()
	}
	out, err := exec.CommandContext(ctx, "git", "-C", engine, "rev-parse", "--verify", ref+"^{commit}").Output()
	if err != nil {
		return "", "", fmt.Errorf("--setup %s: not a commit in %s", ref, engine)
	}
	resolved = strings.TrimSpace(string(out))
	dir := filepath.Join(tmp, "setup-"+shortSHA(resolved))
	cli = filepath.Join(dir, "bin", "issue-cli")
	if _, err := os.Stat(cli); err == nil {
		return cli, resolved, nil
	}
	fmt.Fprintf(log, "building issue-cli at %s…\n", shortSHA(resolved))
	src := filepath.Join(dir, "src")
	_ = os.RemoveAll(src)
	steps := [][]string{
		{"git", "clone", "--quiet", "--no-checkout", "--shared", engine, src},
		{"git", "-C", src, "checkout", "--quiet", "--detach", resolved},
		{"go", "build", "-o", cli, "./cmd/issue-cli"},
	}
	for _, s := range steps {
		cmd := exec.CommandContext(ctx, s[0], s[1:]...)
		cmd.Dir = src
		if s[0] == "git" {
			cmd.Dir = dir
		}
		_ = os.MkdirAll(dir, 0o755)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("--setup %s: %s: %v\n%s", ref, strings.Join(s, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return cli, resolved, nil
}

func cliVersion(cli string) string {
	out, err := exec.Command(cli, "version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// memGuard is `mem-guard check`: BLOCKED (or a non-zero exit) blocks a
// task. Without mem-guard on PATH nothing blocks, with a warning.
func memGuard(log io.Writer) func() (bool, string) {
	if _, err := exec.LookPath("mem-guard"); err != nil {
		fmt.Fprintln(log, "warning: mem-guard is not on PATH; tasks start without a memory check")
		return nil
	}
	return func() (bool, string) {
		out, err := exec.Command("mem-guard", "check").CombinedOutput()
		msg := strings.TrimSpace(string(out))
		return err != nil || strings.Contains(msg, "BLOCKED"), msg
	}
}

// benchSnapshots takes a health snapshot of every project with a task in
// b, so the bench family's trend gets the run. A project whose snapshot
// fails is skipped with a warning.
func benchSnapshots(ctx *Context, b *bench.Benchmark) {
	done := map[string]bool{}
	for _, t := range b.Tasks {
		if done[t.Project] {
			continue
		}
		done[t.Project] = true
		for i := range ctx.AllProjects {
			p := &ctx.AllProjects[i]
			if p.Slug != t.Project {
				continue
			}
			env, err := health.NewEnv(p, p.Root(), ctx.now())
			if err == nil {
				_, _, err = health.Take(context.Background(), env, health.TriggerBench)
			}
			if err != nil {
				fmt.Fprintf(ctx.Stderr, "warning: health snapshot of %s: %v\n", p.Slug, err)
			}
		}
	}
}

func previousRun(r *bench.Run) *bench.Run {
	runs := bench.Runs(r.Benchmark)
	var prev *bench.Run
	for _, x := range runs {
		if x.ID == r.ID {
			break
		}
		if x.Finished() {
			prev = x
		}
	}
	return prev
}

func printRun(w io.Writer, r *bench.Run) {
	fmt.Fprintf(w, "== Bench run %s/%s ==\n", r.Benchmark, r.ID)
	setup := "this issue-cli"
	if r.Setup.Ref != "" {
		setup = "issue-cli at " + shortSHA(r.Setup.Ref)
	}
	if r.Setup.Workflow != "" {
		setup += ", workflow " + r.Setup.Workflow
	}
	if r.Setup.Agent != "" {
		setup += ", agent " + r.Setup.Agent
	}
	if r.Setup.Label != "" {
		setup = r.Setup.Label + " (" + setup + ")"
	}
	setup += ", worker home " + orDefault(r.Setup.WorkerHome, bench.WorkerHomeOwner)
	fmt.Fprintf(w, "setup: %s\nstarted: %s by %s", setup, r.Started.Local().Format("2006-01-02 15:04"), orDefault(r.StartedBy, "owner"))
	if r.Quote != "" {
		fmt.Fprintf(w, " (%q)", r.Quote)
	}
	fmt.Fprintf(w, "\nstop: %s", orRunning(r.Stop))
	if r.StopNote != "" {
		fmt.Fprintf(w, " — %s", r.StopNote)
	}
	fmt.Fprintf(w, "\nresult: %s\n", runLine(r))
	s := r.Summary
	if s.FirstTryRate >= 0 {
		fmt.Fprintf(w, "gates passed first try: %.0f%% · refused calls %d · backward moves %d\n", s.FirstTryRate*100, s.Refused, s.Backward)
	}
	fmt.Fprintln(w, "\nTasks:")
	for _, t := range r.Tasks {
		judge := "-"
		if t.Judge >= 0 {
			judge = fmt.Sprint(t.Judge)
		}
		tokens := "?"
		if t.Usage.Known {
			tokens = fmt.Sprint(t.Usage.Tokens())
		}
		scored := fmt.Sprintf("hidden %d/%d · check %s · judge %s", t.HiddenPassed, t.HiddenTotal, okWord(t.CheckOK), judge)
		wall := (time.Duration(t.WallSec) * time.Second).String()
		if t.Eval != "" && t.EvalScore >= 0 {
			scored = fmt.Sprintf("%s eval %s %.2f (%d/%d) · check %s", t.Workflow, t.Eval, t.EvalScore, t.EvalPassed, t.EvalTotal, okWord(t.CheckOK))
			wall += fmt.Sprintf(" + scoring %s", secText(t.ScoreWallSec))
		}
		line := fmt.Sprintf("  %-8s %s  %s · %s tokens · $%.2f · %s",
			t.Outcome, t.Task, scored, tokens, t.Usage.USD, wall)
		if t.Reason != "" && t.Outcome != bench.OutcomePass {
			line += " — " + t.Reason
		}
		fmt.Fprintln(w, line)
		if len(t.OutsideTouched) > 0 {
			fmt.Fprintf(w, "           ⚠ wrote outside the sandbox: %s\n", strings.Join(t.OutsideTouched, ", "))
		}
	}
	if r.Holdout.Total > 0 {
		fmt.Fprintf(w, "  holdout: %d/%d passed\n", r.Holdout.Passed, r.Holdout.Total)
	}
}

func okWord(ok bool) string {
	if ok {
		return "green"
	}
	return "red"
}

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
	return nil
}

func runBenchCompare(ctx *Context, args []string) error {
	fs := newFlagSet("bench compare", ctx)
	branch := fs.String("branch", "", "mark the workflows this ref changed since main (the rest are controls)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return &usageError{msg: "usage: issue-cli bench compare <benchmark>/<runA> <benchmark>/<runB> [--branch <ref>]"}
	}
	a, err := bench.FindRun("", fs.Arg(0))
	if err != nil {
		return err
	}
	b, err := bench.FindRun("", fs.Arg(1))
	if err != nil {
		return err
	}
	c := bench.Compare(a, b)
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
	}
	if code != 0 {
		return &exitCodeError{Code: code}
	}
	return nil
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
			compactTokens(v.TokensA), compactTokens(v.TokensB), relText(float64(v.TokensA), float64(v.TokensB)),
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

func compactTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return strconv.FormatInt(n, 10)
}

func scoreText(f float64) string {
	if f < 0 {
		return "–"
	}
	return fmt.Sprintf("%.2f", f)
}

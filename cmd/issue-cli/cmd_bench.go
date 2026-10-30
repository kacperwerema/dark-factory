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
	"strings"
	"syscall"
	"time"

	"github.com/michal-franc/issue-viewer/internal/bench"
	"github.com/michal-franc/issue-viewer/internal/health"
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
  bench candidates [--all]                 done issues of this project that make good tasks
  bench task add <benchmark> <slug> [--base <sha> --merged <sha>] [--start <status>]
                 [--check "<cmd>"] [--holdout] [--snapshot <file>] [--no-verify]
      Finds base, merged and the hidden test files from the issue's stats,
      snapshots the issue as it stood at --start (default "in progress"),
      and checks the merged and base commits once. Creates the benchmark.
  bench task remove <benchmark> <task>
  bench holdout <benchmark> <task> on|off
  bench budget <benchmark> [--tokens <n>] [--usd <x>] [--wall 6h] [--task-wall 45m]
  bench show <benchmark>                   tasks, budget, worker and judge
  bench run <benchmark> [--setup <engine ref>] [--engine <dir>] [--workflow <file>]
            [--label <name>] [--resume <run>] [--keep] [--quote "<owner's words>"]
      One task at a time: mem-guard check, a sandbox (clone with no remote at
      base, scratch tracker with YOLO on), the worker, then the hidden tests
      from the merged commit and the check. Stops at the budget. An agent
      session needs --quote, the owner's words asking for the run.
  bench report [<benchmark>/<run>|<benchmark>/latest]
  bench compare <runA> <runB>              exit 0 no regression, 1 B regresses, 2 not comparable

<task> is <project>/<slug>, or the slug when it is unique.

Examples:
  issue-cli bench candidates
  issue-cli bench task add factory-v1 cli/prove-list-attaches-proves-trailers --check "go vet ./..."
  issue-cli bench budget factory-v1 --tokens 6000000 --usd 40 --wall 6h --task-wall 45m
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
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
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
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, cands)
	}
	fmt.Fprintf(ctx.Stdout, "== Golden task candidates: %s ==\n", ctx.Project.Slug)
	if len(cands) == 0 {
		fmt.Fprintln(ctx.Stdout, "  none (a candidate is a done issue with a recorded branch, test files in its commits and at most 600 changed lines; --all shows why each is left out)")
		return nil
	}
	for _, c := range cands {
		if c.Why != "" {
			fmt.Fprintf(ctx.Stdout, "  - %s  %s\n", c.Slug, c.Why)
			continue
		}
		fmt.Fprintf(ctx.Stdout, "  %s  %s · %d lines in %d files · %d hidden test files (%d tests) · %s..%s\n",
			c.Slug, orType(c.Type), c.Lines, c.Files, c.Hidden, c.Tests, c.Base, c.Merged)
	}
	fmt.Fprintf(ctx.Stdout, "\nThe owner picks the set: issue-cli bench task add <benchmark> <slug> [--holdout]\n")
	return nil
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
	t, warnings, err := bench.NewTask(context.Background(), ctx.Project, issue, bench.AddOptions{
		Base: *base, Merged: *merged, Start: *start, Check: *check, Holdout: *holdout,
		Snapshot: *snapshot, Verify: !*noVerify, Log: logf,
	}, ctx.now())
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
	fmt.Fprintf(ctx.Stdout, "  base %s → merged %s, starts at %q\n", shortSHA(t.Base), shortSHA(t.Merged), t.Start)
	fmt.Fprintf(ctx.Stdout, "  hidden: %d test files, %d Go tests; check: %s\n", len(t.Hidden), len(t.HiddenTests), orNone(t.Check))
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
	fmt.Fprintf(ctx.Stdout, "%s budget: %s\n", b.Name, budgetLine(b.Budget))
	return nil
}

func budgetLine(b bench.Budget) string {
	part := func(set bool, s string) string {
		if !set {
			return "none"
		}
		return s
	}
	return fmt.Sprintf("tokens %s, cost %s, wall %s, per task %s",
		part(b.Tokens > 0, fmt.Sprint(b.Tokens)), part(b.USD > 0, fmt.Sprintf("$%.2f", b.USD)),
		part(b.WallDur() > 0, b.Wall), b.TaskWallDur())
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
	fmt.Fprintf(ctx.Stdout, "== Benchmark %s ==\nfile: %s\nbudget: %s\nworker: %s\njudge: %s\n\nTasks:\n", b.Name, b.Path(), budgetLine(b.Budget), orDefault(b.Worker, bench.DefaultWorker), orNone(b.Judge))
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
	workflow := fs.String("workflow", "", "a workflow file to use in every sandbox instead of the project's")
	label := fs.String("label", "", "a name for the setup, shown in reports and the trend")
	resume := fs.String("resume", "", "continue a paused or budget-stopped run")
	keep := fs.Bool("keep", false, "leave each task's sandbox in place")
	quote := fs.String("quote", "", "the owner's words asking for this run (required in an agent session)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli bench run <benchmark> [--setup <ref>] [--workflow <file>] [--label <name>] [--resume <run>]"}
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
	if *workflow != "" {
		if abs, err := filepath.Abs(*workflow); err == nil {
			*workflow = abs
		}
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
		Setup:    bench.Setup{Ref: ref, IssueCLI: cli, IssueCLIVersion: cliVersion(cli), Workflow: *workflow, Label: *label},
		Workflow: *workflow, Resume: *resume, StartedBy: startedBy, Quote: q,
	}
	fmt.Fprintf(ctx.Stderr, "bench run %s: %d tasks, budget %s\n", b.Name, len(b.Tasks), budgetLine(b.Budget))
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
	if r.Setup.Label != "" {
		setup = r.Setup.Label + " (" + setup + ")"
	}
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
		line := fmt.Sprintf("  %-8s %s  hidden %d/%d · check %s · judge %s · %s tokens · $%.2f · %s",
			t.Outcome, t.Task, t.HiddenPassed, t.HiddenTotal, okWord(t.CheckOK), judge, tokens, t.Usage.USD, (time.Duration(t.WallSec) * time.Second).String())
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
	pos, err := parsePositionals(ctx, "bench compare", args, 2)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return &usageError{msg: "usage: issue-cli bench compare <benchmark>/<runA> <benchmark>/<runB>"}
	}
	a, err := bench.FindRun("", pos[0])
	if err != nil {
		return err
	}
	b, err := bench.FindRun("", pos[1])
	if err != nil {
		return err
	}
	c := bench.Compare(a, b)
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

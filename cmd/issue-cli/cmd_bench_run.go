package main

import (
	"context"

	"fmt"
	"io"

	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"

	"strings"
	"syscall"

	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/routing"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/memguard"
	"github.com/kacperwerema/dark-factory/internal/spend"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func runBenchRun(ctx *Context, args []string) error { return runBenchRunWith(ctx, args, 0) }

// runBenchRunWith is bench run; memBelow > 0 also holds each task while
// memory is at or over that percent (the nightly loop's 50% line).
func runBenchRunWith(ctx *Context, args []string, memBelow int) error {
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
	agentsFlag := fs.String("agents", "", "run every task on each of these agent CLIs, e.g. claude,codex (workers: in the benchmark file names their commands)")
	modelFlag := fs.String("model", "", "start every worker on this model (--model), e.g. claude-sonnet-5-5; compare with a run without it: bench compare <runA> <runB>")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli bench run <benchmark> [--setup <ref>] [--workflow <file>] [--label <name>] [--worker <cmd> | --agents claude,codex] [--model <name>] [--resume <run>]"}
	}
	who := agentSession(ctx)
	q := strings.TrimSpace(normalizeEscapedText(*quote))
	if who != "" && q == "" {
		guardNever(ctx, "bench run")
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
	agents, err := benchAgents(b, *agentsFlag, *workerFlag, *resume)
	if err != nil {
		return err
	}
	model, err := benchModel(b, agents, *modelFlag, *resume)
	if err != nil {
		return err
	}
	workflow, byBox, err := parseWorkflowFlags(workflows)
	if err != nil {
		return err
	}
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tmp := filepath.Join(os.TempDir(), "awe-bench")
	cli, ref, sha, err := benchCLIFor(runCtx, *setup, *engine, tmp, ctx.Stderr)
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
		MemGuard: memGuardBelow(memGuard(ctx.Stderr), memBelow), MemWait: 30 * time.Minute,
		Log: ctx.Stderr, Now: ctx.now,
	}
	startedBy := "owner"
	if who != "" {
		startedBy = who
	}
	opt := bench.Options{
		Setup:    bench.Setup{Ref: ref, SHA: sha, IssueCLI: cli, IssueCLIVersion: cliVersion(cli), Workflow: strings.Join(workflows, ","), Label: *label, Agent: bench.WorkerAgent(orDefault(b.Worker, bench.DefaultWorker))},
		Workflow: workflow, Workflows: byBox, Resume: *resume, StartedBy: startedBy, Quote: q,
	}
	opt.Setup.Model = model
	jobs := len(b.Tasks)
	switch len(agents) {
	case 0:
		fmt.Fprintf(ctx.Stderr, "bench run %s: %d tasks%s, budget %s\n", b.Name, len(b.Tasks), modelWords(model), budgetLine(b, jobs))
	case 1:
		opt.Setup.Agent = agents[0]
		fmt.Fprintf(ctx.Stderr, "bench run %s: %d tasks on %s%s, budget %s\n", b.Name, len(b.Tasks), agents[0], modelWords(model), budgetLine(b, jobs))
	default:
		jobs *= len(agents)
		opt.Setup.Agent, opt.Agents = strings.Join(agents, ","), agents
		fmt.Fprintf(ctx.Stderr, "bench run %s: %d tasks on %s (%d in all), budget %s\n", b.Name, len(b.Tasks), opt.Setup.Agent, jobs, budgetLine(b, jobs))
	}
	fmt.Fprintf(ctx.Stderr, "estimate: %s\n", bench.Estimate(b, max(len(agents), 1)))
	fmt.Fprintln(ctx.Stderr, bench.SmokeLine(smokeVersion(opt.Setup.IssueCLIVersion)))
	// One model, one cost: the prices the rooms' spend reads, the benchmark
	// files' included.
	b.Prices = spend.LoadPrices()
	run, err := benchExecute(runCtx, b, env, opt)
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
	printByAgent(ctx, bench.ByAgent(run))
	prev := previousRun(run)
	fmt.Fprintf(ctx.Stdout, "\nNext:\n  issue-cli bench report %s/%s\n", run.Benchmark, run.ID)
	if len(run.Setup.Agents()) > 1 {
		fmt.Fprintf(ctx.Stdout, "  issue-cli bench compare %s/%s   by agent and type, and the routing: rules\n", run.Benchmark, run.ID)
	} else if run.Setup.Model != "" {
		other := "<a run without --model>"
		if base := previousRunOnOtherModel(run); base != nil {
			other = base.Benchmark + "/" + base.ID
		}
		fmt.Fprintf(ctx.Stdout, "  issue-cli bench compare %s %s/%s   by model and type, side by side\n", other, run.Benchmark, run.ID)
	} else if prev != nil {
		fmt.Fprintf(ctx.Stdout, "  issue-cli bench compare %s/%s %s/%s\n", prev.Benchmark, prev.ID, run.Benchmark, run.ID)
	}
	if run.Stop == bench.StopPaused || run.Stop == bench.StopBudget {
		fmt.Fprintf(ctx.Stdout, "  issue-cli bench run %s --resume %s   continue it\n", run.Benchmark, run.ID)
	}
	return nil
}

// benchModel reads --model for a run of b on agents: a plain model name,
// for one agent only (a model name is one CLI's). It sets b's worker to
// start on it. A resumed run keeps its own model.
func benchModel(b *bench.Benchmark, agents []string, flag, resume string) (string, error) {
	model := strings.TrimSpace(flag)
	if resume != "" {
		if model != "" {
			return "", &usageError{msg: "bench run: a resumed run keeps its model: drop --model"}
		}
		prev, err := bench.LoadRun(b.Name, resume)
		if err != nil {
			return "", fmt.Errorf("resume %s: %w", resume, err)
		}
		model = prev.Setup.Model
	}
	if model == "" {
		return "", nil
	}
	if !routing.ValidModel(model) {
		return "", &usageError{msg: fmt.Sprintf("bench run: --model %q is not a model name (e.g. claude-sonnet-5-5)", model)}
	}
	if len(agents) > 1 {
		return "", &usageError{msg: "bench run: --model names one CLI's model: run each agent on its own (--agents claude --model ...), then bench compare the runs"}
	}
	worker := strings.TrimSpace(b.Worker)
	if worker == "" {
		worker = bench.DefaultWorker
	}
	b.Worker = bench.WithModel(worker, model)
	return model, nil
}

// modelWords is " on model <m>" for a run on a chosen model.
func modelWords(model string) string {
	if model == "" {
		return ""
	}
	return " on model " + model
}

// previousRunOnOtherModel is the newest earlier run of run's benchmark on
// another model (or the CLI's own), the one to put beside it; nil if none.
func previousRunOnOtherModel(run *bench.Run) *bench.Run {
	var prev *bench.Run
	for _, r := range bench.Runs(run.Benchmark) {
		if r.ID == run.ID {
			break
		}
		if r.Finished() && r.Setup.Model != run.Setup.Model {
			prev = r
		}
	}
	return prev
}

// benchAgents reads --agents for a run of b. One agent becomes b's worker for
// the run. Each worker's command must be on PATH, so a typo or an agent not
// installed stops the run before anything is spent. A resumed run keeps its
// own agents, checked the same way.
func benchAgents(b *bench.Benchmark, flag, worker, resume string) ([]string, error) {
	var agents []string
	seen := map[string]bool{}
	for _, a := range strings.Split(flag, ",") {
		if a = strings.TrimSpace(a); a != "" && !seen[a] {
			seen[a] = true
			agents = append(agents, a)
		}
	}
	if len(agents) > 0 && strings.TrimSpace(worker) != "" {
		return nil, &usageError{msg: "bench run: --agents or --worker, not both (workers: in the benchmark file names an agent's command)"}
	}
	check := agents
	if resume != "" {
		if len(agents) > 0 {
			return nil, &usageError{msg: "bench run: a resumed run keeps its agents: drop --agents"}
		}
		prev, err := bench.LoadRun(b.Name, resume)
		if err != nil {
			return nil, fmt.Errorf("resume %s: %w", resume, err)
		}
		if check = prev.Setup.Agents(); len(check) < 2 {
			return nil, nil
		}
	}
	for _, a := range check {
		w, err := b.WorkerFor(a)
		if err != nil {
			return nil, err
		}
		if f := strings.Fields(w); len(f) == 0 {
			return nil, fmt.Errorf("the worker for %s is empty", a)
		} else if _, err := exec.LookPath(f[0]); err != nil {
			return nil, fmt.Errorf("the worker for %s needs %s, which is not on PATH: nothing ran", a, f[0])
		}
	}
	if len(agents) == 1 {
		w, _ := b.WorkerFor(agents[0])
		b.Worker = w
	}
	return check, nil
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
// from ref in a throwaway clone of the engine. resolved is ref as a commit
// ("" for this binary); sha is the engine commit the issue-cli is built
// from: resolved, or this binary's own revision.
func benchIssueCLI(ctx context.Context, ref, engine, tmp string, log io.Writer) (cli, resolved, sha string, err error) {
	if ref == "" {
		self, err := os.Executable()
		if err != nil {
			return "", "", "", err
		}
		return self, "", selfRevision(), nil
	}
	if engine == "" {
		engine, _ = os.Getwd()
	}
	out, err := exec.CommandContext(ctx, "git", "-C", engine, "rev-parse", "--verify", ref+"^{commit}").Output()
	if err != nil {
		return "", "", "", fmt.Errorf("--setup %s: not a commit in %s", ref, engine)
	}
	resolved = strings.TrimSpace(string(out))
	dir := filepath.Join(tmp, "setup-"+gitdir.ShortSHA(resolved))
	cli = filepath.Join(dir, "bin", "issue-cli")
	if _, err := os.Stat(cli); err == nil {
		return cli, resolved, resolved, nil
	}
	fmt.Fprintf(log, "building issue-cli at %s…\n", gitdir.ShortSHA(resolved))
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
			return "", "", "", fmt.Errorf("--setup %s: %s: %v\n%s", ref, strings.Join(s, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return cli, resolved, resolved, nil
}

// selfRevision is the engine commit this binary was built from (go build's
// vcs.revision; "" when the build carries none, as under go test).
func selfRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, kv := range info.Settings {
		if kv.Key == "vcs.revision" {
			return kv.Value
		}
	}
	return ""
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
	if found, _, _ := memguard.Check(); !found {
		fmt.Fprintln(log, "warning: mem-guard is not on PATH; tasks start without a memory check")
		return nil
	}
	return func() (bool, string) {
		_, blocked, msg := memguard.Check()
		return blocked, msg
	}
}

// memGuardBelow adds a stricter line to guard: blocked while memory is at
// or over below percent (mem-guard's reading). below 0: guard unchanged.
func memGuardBelow(guard func() (bool, string), below int) func() (bool, string) {
	if below <= 0 {
		return guard
	}
	return func() (bool, string) {
		if guard != nil {
			if blocked, msg := guard(); blocked {
				return true, msg
			}
		}
		if m := autostart.ReadMemory(); m.Found && m.Used >= below {
			return true, fmt.Sprintf("BLOCKED: memory %d%% ≥ %d%% (the nightly line)", m.Used, below)
		}
		return false, ""
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
		setup = "issue-cli at " + gitdir.ShortSHA(r.Setup.Ref)
	}
	if r.Setup.Workflow != "" {
		setup += ", workflow " + r.Setup.Workflow
	}
	if r.Setup.Agent != "" {
		setup += ", agent " + r.Setup.Agent
	}
	if r.Setup.Model != "" {
		setup += ", model " + r.Setup.Model
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
		cost := fmt.Sprintf("$%.2f", t.Usage.USD)
		if t.Usage.Known && !t.Usage.USDKnown {
			cost = "cost unknown"
		}
		line := fmt.Sprintf("  %-8s %s  %s · %s tokens · %s · %s",
			t.Outcome, t.Key(), scored, tokens, cost, wall)
		if t.Reason != "" && t.Outcome != bench.OutcomePass {
			line += " — " + t.Reason
		}
		fmt.Fprintln(w, line)
		if len(t.OutsideTouched) > 0 {
			fmt.Fprintf(w, "           ⚠ wrote outside the sandbox: %s\n", strings.Join(t.OutsideTouched, ", "))
		}
		if t.Memory != nil {
			fmt.Fprintf(w, "           %s\n", t.Memory.Line())
		}
		if len(t.Stages) > 0 {
			fmt.Fprintf(w, "           stages: %s\n", bench.StagesText(t))
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

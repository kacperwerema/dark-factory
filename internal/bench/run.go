package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/trace"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"slices"

	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clilog"
	"github.com/kacperwerema/dark-factory/internal/memory"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// BenchNote is appended to every worker prompt, the same for every setup so
// it never changes a comparison: a run has nobody to answer.
const BenchNote = "\n\n## Benchmark run\n\nThis is a benchmark run in a sandbox: no human will answer in this session. When the workflow says to ask or wait for the owner, record your choice as provisional (issue-cli decide) and continue. Stop when the issue reaches done.\n"

// Env is what a run needs from the machine; tests replace every part.
type Env struct {
	// Projects resolves a task's project slug.
	Projects func(slug string) *tracker.Project
	// IssueCLI is the issue-cli binary under test.
	IssueCLI string
	// Home is the owner's home: where a sandbox HOME's login links point,
	// and where the transcripts are read under worker_home: owner ("" skips
	// them there and links nothing).
	Home string
	// TmpRoot is where sandboxes go (outside any .worktrees/).
	TmpRoot string
	// MemGuard reports whether memory is too high to start a task; nil:
	// never blocked.
	MemGuard func() (blocked bool, msg string)
	// MemWait is how long a task waits for memory before the run pauses;
	// MemPoll how often it looks again.
	MemWait, MemPoll time.Duration
	// Poll is how often a running worker's transcript is read for the
	// budget; 0 is 30s.
	Poll time.Duration
	Now  func() time.Time
	Log  io.Writer
	// Keep leaves each task's sandbox in place.
	Keep bool
	// MachineMemory is the machine memory log the sandbox's is cut from;
	// "" is memory.MachinePath().
	MachineMemory string
}

// Options are one run's choices.
type Options struct {
	Setup Setup
	// Workflow replaces Product's workflow file in the sandbox of every
	// product task; Workflows replaces a box's (product or a department's
	// key) for its tasks, and wins over Workflow.
	Workflow  string
	Workflows map[string]string
	// Agents are the agent CLIs to run every task on (bench run --agents):
	// with two or more, one record per task and agent. Empty or one: the
	// benchmark's worker as it is. A resumed run keeps its own.
	Agents []string
	// Resume continues run id: tasks with a final outcome are kept.
	Resume string
	// StartedBy and Quote go into the run (Run.StartedBy, Run.Quote).
	StartedBy, Quote string
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Env) logf(format string, args ...any) {
	if e.Log != nil {
		fmt.Fprintf(e.Log, format+"\n", args...)
	}
}

// NewRunID is a run id from its start time.
func NewRunID(at time.Time) string { return at.UTC().Format("20060102-150405") }

// job is one task on one worker; agent is set in a run on two or more.
type job struct {
	task          Task
	agent, worker string
}

// jobsFor is every task on every agent, the tasks in order; with under two
// agents, every task on the benchmark's worker.
func jobsFor(b *Benchmark, agents []string) ([]job, error) {
	var out []job
	if len(agents) < 2 {
		worker := b.Worker
		if worker == "" {
			worker = DefaultWorker
		}
		for _, t := range b.Tasks {
			out = append(out, job{task: t, worker: worker})
		}
		return out, nil
	}
	for _, t := range b.Tasks {
		for _, a := range agents {
			w, err := b.WorkerFor(a)
			if err != nil {
				return nil, err
			}
			out = append(out, job{task: t, agent: a, worker: w})
		}
	}
	return out, nil
}

// Execute runs every task of b in order, on each of opt.Agents, and saves
// the run after each task.
func Execute(ctx context.Context, b *Benchmark, env Env, opt Options) (*Run, error) {
	if err := validWorkerHome(b.WorkerHome); err != nil {
		return nil, err
	}
	run := &Run{Benchmark: b.Name, Setup: opt.Setup, Budget: b.Budget, StartedBy: opt.StartedBy, Quote: opt.Quote}
	run.Setup.WorkerHome = b.HomeMode()
	var records []TaskRecord
	if opt.Resume != "" {
		prev, err := LoadRun(b.Name, opt.Resume)
		if err != nil {
			return nil, fmt.Errorf("resume %s: %w", opt.Resume, err)
		}
		all, err := LoadRecords(b.Name, opt.Resume)
		if err != nil {
			return nil, err
		}
		run = prev
		run.Ended, run.Stop, run.StopNote = time.Time{}, "", ""
		opt.Agents = prev.Setup.Agents()
		for _, r := range all {
			if r.Outcome != OutcomeBudget {
				records = append(records, r)
			}
		}
	} else {
		run.Started = env.now()
		run.ID = NewRunID(run.Started)
	}
	if env.Poll <= 0 {
		env.Poll = 30 * time.Second
	}
	jobs, err := jobsFor(b, opt.Agents)
	if err != nil {
		return nil, err
	}
	env.logf("%s", HomeNote(b.HomeMode(), env.Home))
	if env.MemPoll <= 0 {
		env.MemPoll = time.Minute
	}
	done := map[string]bool{}
	for _, r := range records {
		done[r.Key()] = true
	}
	recount := func() {
		run.SpentTokens, run.SpentUSD = 0, 0
		for _, r := range records {
			run.SpentTokens += r.Usage.Tokens()
			run.SpentUSD += r.Usage.USD
		}
	}
	recount()
	finish := func(stop, note string) (*Run, error) {
		run.Stop, run.StopNote, run.Ended = stop, note, env.now()
		return run, Save(run, records)
	}
	for i, j := range jobs {
		t := j.task
		if done[JobKey(t.ID(), j.agent)] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return finish(StopError, "interrupted")
		}
		if over := overBudget(b.Budget, len(jobs), run, env.now(), 0, 0); over != "" {
			for _, rest := range jobs[i:] {
				rt := rest.task
				if !done[JobKey(rt.ID(), rest.agent)] {
					records = append(records, TaskRecord{Task: rt.ID(), Project: rt.Project, Type: rt.Type, Holdout: rt.Holdout, Agent: rest.agent, Outcome: OutcomeBudget, Reason: over, Judge: -1})
				}
			}
			return finish(StopBudget, over)
		}
		if paused := waitMemory(ctx, &env); paused != "" {
			run.Stop, run.StopNote, run.Ended = StopPaused, paused, env.now()
			return run, Save(run, records)
		}
		label := t.ID()
		if t.Holdout {
			label = fmt.Sprintf("holdout task %d", i+1)
		}
		if j.agent != "" {
			label += " on " + j.agent
		}
		env.logf("▶ %d/%d %s", i+1, len(jobs), label)
		rec := runTask(ctx, b, j, len(jobs), i, run, &env, opt)
		records = append(records, rec)
		recount()
		if t.Holdout {
			env.logf("  done (holdout)")
		} else {
			env.logf("  %s %s · %s · %s", rec.Outcome, rec.Reason, tokensText(rec.Usage), wallText(rec.WallSec))
		}
		if err := Save(run, records); err != nil {
			return run, err
		}
	}
	return finish(StopDone, "")
}

// overBudget names the cap a run has spent, counting extra tokens and usd
// a running task used so far; "" while within budget. With no tokens set
// the token cap is DefaultTaskTokens for each of the run's tasks.
func overBudget(b Budget, tasks int, run *Run, now time.Time, tokens int64, usd float64) string {
	switch limit := b.TokenCap(tasks); {
	case run.SpentTokens+tokens >= limit:
		return fmt.Sprintf("token budget spent (%d of %d)", run.SpentTokens+tokens, limit)
	case b.USD > 0 && run.SpentUSD+usd >= b.USD:
		return fmt.Sprintf("cost budget spent ($%.2f of $%.2f)", run.SpentUSD+usd, b.USD)
	case b.WallDur() > 0 && now.Sub(run.Started) >= b.WallDur():
		return fmt.Sprintf("wall-time budget spent (%s)", b.WallDur())
	}
	return ""
}

// waitMemory waits while mem-guard says BLOCKED, up to env.MemWait; the
// note when the run must pause instead.
func waitMemory(ctx context.Context, env *Env) string {
	if env.MemGuard == nil {
		return ""
	}
	deadline := env.now().Add(env.MemWait)
	for {
		blocked, msg := env.MemGuard()
		if !blocked {
			return ""
		}
		if !env.now().Before(deadline) {
			return "memory stayed blocked: " + msg
		}
		env.logf("  waiting for memory: %s", msg)
		select {
		case <-ctx.Done():
			return "interrupted while waiting for memory"
		case <-time.After(env.MemPoll):
		}
	}
}

// sandbox is one task's throwaway project.
type sandbox struct {
	dir, repo, tracker, config, state, issue, work, slug string
	// home is the worker's HOME under worker_home: sandbox, "" when it
	// keeps the owner's.
	home string
	// memory is what the sandbox's memory stores hold (cutMemory).
	memory *MemoryCut
	proj   *tracker.Project
	wf     *tracker.WorkflowConfig
	// status, refs and memories are the issue's status when the worker got
	// its prompt and the decision refs and memory ids the prompt carried.
	status         string
	refs, memories []string
}

func runTask(ctx context.Context, b *Benchmark, j job, jobs, n int, run *Run, env *Env, opt Options) TaskRecord {
	t := j.task
	// cli is the agent CLI the worker runs, whose usage to read.
	cli := j.agent
	if cli == "" {
		if a := run.Setup.Agents(); len(a) == 1 {
			cli = a[0]
		} else {
			cli = WorkerAgent(j.worker)
		}
	}
	rec := TaskRecord{Task: t.ID(), Project: t.Project, Type: t.Type, Holdout: t.Holdout, Agent: j.agent, Judge: -1, EvalScore: -1, Workflow: t.WorkflowKey(), Started: env.now()}
	fail := func(outcome, reason string) TaskRecord {
		rec.Outcome, rec.Reason = outcome, reason
		rec.WallSec = env.now().Sub(rec.Started).Seconds()
		return rec
	}
	p := env.Projects(t.Project)
	if p == nil {
		return fail(OutcomeError, "no project "+t.Project)
	}
	sb, err := newSandbox(env, run.ID, n, t, p, opt, b.HomeMode())
	if !env.Keep && sb != nil {
		defer os.RemoveAll(sb.dir)
	}
	if sb != nil {
		rec.Memory = sb.memory
	}
	if err != nil {
		return fail(OutcomeError, "sandbox: "+err.Error())
	}
	ev, err := TaskEval(t, sb.wf)
	if err != nil {
		return fail(OutcomeError, err.Error())
	}
	graded := ev != nil && ev.Graded()
	if ev != nil {
		rec.Eval = ev.ID
		if graded && len(ev.Assert) == 0 && (len(ev.Rubric) == 0 || strings.TrimSpace(b.Grader) == "") {
			return fail(OutcomeError, "eval "+ev.ID+": "+ErrNothingToScore.Error())
		}
	}
	if t.Check != "" {
		if ok, out := shell(ctx, sb.work, t.Check, nil, 30*time.Minute); !ok {
			return fail(OutcomeBroken, "the check fails at base: "+lastLine(out))
		}
	}
	prompt, err := sb.prompt(ctx, env)
	if err != nil {
		return fail(OutcomeError, "prompt: "+err.Error())
	}

	// The worker, stopped at the task's wall cap, the run's wall cap, or
	// when the transcript shows the run's token or cost budget spent.
	wall := b.Budget.TaskWallDur()
	if left := b.Budget.WallDur() - env.now().Sub(run.Started); b.Budget.WallDur() > 0 && left < wall {
		wall = max(left, time.Second)
	}
	wctx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()
	var budgetHit string
	var mu sync.Mutex
	stopWatch := make(chan struct{})
	go func() {
		tick := time.NewTicker(env.Poll)
		defer tick.Stop()
		for {
			select {
			case <-stopWatch:
				return
			case <-tick.C:
				tr := sb.transcript(env, cli, rec.Started)
				Cost(&tr.Usage, b.Prices)
				if over := overBudget(b.Budget, jobs, run, env.now(), tr.Usage.Tokens(), tr.Usage.USD); over != "" {
					mu.Lock()
					budgetHit = over
					mu.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	stdout, stderr, werr := runWorker(wctx, sb.work, j.worker, prompt, sb.env(env))
	close(stopWatch)
	rec.WallSec = env.now().Sub(rec.Started).Seconds()

	tr := sb.transcript(env, cli, rec.Started)
	// A Codex stdout is no Claude Code result: a --json stream's last line
	// would read as one, with the cached input counted twice.
	if u, ok := ParseResult(stdout); ok && cli != AgentCodex {
		u.ToolVersion = tr.Usage.ToolVersion
		if u.Model == "" {
			u.Model = tr.Usage.Model
		}
		rec.Usage = u
	} else {
		rec.Usage = tr.Usage
	}
	Cost(&rec.Usage, b.Prices)
	rec.OutsideTouched = tr.Outside
	sb.clilogStats(&rec)
	rec.Trace = sb.trace(rec, tr, cli, prompt)

	mu.Lock()
	hit := budgetHit
	mu.Unlock()
	switch {
	case hit != "":
		rec.Outcome, rec.Reason = OutcomeBudget, hit
	case errors.Is(wctx.Err(), context.DeadlineExceeded):
		rec.Outcome, rec.Reason = OutcomeTimeout, "stopped at "+wall.String()
	case ctx.Err() != nil:
		rec.Outcome, rec.Reason = OutcomeError, "interrupted"
		return rec
	case werr != nil && sb.home != "" && saysNoLogin(stdout, stderr):
		rec.Outcome, rec.Reason = OutcomeError, NoLoginReason
	case werr != nil && !rec.Usage.Known && (j.agent != "" || cli == AgentCodex):
		// It never got to work: no verdict on the agent, on the numbers by
		// agent least of all.
		rec.Outcome, rec.Reason = OutcomeError, "worker stopped before using a token: "+lastLine(string(stderr))
	}

	rec.DiffLines = diffLines(sb.work, t.Base)
	workerDiff, _ := git(sb.work, "diff", t.Base)
	score(ctx, sb, t, &rec)
	if ev != nil {
		for _, c := range ev.Checks {
			if ok, _ := shell(ctx, sb.work, c, nil, 30*time.Minute); !ok {
				rec.CheckOK = false
			}
		}
	}
	if graded {
		if rec.Outcome == "" {
			scoreEval(ctx, b, t, ev, sb, workerDiff, &rec)
		}
	} else if rec.Outcome == "" {
		switch {
		case werr != nil && rec.HiddenTotal == 0 && t.Check == "":
			rec.Outcome, rec.Reason = OutcomeError, "worker: "+werr.Error()
		case rec.HiddenPassed == rec.HiddenTotal && rec.CheckOK:
			rec.Outcome = OutcomePass
		default:
			rec.Outcome = OutcomeFail
			rec.Reason = fmt.Sprintf("hidden tests %d/%d, check %s", rec.HiddenPassed, rec.HiddenTotal, okText(rec.CheckOK))
		}
	}
	if b.Judge != "" {
		judge(ctx, b, t, sb, workerDiff, &rec)
	}
	if t.Stages != nil && ctx.Err() == nil {
		brief, _ := os.ReadFile(SnapshotPath(t))
		scoreStages(ctx, stageInput{task: t, wf: sb.wf, ev: ev, b: b, dir: sb.dir, env: sb.scorerEnv(),
			brief: string(brief), design: workerDesign(sb.proj.IssueDir), docs: workerDocs(sb.work, t.Base)}, &rec)
	}
	return rec
}

func newSandbox(env *Env, runID string, n int, t Task, p *tracker.Project, opt Options, homeMode string) (*sandbox, error) {
	root := env.TmpRoot
	if root == "" {
		root = filepath.Join(os.TempDir(), "awe-bench")
	}
	sb := &sandbox{dir: filepath.Join(root, runID, fmt.Sprintf("%02d", n+1))}
	if err := os.RemoveAll(sb.dir); err != nil {
		return nil, err
	}
	sb.repo = filepath.Join(sb.dir, "repo")
	sb.tracker = filepath.Join(sb.dir, "tracker")
	sb.state = filepath.Join(sb.dir, "state")
	sb.config = filepath.Join(sb.dir, "projects.yaml")
	for _, d := range []string{sb.tracker, sb.state, sb.xdgConfig(), sb.xdgData()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return sb, err
		}
	}
	if homeMode == WorkerHomeSandbox {
		sb.home = filepath.Join(sb.dir, "home")
		if _, err := prepareWorkerHome(sb.home, env.Home); err != nil {
			return sb, fmt.Errorf("worker home: %w", err)
		}
	}
	src, err := git(p.Root(), "rev-parse", "--show-toplevel")
	if err != nil {
		return sb, err
	}
	if _, err := git(sb.dir, "clone", "--quiet", "--no-checkout", "--shared", strings.TrimSpace(src), sb.repo); err != nil {
		return sb, err
	}
	if _, err := git(sb.repo, "remote", "remove", "origin"); err != nil {
		return sb, err
	}
	if _, err := git(sb.repo, "checkout", "--quiet", "--detach", t.Base); err != nil {
		return sb, err
	}
	if err := cutRefs(sb.repo, t.Base); err != nil {
		return sb, fmt.Errorf("refs: %w", err)
	}
	for _, h := range t.Hidden {
		_ = os.Remove(filepath.Join(sb.repo, filepath.FromSlash(h)))
	}
	sb.work = sb.repo

	// The scratch tracker: the issue snapshot and the workflow under test.
	snap, err := os.ReadFile(SnapshotPath(t))
	if err != nil {
		return sb, fmt.Errorf("issue snapshot: %w", err)
	}
	sb.issue = filepath.Join(sb.tracker, "issues", filepath.FromSlash(t.Slug)+".md")
	if err := os.MkdirAll(filepath.Dir(sb.issue), 0o755); err != nil {
		return sb, err
	}
	if err := os.WriteFile(sb.issue, snap, 0o644); err != nil {
		return sb, err
	}
	machineLog := env.MachineMemory
	if machineLog == "" {
		machineLog = memory.MachinePath()
	}
	if sb.memory, err = sb.cutMemory(p, t, machineLog); err != nil {
		return sb, fmt.Errorf("memory: %w", err)
	}
	key := t.WorkflowKey()
	wfSrc := opt.Workflows[key]
	if wfSrc == "" && key == ProductWorkflow {
		wfSrc = opt.Workflow
	}
	if wfSrc == "" {
		if wfSrc, err = DepartmentWorkflowFile(p, key); err != nil {
			return sb, err
		}
	}
	wfPath := filepath.Join(sb.tracker, "workflow.yaml")
	if wfSrc != "" {
		data, err := os.ReadFile(wfSrc)
		if err != nil {
			return sb, fmt.Errorf("workflow: %w", err)
		}
		if err := os.WriteFile(wfPath, data, 0o644); err != nil {
			return sb, err
		}
	}
	cfg := fmt.Sprintf("projects:\n  - name: %q\n    slug: %q\n    issues: %q\n    workflow: %q\n    docs: %q\n    workdir: %q\n    terminal: \"none\"\n",
		p.Name, p.Slug, filepath.Join(sb.tracker, "issues"), wfPath, filepath.Join(sb.repo, "docs"), sb.repo)
	if err := os.WriteFile(sb.config, []byte(cfg), 0o644); err != nil {
		return sb, err
	}
	sb.proj = &tracker.Project{Name: p.Name, Slug: p.Slug, IssueDir: filepath.Join(sb.tracker, "issues"), WorkflowFile: wfPath, WorkDir: sb.repo, Terminal: "none"}
	sb.wf = sb.proj.LoadWorkflow()
	// Gates pass by themselves: YOLO on in the sandbox's own state file.
	err = yolo.TurnOn(sb.repo, p.Slug, yolo.State{Forever: true, By: "bench", Words: "benchmark run", How: "bench"}, env.now())
	return sb, err
}

// env is the worker's environment: the sandbox's config and state, the
// issue-cli under test first on PATH, git's identity and no global config,
// and nothing that points at the owner's tmux, leads or real tracker (tmux
// gets the sandbox as its socket folder, so no live session is seen).
func (sb *sandbox) env(env *Env) []string {
	out := isolatedEnviron()
	if sb.home != "" {
		// Its own HOME and agent config dirs (<home>/.claude, <home>/.codex),
		// the caches kept where they are.
		out = slices.DeleteFunc(out, func(kv string) bool {
			k, _, _ := strings.Cut(kv, "=")
			return k == "HOME" || k == "CLAUDE_CONFIG_DIR" || k == "CODEX_HOME"
		})
		out = append(out, "HOME="+sb.home)
		out = append(out, cacheEnv(env.Home)...)
	}
	path := os.Getenv("PATH")
	if env.IssueCLI != "" {
		path = filepath.Dir(env.IssueCLI) + string(os.PathListSeparator) + path
	}
	out = append(out, "PATH="+path, "ISSUE_VIEWER_CONFIG="+sb.config, "XDG_STATE_HOME="+sb.state, "XDG_CONFIG_HOME="+sb.xdgConfig(), "XDG_DATA_HOME="+sb.xdgData(),
		"TMUX_TMPDIR="+sb.state, "ISSUE_VIEWER_ISSUE_SLUG="+sb.slug, "AGENT_NAME="+tracker.AgentSessionName(sb.slug))
	// The folders a Codex worker's sandbox opens (DefaultCodexWorker).
	dir := sb.dir
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	out = append(out, "BENCH_SANDBOX="+dir, "BENCH_GOCACHE="+goCache(dir))
	return gitEnv(out)
}

// goCache is the Go build cache the worker's go uses, or dir without one.
func goCache(dir string) string {
	if c := os.Getenv("GOCACHE"); c != "" {
		return c
	}
	if c, err := os.UserCacheDir(); err == nil {
		return filepath.Join(c, "go-build")
	}
	return dir
}

// isolatedEnviron is this process's environment without what points at the
// owner's sessions, state, config or benchmark (the holdout lives there):
// every child of a run starts from it.
func isolatedEnviron() []string {
	drop := []string{"TMUX", "TMUX_PANE", "TMUX_TMPDIR", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "ISSUE_VIEWER_LEAD_SCOPE", "ISSUE_VIEWER_ISSUE_SLUG", "ISSUE_VIEWER_CONFIG", "ISSUE_VIEWER_BENCH_DIR", "AGENT_NAME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "PATH"}
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(drop, k) && !strings.HasPrefix(k, "ISSUE_CLI_") {
			out = append(out, kv)
		}
	}
	return out
}

// xdgConfig is the sandbox's XDG_CONFIG_HOME: no owner's projects.yaml,
// prices or benchmark is visible.
func (sb *sandbox) xdgConfig() string { return filepath.Join(sb.dir, "config") }

// scorerEnv is the scorer's environment: isolated like the worker's, and
// HOME in the sandbox too (promptfoo needs no login), with npm's cache kept
// where it is so npx downloads the pinned promptfoo once, not per task.
func (sb *sandbox) scorerEnv() []string {
	cache := os.Getenv("npm_config_cache")
	if cache == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cache = filepath.Join(home, ".npm")
		}
	}
	var out []string
	for _, kv := range isolatedEnviron() {
		if k, _, _ := strings.Cut(kv, "="); k != "HOME" && k != "npm_config_cache" {
			out = append(out, kv)
		}
	}
	home := filepath.Join(sb.dir, "eval", "home")
	_ = os.MkdirAll(home, 0o755)
	out = append(out, "PATH="+os.Getenv("PATH"), "HOME="+home, "XDG_STATE_HOME="+sb.state, "XDG_CONFIG_HOME="+sb.xdgConfig(), "XDG_DATA_HOME="+sb.xdgData(), "TMUX_TMPDIR="+sb.state)
	if cache != "" {
		out = append(out, "npm_config_cache="+cache)
	}
	return gitEnv(out)
}

// prompt is the issue-cli under test's dispatch prompt for the snapshot,
// with its worktree made when the workflow asks for one, plus BenchNote.
func (sb *sandbox) prompt(ctx context.Context, env *Env) (string, error) {
	cli := env.IssueCLI
	if cli == "" {
		cli = "issue-cli"
	}
	issues, err := tracker.LoadIssues(sb.proj.IssueDir)
	if err != nil || len(issues) != 1 {
		return "", fmt.Errorf("the snapshot does not load as one issue: %v", err)
	}
	slug := issues[0].Slug
	sb.slug, sb.status = slug, issues[0].Status
	cmd := exec.CommandContext(ctx, cli, "--config", sb.config, "--project", sb.proj.Slug, "dispatch-prompt", slug, "--json")
	cmd.Dir = sb.repo
	cmd.Env = sb.env(env)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s dispatch-prompt: %v %s", cli, err, strings.TrimSpace(errb.String()))
	}
	var dp struct {
		Prompt       string   `json:"prompt"`
		Worktree     string   `json:"worktree"`
		Branch       string   `json:"branch"`
		DecisionRefs []string `json:"decision_refs"`
		MemoryIDs    []string `json:"memory_ids"`
	}
	if err := json.Unmarshal(out.Bytes(), &dp); err != nil {
		return "", err
	}
	sb.refs, sb.memories = dp.DecisionRefs, dp.MemoryIDs
	if dp.Worktree != "" && dp.Branch != "" {
		if _, err := git(sb.repo, "worktree", "add", "--quiet", "-b", dp.Branch, dp.Worktree, "HEAD"); err != nil {
			return "", err
		}
		sb.work = dp.Worktree
	}
	return dp.Prompt + BenchNote, nil
}

// transcript reads the worker's transcripts. Claude Code names their folder
// after its working directory, which may be the resolved path (/private/var
// for /var on macOS), so both are read. A Codex worker's come from its
// rollout files instead, sessions since rec's start in the sandbox.
func (sb *sandbox) transcript(env *Env, cli string, since time.Time) Transcript {
	home := env.Home
	if sb.home != "" {
		home = sb.home
	}
	if cli == AgentCodex {
		return sb.codexTranscript(home, since)
	}
	if home == "" {
		return Transcript{}
	}
	dirs := []string{sb.work}
	if real, err := filepath.EvalSymlinks(sb.work); err == nil && real != sb.work {
		dirs = append(dirs, real)
	}
	roots := []string{sb.dir}
	if real, err := filepath.EvalSymlinks(sb.dir); err == nil {
		roots = append(roots, real)
	}
	var tr Transcript
	for _, d := range dirs {
		if got := ReadTranscripts(TranscriptDir(home, d), roots...); got.Usage.Known || len(got.Outside) > 0 {
			tr = got
		}
	}
	return tr
}

func (sb *sandbox) codexTranscript(home string, since time.Time) Transcript {
	dir := CodexSessions(home)
	if sb.home != "" {
		// Everything in its own home is this task's.
		since = time.Time{}
	} else if c := os.Getenv("CODEX_HOME"); c != "" {
		dir = filepath.Join(c, "sessions")
	} else if home == "" {
		return Transcript{}
	}
	roots := []string{sb.dir}
	if real, err := filepath.EvalSymlinks(sb.dir); err == nil {
		roots = append(roots, real)
	}
	return ReadCodexSessions(dir, since, roots...)
}

// clilogStats fills the record's issue-cli numbers from the sandbox's clilog
// and the issue's transitions from its stats sidecar.
func (sb *sandbox) clilogStats(rec *TaskRecord) {
	refusedTo := map[string]bool{}
	clilog.Read(clilog.ProjectPath(sb.repo), func(r clilog.Record) {
		rec.Calls++
		failed := r.Exit != nil && *r.Exit != 0
		if failed {
			rec.Refused++
		}
		args := clilog.StripGlobal(r.Args)
		if len(args) == 0 || args[0] != "transition" || slices.Contains(args, "--dry-run") {
			return
		}
		to := clilog.FlagValue(args, "--to")
		if failed {
			refusedTo[to] = true
			return
		}
		rec.Gates++
		if !refusedTo[to] {
			rec.FirstTryGates++
		}
		delete(refusedTo, to)
	})
	stats, _ := tracker.LoadStats(sb.issue)
	order := map[string]int{}
	if sb.wf != nil {
		for i, s := range sb.wf.Statuses {
			order[s.Name] = i
		}
	}
	for _, t := range stats.Transitions {
		rec.Statuses = append(rec.Statuses, t.To)
		if a, ok := order[t.From]; ok {
			if b, ok := order[t.To]; ok && b < a {
				rec.Backward++
			}
		}
	}
	if issues, err := tracker.LoadIssues(sb.proj.IssueDir); err == nil && len(issues) == 1 {
		rec.FinalStatus = issues[0].Status
	}
}

// trace is the task's trace (internal/trace): the sandbox issue's
// transitions and clilog, the worker's transcript messages, and the prompt
// the bench sent as the first span's briefing.
func (sb *sandbox) trace(rec TaskRecord, tr Transcript, cli, prompt string) []trace.Span {
	stats, _ := tracker.LoadStats(sb.issue)
	var calls []clilog.Record
	clilog.Read(clilog.ProjectPath(sb.repo), func(r clilog.Record) { calls = append(calls, r) })
	var statuses []string
	if sb.wf != nil {
		for _, s := range sb.wf.Statuses {
			statuses = append(statuses, s.Name)
		}
	}
	status := rec.FinalStatus
	if status == "" {
		status = sb.status
	}
	open := sb.wf == nil || !sb.wf.IsLastStatus(status)
	briefing := tracker.NewBriefing(tracker.BriefingDispatch, sb.status, "", prompt, sb.memories, sb.refs, rec.Started)
	return trace.Build(trace.In{
		Transitions: stats.Transitions,
		Briefings:   append([]tracker.Briefing{briefing}, stats.Briefings...),
		Calls:       calls,
		Messages:    tr.Messages,
		Statuses:    statuses,
		Status:      status,
		Start:       rec.Started,
		End:         rec.Started.Add(time.Duration(rec.WallSec * float64(time.Second))),
		Open:        open,
		CallsKnown:  len(calls) > 0,
		TokensKnown: len(tr.Messages) > 0,
		Agent:       cli,
	})
}

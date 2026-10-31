package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/michal-franc/issue-viewer/internal/clilog"
	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
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
}

// Options are one run's choices.
type Options struct {
	Setup Setup
	// Workflow replaces Product's workflow file in the sandbox of every
	// product task; Workflows replaces a box's (product or a department's
	// key) for its tasks, and wins over Workflow.
	Workflow  string
	Workflows map[string]string
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

// Execute runs every task of b in order and saves the run after each task.
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
	env.logf("%s", HomeNote(b.HomeMode(), env.Home))
	if env.MemPoll <= 0 {
		env.MemPoll = time.Minute
	}
	done := map[string]bool{}
	for _, r := range records {
		done[r.Task] = true
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
	for i, t := range b.Tasks {
		if done[t.ID()] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return finish(StopError, "interrupted")
		}
		if over := overBudget(b.Budget, len(b.Tasks), run, env.now(), 0, 0); over != "" {
			for _, rest := range b.Tasks[i:] {
				if !done[rest.ID()] {
					records = append(records, TaskRecord{Task: rest.ID(), Project: rest.Project, Type: rest.Type, Holdout: rest.Holdout, Outcome: OutcomeBudget, Reason: over, Judge: -1})
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
		env.logf("▶ %d/%d %s", i+1, len(b.Tasks), label)
		rec := runTask(ctx, b, t, i, run, &env, opt)
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
	proj *tracker.Project
	wf   *tracker.WorkflowConfig
}

func runTask(ctx context.Context, b *Benchmark, t Task, n int, run *Run, env *Env, opt Options) TaskRecord {
	rec := TaskRecord{Task: t.ID(), Project: t.Project, Type: t.Type, Holdout: t.Holdout, Judge: -1, EvalScore: -1, Workflow: t.WorkflowKey(), Started: env.now()}
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
				tr := sb.transcript(env)
				Cost(&tr.Usage, b.Prices)
				if over := overBudget(b.Budget, len(b.Tasks), run, env.now(), tr.Usage.Tokens(), tr.Usage.USD); over != "" {
					mu.Lock()
					budgetHit = over
					mu.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	worker := b.Worker
	if worker == "" {
		worker = DefaultWorker
	}
	stdout, stderr, werr := runWorker(wctx, sb.work, worker, prompt, sb.env(env))
	close(stopWatch)
	rec.WallSec = env.now().Sub(rec.Started).Seconds()

	tr := sb.transcript(env)
	if u, ok := ParseResult(stdout); ok {
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
	for _, d := range []string{sb.tracker, sb.state, sb.xdgConfig()} {
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
	out = append(out, "PATH="+path, "ISSUE_VIEWER_CONFIG="+sb.config, "XDG_STATE_HOME="+sb.state, "XDG_CONFIG_HOME="+sb.xdgConfig(),
		"TMUX_TMPDIR="+sb.state, "ISSUE_VIEWER_ISSUE_SLUG="+sb.slug, "AGENT_NAME="+tracker.AgentSessionName(sb.slug))
	return gitEnv(out)
}

// isolatedEnviron is this process's environment without what points at the
// owner's sessions, state, config or benchmark (the holdout lives there):
// every child of a run starts from it.
func isolatedEnviron() []string {
	drop := []string{"TMUX", "TMUX_PANE", "TMUX_TMPDIR", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "ISSUE_VIEWER_LEAD_SCOPE", "ISSUE_VIEWER_ISSUE_SLUG", "ISSUE_VIEWER_CONFIG", "ISSUE_VIEWER_BENCH_DIR", "AGENT_NAME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "PATH"}
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
	out = append(out, "PATH="+os.Getenv("PATH"), "HOME="+home, "XDG_STATE_HOME="+sb.state, "XDG_CONFIG_HOME="+sb.xdgConfig(), "TMUX_TMPDIR="+sb.state)
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
	sb.slug = slug
	cmd := exec.CommandContext(ctx, cli, "--config", sb.config, "--project", sb.proj.Slug, "dispatch-prompt", slug, "--json")
	cmd.Dir = sb.repo
	cmd.Env = sb.env(env)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s dispatch-prompt: %v %s", cli, err, strings.TrimSpace(errb.String()))
	}
	var dp struct {
		Prompt   string `json:"prompt"`
		Worktree string `json:"worktree"`
		Branch   string `json:"branch"`
	}
	if err := json.Unmarshal(out.Bytes(), &dp); err != nil {
		return "", err
	}
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
// for /var on macOS), so both are read.
func (sb *sandbox) transcript(env *Env) Transcript {
	home := env.Home
	if sb.home != "" {
		home = sb.home
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

// score puts the merged commit's hidden test files over the worker's, runs
// the hidden Go tests and the task's check.
func score(ctx context.Context, sb *sandbox, t Task, rec *TaskRecord) {
	for _, h := range t.Hidden {
		data, err := git(sb.repo, "show", t.Merged+":"+h)
		if err != nil {
			continue
		}
		dst := filepath.Join(sb.work, filepath.FromSlash(h))
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		_ = os.WriteFile(dst, []byte(data), 0o644)
	}
	rec.HiddenTotal = len(t.HiddenTests)
	if rec.HiddenTotal > 0 {
		rec.HiddenPassed = len(goHidden(ctx, sb.work, t))
	}
	rec.CheckOK = true
	if t.Check != "" {
		rec.CheckOK, _ = shell(ctx, sb.work, t.Check, nil, 30*time.Minute)
	}
}

// goHidden runs the hidden Go tests in their packages and returns the ones
// that passed; a test that never ran (the package does not build) failed.
func goHidden(ctx context.Context, dir string, t Task) map[string]bool {
	return goHiddenEnv(ctx, dir, t, nil)
}

// goHiddenEnv is goHidden with the go command's environment (nil: this
// process's).
func goHiddenEnv(ctx context.Context, dir string, t Task, env []string) map[string]bool {
	pkgs := map[string]bool{}
	for _, h := range t.Hidden {
		if strings.HasSuffix(h, "_test.go") {
			pkgs["./"+filepath.ToSlash(filepath.Dir(h))] = true
		}
	}
	var quoted []string
	for _, name := range t.HiddenTests {
		quoted = append(quoted, regexp.QuoteMeta(name))
	}
	args := []string{"test", "-json", "-count=1", "-run", "^(" + strings.Join(quoted, "|") + ")$"}
	for p := range pkgs {
		args = append(args, p)
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(tctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, _ := cmd.Output()
	want := map[string]bool{}
	for _, n := range t.HiddenTests {
		want[n] = true
	}
	passed := map[string]bool{}
	for _, l := range bytes.Split(out, []byte("\n")) {
		var ev struct{ Action, Test string }
		if json.Unmarshal(l, &ev) == nil && ev.Action == "pass" && want[ev.Test] {
			passed[ev.Test] = true
		}
	}
	return passed
}

var judgeScore = regexp.MustCompile(`(?im)^\s*SCORE:\s*([0-5])\b`)
var judgeReason = regexp.MustCompile(`(?im)^\s*REASON:\s*(.+)$`)

// JudgePrompt is what the judge reads: the issue, the known good diff and
// the worker's diff, each cut to limit bytes.
func JudgePrompt(issue, merged, worker string) string {
	cut := cutDiff
	return "You judge one benchmark task. Compare the WORKER DIFF with the KNOWN GOOD DIFF for the ISSUE: does it do what the issue asks, as well as the known good one? Different code that meets the issue is fine.\n" +
		"Answer with exactly two lines:\nSCORE: <0-5>  (5 = as good or better, 0 = nothing useful)\nREASON: <one line>\n\n" +
		"## ISSUE\n\n" + cut(issue) + "\n\n## KNOWN GOOD DIFF\n\n" + cut(merged) + "\n\n## WORKER DIFF\n\n" + cut(worker) + "\n"
}

// diffLimit caps each text the judge and a diff eval read.
const diffLimit = 60000

// cutDiff cuts s to diffLimit bytes.
func cutDiff(s string) string {
	if len(s) > diffLimit {
		return s[:diffLimit] + "\n… (cut)\n"
	}
	return s
}

func judge(ctx context.Context, b *Benchmark, t Task, sb *sandbox, workerDiff string, rec *TaskRecord) {
	issue, _ := os.ReadFile(SnapshotPath(t))
	merged, _ := git(sb.repo, "diff", t.Base, t.Merged)
	jctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, _, err := runWorker(jctx, sb.dir, b.Judge, JudgePrompt(string(issue), merged, workerDiff), gitEnv(os.Environ()))
	if err != nil && len(out) == 0 {
		rec.JudgeReason = "judge: " + err.Error()
		return
	}
	text := string(out)
	var res struct {
		Result string `json:"result"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &res) == nil && res.Result != "" {
		text = res.Result
		if u, ok := ParseResult(out); ok {
			Cost(&u, b.Prices)
			rec.Usage.Input += u.Input
			rec.Usage.Output += u.Output
			rec.Usage.CacheRead += u.CacheRead
			rec.Usage.CacheWrite += u.CacheWrite
			rec.Usage.USD += u.USD
		}
	}
	if m := judgeScore.FindStringSubmatch(text); m != nil {
		rec.Judge, _ = strconv.Atoi(m[1])
	} else {
		rec.JudgeReason = "judge gave no SCORE line"
		return
	}
	if m := judgeReason.FindStringSubmatch(text); m != nil {
		rec.JudgeReason = strings.TrimSpace(m[1])
	}
}

// runWorker runs command in dir through sh with prompt on stdin, in its own
// process group so a stop ends every process it started.
func runWorker(ctx context.Context, dir, command, prompt string, env []string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(prompt)
	var out bytes.Buffer
	errTail := &tailBuffer{max: 4096}
	cmd.Stdout = &out
	cmd.Stderr = errTail
	ownGroup(cmd)
	cmd.WaitDelay = 10 * time.Second
	err = cmd.Run()
	return out.Bytes(), errTail.buf, err
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

// shell runs command in dir and reports whether it exited 0, with its
// combined output.
func shell(ctx context.Context, dir, command string, env []string, limit time.Duration) (bool, string) {
	sctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(sctx, "sh", "-c", command)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	ownGroup(cmd)
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

func diffLines(dir, base string) int {
	_, _ = git(dir, "add", "-A", "-N")
	out, err := git(dir, "diff", "--numstat", base)
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 {
			a, _ := strconv.Atoi(f[0])
			d, _ := strconv.Atoi(f[1])
			n += a + d
		}
	}
	return n
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func okText(ok bool) string {
	if ok {
		return "green"
	}
	return "red"
}

func tokensText(u Usage) string {
	if !u.Known {
		return "tokens unknown"
	}
	s := fmt.Sprintf("%s tokens", compact(u.Tokens()))
	if u.USDKnown {
		s += fmt.Sprintf(" $%.2f", u.USD)
	}
	return s
}

func wallText(sec float64) string {
	return (time.Duration(sec) * time.Second).Round(time.Second).String()
}

func compact(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return strconv.FormatInt(n, 10)
}

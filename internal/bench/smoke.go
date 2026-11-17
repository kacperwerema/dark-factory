package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	shq "github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The smoke (docs/benchmark.md "Smoke before a run"): the fixture project's
// tasks in a throwaway benchmark, run through Execute with the issue-cli
// under test, a stand-in worker that applies each task's known solution, and
// stand-in judge, grader and scorer, then checked piece by piece. No model
// is called and nothing is spent; it proves the harness on this machine
// before a real run does.

// Smoke check names, in the order they print.
const (
	SmokeFixture   = "fixture"
	SmokeRun       = "run"
	SmokeStages    = "stages"
	SmokeTrace     = "trace"
	SmokeUsage     = "usage"
	SmokeCalls     = "calls"
	SmokeIsolation = "isolation"
	SmokeReport    = "report"
	SmokeControl   = "control"
)

// SmokeCheckNames are the checks a smoke makes, in order.
var SmokeCheckNames = []string{SmokeFixture, SmokeRun, SmokeStages, SmokeTrace, SmokeUsage, SmokeCalls, SmokeIsolation, SmokeReport, SmokeControl}

// The stand-in worker's usage, which the usage check expects back.
const (
	smokeModel  = "stand-in"
	smokeTokens = 4200
)

// smokeWorker is the stand-in worker: it checks the prompt names its issue,
// calls the issue-cli under test (start, then a ## Design), copies the
// task's known solution over the work tree and prints a Claude Code result.
const smokeWorker = `set -e
exec 2>>"$BENCH_SMOKE_LOGS/$(printf '%s' "$ISSUE_VIEWER_ISSUE_SLUG" | tr / _).log"
prompt=$(cat)
case "$prompt" in
  *"$ISSUE_VIEWER_ISSUE_SLUG"*) ;;
  *) echo "stand-in worker: the prompt does not name $ISSUE_VIEWER_ISSUE_SLUG" >&2; exit 3 ;;
esac
issue-cli start "$ISSUE_VIEWER_ISSUE_SLUG" >/dev/null
issue-cli append "$ISSUE_VIEWER_ISSUE_SLUG" --section Design --body "Stand-in design: apply the known solution, its test and its docs line." >/dev/null
cp -R "$BENCH_SMOKE_SOLUTIONS/$ISSUE_VIEWER_ISSUE_SLUG/." .
echo '{"type":"result","session_id":"bench-smoke","num_turns":1,"total_cost_usd":0,"usage":{"input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":3000,"cache_creation_input_tokens":0},"modelUsage":{"stand-in":{}}}'
`

// smokeJudge answers every judge prompt with a full score.
const smokeJudge = `cat > /dev/null
printf 'SCORE: 5\nREASON: stand-in judge\n'
`

// smokeScorer stands in for promptfoo: every rubric line passes.
const smokeScorer = `printf '{"results":{"results":[{"gradingResult":{"score":1,"componentResults":[{"pass":true,"score":1,"reason":"stand-in scorer","assertion":{"type":"llm-rubric","value":"stand-in"}}]}}],"stats":{}}}' > result.json
`

// SmokeCheck is one check's verdict.
type SmokeCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// SmokeResult is what a smoke found.
type SmokeResult struct {
	Pass    bool         `json:"pass"`
	Checks  []SmokeCheck `json:"checks"`
	Tasks   int          `json:"tasks"`
	Seconds float64      `json:"seconds"`
	// Dir is the smoke's folder, kept only with SmokeOptions.Keep.
	Dir string `json:"dir,omitempty"`
	// Run is the run's record, nil when the run never started.
	Run *Run `json:"-"`
}

// Failed names the red checks.
func (r *SmokeResult) Failed() []string {
	var out []string
	for _, c := range r.Checks {
		if !c.OK {
			out = append(out, c.Name)
		}
	}
	return out
}

// SmokeOptions is what a smoke needs from its caller.
type SmokeOptions struct {
	// Env is the run's machine: IssueCLI (the issue-cli under test),
	// MemGuard, Log, Now. Projects, TmpRoot, Home and Keep are the smoke's.
	Env Env
	// Setup is recorded on the run as bench run records it.
	Setup Setup
	// Report renders a run as bench report prints it.
	Report func(*Run) string
	// Keep leaves the smoke's folder in place.
	Keep bool
	// Root is where the smoke's folder is made (default TMPDIR).
	Root string
	// Scorer replaces the stand-in scorer command (tests break the harness
	// with it).
	Scorer string
}

// Smoke runs the fixture's tasks end to end with stand-ins and checks what
// came back. The owner's bench folder is not touched: the benchmark and its
// run live in the smoke's folder, removed afterwards unless Keep.
func Smoke(ctx context.Context, opt SmokeOptions) (*SmokeResult, error) {
	started := time.Now()
	root := opt.Root
	if root == "" {
		root = os.TempDir()
	}
	dir, err := os.MkdirTemp(root, "awe-bench-smoke-")
	if err != nil {
		return nil, err
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	res := &SmokeResult{}
	if opt.Keep {
		res.Dir = dir
	} else {
		defer os.RemoveAll(dir)
	}
	// The benchmark, its snapshots and its run go to the smoke's own bench
	// folder for as long as the smoke runs.
	prev, had := os.LookupEnv("ISSUE_VIEWER_BENCH_DIR")
	if err := os.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(dir, "bench")); err != nil {
		return nil, err
	}
	defer func() {
		if had {
			os.Setenv("ISSUE_VIEWER_BENCH_DIR", prev)
		} else {
			os.Unsetenv("ISSUE_VIEWER_BENCH_DIR")
		}
	}()
	add := func(name string, ok bool, format string, a ...any) {
		res.Checks = append(res.Checks, SmokeCheck{Name: name, OK: ok, Detail: fmt.Sprintf(format, a...)})
	}
	finish := func() *SmokeResult {
		res.Seconds = time.Since(started).Seconds()
		res.Pass = len(res.Checks) == len(SmokeCheckNames) && len(res.Failed()) == 0
		return res
	}

	// The fixture and its tasks, as the owner's bench task add makes them.
	fx, proj, tasks, err := smokeTasks(ctx, dir, opt.Env.now())
	if err != nil {
		add(SmokeFixture, false, "%v", err)
		return finish(), nil
	}
	add(SmokeFixture, true, "%d tasks with stage references", len(tasks))
	res.Tasks = len(tasks)

	standins := filepath.Join(dir, "standin")
	for name, body := range map[string]string{"worker.sh": smokeWorker, "judge.sh": smokeJudge, "scorer.sh": smokeScorer} {
		if err := writeFile(filepath.Join(standins, name), body); err != nil {
			return nil, err
		}
	}
	solutions := filepath.Join(dir, "solutions")
	if err := writeSolutions(solutions, fx); err != nil {
		return nil, err
	}
	headBefore, _ := git(fx.Repo, "rev-parse", "HEAD")

	b := New("smoke")
	b.Tasks = tasks
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return nil, err
	}
	b.Worker = "BENCH_SMOKE_SOLUTIONS=" + shq.Quote(solutions) + " BENCH_SMOKE_LOGS=" + shq.Quote(logs) + " sh " + shq.Quote(filepath.Join(standins, "worker.sh"))
	b.Judge = "sh " + shq.Quote(filepath.Join(standins, "judge.sh"))
	b.Grader = "stand-in"
	b.Scorer = "sh " + shq.Quote(filepath.Join(standins, "scorer.sh"))
	if opt.Scorer != "" {
		b.Scorer = opt.Scorer
	}
	b.Budget.TaskWall = "10m"
	if err := b.Save(); err != nil {
		return nil, err
	}

	env := opt.Env
	env.Projects = func(slug string) *tracker.Project {
		if slug == proj.Slug {
			return proj
		}
		return nil
	}
	env.TmpRoot = filepath.Join(dir, "runs")
	env.Home = ""
	env.Keep = opt.Keep
	if env.Log != nil {
		// A stand-in needs no login: the run's "no login found" note would
		// only mislead.
		env.Log = dropLogLines{w: env.Log, prefix: "worker home:"}
	}
	if env.MachineMemory == "" {
		env.MachineMemory = filepath.Join(dir, "no-machine-memory.jsonl")
	}
	setup := opt.Setup
	setup.Label = "smoke"
	setup.Agent = AgentClaude
	run, err := Execute(ctx, b, env, Options{Setup: setup, StartedBy: "smoke"})
	if err != nil {
		add(SmokeRun, false, "%v", err)
		return finish(), nil
	}
	res.Run = run
	checkSmokeRun(run, tasks, logs, add)

	clean, _ := git(fx.Repo, "status", "--porcelain")
	headAfter, _ := git(fx.Repo, "rev-parse", "HEAD")
	var outside []string
	for _, r := range run.Tasks {
		outside = append(outside, r.OutsideTouched...)
	}
	switch {
	case len(outside) > 0:
		add(SmokeIsolation, false, "the worker wrote outside its sandbox: %s", strings.Join(outside, ", "))
	case strings.TrimSpace(clean) != "" || headAfter != headBefore:
		add(SmokeIsolation, false, "the fixture repo changed: %s", firstLine(strings.TrimSpace(clean)+" HEAD "+strings.TrimSpace(headAfter)))
	default:
		add(SmokeIsolation, true, "nothing written outside the sandboxes, the fixture repo unchanged")
	}

	if opt.Report == nil {
		add(SmokeReport, false, "no report printer")
	} else if out := opt.Report(run); !strings.Contains(out, "stages:") {
		add(SmokeReport, false, "bench report printed no stages: line")
	} else {
		add(SmokeReport, true, "bench report renders the run with its stages")
	}

	// The control: a worker that changes nothing fails, so a green run is
	// not one that passes everything.
	control := tasks[smokeControlTask(tasks)]
	cb := New("smoke-control")
	cb.Tasks = []Task{control}
	cb.Worker = "cat > /dev/null"
	cb.Budget.TaskWall = "10m"
	if err := cb.Save(); err != nil {
		return nil, err
	}
	crun, err := Execute(ctx, cb, env, Options{Setup: setup, StartedBy: "smoke"})
	ok, detail := smokeControlVerdict(control, crun, err)
	add(SmokeControl, ok, "%s", detail)
	return finish(), nil
}

// smokeControlVerdict is the control check: the do-nothing worker's task
// must come back fail, or a green run proves nothing.
func smokeControlVerdict(control Task, run *Run, err error) (bool, string) {
	switch {
	case err != nil:
		return false, err.Error()
	case run == nil || len(run.Tasks) != 1:
		n := 0
		if run != nil {
			n = len(run.Tasks)
		}
		return false, fmt.Sprintf("%d records for one task", n)
	case run.Tasks[0].Outcome != OutcomeFail:
		return false, fmt.Sprintf("a worker that changes nothing came back %s, not fail: the scoring passes work that was never done", run.Tasks[0].Outcome)
	}
	return true, fmt.Sprintf("a worker that changes nothing fails %s (%s)", control.Slug, run.Tasks[0].Reason)
}

// smokeControlTask is the control's task: the tweak (no design status, the
// quickest), else the first.
func smokeControlTask(tasks []Task) int {
	for i, t := range tasks {
		if t.Type == "tweak" {
			return i
		}
	}
	return 0
}

// smokeTasks writes the fixture under dir/fx and makes a task of each of
// its issues as bench task add would, with a docs rubric line.
func smokeTasks(ctx context.Context, dir string, now time.Time) (*Fixture, *tracker.Project, []Task, error) {
	fx, err := FixtureInit(filepath.Join(dir, "fx"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fixture init: %w", err)
	}
	proj := &tracker.Project{Name: FixtureName, Slug: FixtureSlug, IssueDir: fx.Issues, WorkflowFile: fx.Workflow, WorkDir: fx.Repo, Terminal: "none"}
	issues, err := tracker.LoadIssues(fx.Issues)
	if err != nil {
		return nil, nil, nil, err
	}
	var tasks []Task
	for _, slug := range fx.Tasks {
		var issue *tracker.Issue
		for i := range issues {
			if issues[i].Slug == slug {
				issue = issues[i]
			}
		}
		if issue == nil {
			return nil, nil, nil, fmt.Errorf("fixture task %s did not load", slug)
		}
		t, _, err := NewTask(ctx, proj, issue, AddOptions{Verify: true, DocsRubric: []string{"Says what the change does for a user"}}, now)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("task %s: %w", slug, err)
		}
		if t.Stages == nil {
			return nil, nil, nil, fmt.Errorf("task %s: no stage references", slug)
		}
		tasks = append(tasks, t)
	}
	if len(tasks) != len(fixtureTasks) {
		return nil, nil, nil, fmt.Errorf("%d tasks, the fixture has %d", len(tasks), len(fixtureTasks))
	}
	return fx, proj, tasks, nil
}

// writeSolutions writes each task's known solution (the fixture's files
// for it but its tests and issue) under dir/<slug>/, where the stand-in
// worker finds it: the sandbox has no ref past base to read it from.
func writeSolutions(dir string, fx *Fixture) error {
	for i, slug := range fx.Tasks {
		from := path.Join("fixture", fixtureTasks[i].dir)
		to := filepath.Join(dir, filepath.FromSlash(slug))
		if err := os.MkdirAll(to, 0o755); err != nil {
			return err
		}
		err := fs.WalkDir(fixtureFS, from, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			name := strings.TrimSuffix(d.Name(), ".txt")
			if name == "issue.md" || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			data, err := fixtureFS.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(to, name), data, 0o644)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// checkSmokeRun adds the checks read from the run's records.
func checkSmokeRun(run *Run, tasks []Task, logs string, add func(string, bool, string, ...any)) {
	byID := map[string]TaskRecord{}
	for _, r := range run.Tasks {
		byID[r.Task] = r
	}
	var runBad, stageBad, traceBad, usageBad, callsBad []string
	for _, t := range tasks {
		r, ok := byID[t.ID()]
		if !ok {
			runBad = append(runBad, t.Slug+": no record")
			continue
		}
		if r.Outcome != OutcomePass || r.HiddenTotal == 0 || r.HiddenPassed != r.HiddenTotal || !r.CheckOK {
			bad := fmt.Sprintf("%s: %s %s (hidden %d/%d)", t.Slug, r.Outcome, r.Reason, r.HiddenPassed, r.HiddenTotal)
			if said := smokeLogLine(filepath.Join(logs, strings.ReplaceAll(t.Slug, "/", "_")+".log")); said != "" {
				bad += "; the stand-in worker said: " + said
			}
			runBad = append(runBad, bad)
		}
		if why := smokeStagesProblem(t, r); why != "" {
			stageBad = append(stageBad, t.Slug+": "+why)
		}
		switch {
		case len(r.Trace) == 0:
			traceBad = append(traceBad, t.Slug+": no spans")
		case r.Trace[0].Status != t.Start:
			traceBad = append(traceBad, fmt.Sprintf("%s: first span at %q, the task starts at %q", t.Slug, r.Trace[0].Status, t.Start))
		}
		if !r.Usage.Known || r.Usage.Tokens() != smokeTokens || r.Usage.Model != smokeModel {
			usageBad = append(usageBad, fmt.Sprintf("%s: known %v, %d tokens, model %q", t.Slug, r.Usage.Known, r.Usage.Tokens(), r.Usage.Model))
		}
		if r.Calls < 2 {
			callsBad = append(callsBad, fmt.Sprintf("%s: %d issue-cli calls logged", t.Slug, r.Calls))
		}
	}
	verdict := func(name string, bad []string, good string) {
		if len(bad) > 0 {
			add(name, false, "%s", strings.Join(bad, "; "))
			return
		}
		add(name, true, "%s", good)
	}
	if run.Stop != StopDone {
		runBad = append([]string{"stopped " + run.Stop}, runBad...)
	}
	verdict(SmokeRun, runBad, fmt.Sprintf("%d/%d pass, every hidden test green", len(tasks), len(tasks)))
	verdict(SmokeStages, stageBad, "design by the judge and rubric, code by the hidden tests, docs by the rubric")
	verdict(SmokeTrace, traceBad, "each task's trace starts at its start status")
	verdict(SmokeUsage, usageBad, fmt.Sprintf("%d tokens per task, model %s, read from the worker's result", smokeTokens, smokeModel))
	verdict(SmokeCalls, callsBad, "the worker's issue-cli calls reached the sandbox's log")
}

// smokeStagesProblem says what is wrong with a task's stage scores, ""
// when each stage with a reference passed and each without says so.
func smokeStagesProblem(t Task, r TaskRecord) string {
	if len(r.Stages) != len(StageNames) {
		return fmt.Sprintf("%d stages scored, want %d", len(r.Stages), len(StageNames))
	}
	for _, s := range r.Stages {
		if _, missing := t.Stages.Missing[s.Stage]; missing {
			if !strings.HasPrefix(s.Reason, "no reference") {
				return fmt.Sprintf("%s has no reference but says %q", s.Stage, s.Reason)
			}
			continue
		}
		if !s.Known() || !s.Pass {
			return fmt.Sprintf("%s %s", s.Stage, orText(s.Reason, fmt.Sprintf("score %.2f", s.Score)))
		}
		if s.Stage == StageDesign && s.Judge != 5 {
			return fmt.Sprintf("design judge %d, the stand-in says 5", s.Judge)
		}
	}
	return ""
}

// smokeLogLine is the first line of a stand-in's log (its error), "" none.
func smokeLogLine(p string) string {
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return strings.TrimSpace(lines[0])
}

func orText(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// dropLogLines drops the log writes that start with prefix.
type dropLogLines struct {
	w      io.Writer
	prefix string
}

func (d dropLogLines) Write(p []byte) (int, error) {
	if strings.HasPrefix(string(p), d.prefix) {
		return len(p), nil
	}
	return d.w.Write(p)
}

func writeFile(p, body string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(body), 0o644)
}

// SmokeRecord is the last passing smoke, kept in the owner's bench folder
// (smoke.json) so bench run can name it.
type SmokeRecord struct {
	At              time.Time `json:"at"`
	IssueCLIVersion string    `json:"issue_cli_version"`
	Ref             string    `json:"ref,omitempty"`
	SHA             string    `json:"sha,omitempty"`
	Seconds         float64   `json:"seconds"`
}

// SmokePath is the smoke record's file.
func SmokePath() string { return filepath.Join(Dir(), "smoke.json") }

// SaveSmoke writes rec as the last passing smoke.
func SaveSmoke(rec SmokeRecord) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	return jsonfile.Write(SmokePath(), rec, 0o644)
}

// LoadSmoke reads the last passing smoke; ok is false when there is none.
func LoadSmoke() (SmokeRecord, bool) {
	var rec SmokeRecord
	data, err := os.ReadFile(SmokePath())
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return rec, false
	}
	if json.Unmarshal(data, &rec) != nil {
		return rec, false
	}
	return rec, true
}

// SmokeLine is the line bench run and bench print about the last smoke for
// the issue-cli version: a PASS on it, or how to run one.
func SmokeLine(version string) string {
	rec, ok := LoadSmoke()
	if ok && rec.IssueCLIVersion == version {
		return fmt.Sprintf("smoke: PASS %s on %s", rec.At.Local().Format("2006-01-02 15:04"), version)
	}
	line := "smoke: none for this issue-cli (" + version + "): issue-cli bench smoke proves the harness first (stand-ins, no tokens)"
	if ok {
		line += fmt.Sprintf("; the last PASS was on %s", rec.IssueCLIVersion)
	}
	return line
}

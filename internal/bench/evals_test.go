package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The marketing fixture: a project whose Marketing department runs on its
// own workflow file with a piece eval, and one golden task made from a post
// the owner approved. The worker stand-in puts a post on the sandbox's
// shelf; the scorer stand-in answers like promptfoo (testdata holds a real
// result of the pinned version): score 1 when the post says GOOD.

const marketingWorkflow = `statuses:
  - name: brief
  - name: draft
  - name: done
default_type: piece
types:
  piece: {}
evals:
  - id: piece
    types: [piece]
    score: output
    rubric: ["reads like the brand"]
    assert:
      - {type: icontains, value: GOOD}
    pass: 0.7
`

type mktFixture struct {
	repo string
	proj *tracker.Project
	task Task
}

func newMarketingFixture(t *testing.T) *mktFixture {
	t.Helper()
	f := &mktFixture{repo: filepath.Join(t.TempDir(), "shop")}
	mustGit(t, filepath.Dir(f.repo), "init", "-q", "-b", "main", f.repo)
	write(t, filepath.Join(f.repo, "workflow.yaml"), fixtureWorkflow)
	write(t, filepath.Join(f.repo, "workflows", "marketing.yaml"), marketingWorkflow)
	write(t, filepath.Join(f.repo, "departments", "marketing", "brand.md"), "Warm, plain.\n")
	mustGit(t, f.repo, "add", "-A")
	mustGit(t, f.repo, "commit", "-q", "-m", "base")
	head := mustGit(t, f.repo, "rev-parse", "HEAD")
	f.proj = &tracker.Project{Name: "Shop", Slug: "shop", IssueDir: filepath.Join(f.repo, "issues"), WorkflowFile: filepath.Join(f.repo, "workflow.yaml"), WorkDir: f.repo,
		Departments: []tracker.Department{{Name: "Marketing", Workflow: "workflows/marketing.yaml"}}}
	f.task = Task{Project: "shop", Slug: "marketing/post-tees", Type: "piece", Workflow: "marketing", Base: head, Merged: head, Start: "brief",
		Output: "o-1", Reference: "o-1-post.md"}
	write(t, SnapshotPath(f.task), "---\ntitle: \"Post: tees\"\nstatus: \"brief\"\ntype: piece\n---\n\nA post about tees.\n")
	write(t, ReferencePath(f.task), "The approved post about tees.\n")
	return f
}

// postWorker puts a post saying word on the sandbox's shelf (the tracker is
// next to the checkout), or fixes calc.go in the calc project.
func postWorker(word string) string {
	return `cat > /dev/null; if [ -f calc.go ]; then printf 'package calc\n\nfunc Add(a, b int) int { return a + b }\n' > calc.go; else ` +
		`d=../tracker/issues/.outputs; mkdir -p $d/files/marketing; printf '` + word + ` post' > $d/files/marketing/o1-post.md; ` +
		`printf '%s\n' '{"op":"add","id":"o1","date":"2026-10-31T10:00:00Z","department":"marketing","title":"Post","kind":"post","file":"marketing/o1-post.md","issue":"marketing/post-tees"}' >> $d/outputs.jsonl; fi; ` +
		`echo '{"type":"result","total_cost_usd":0.5,"usage":{"input_tokens":1000,"output_tokens":100},"modelUsage":{"claude-opus-5-5":{}}}'`
}

// stubScorer writes a promptfoo-shaped result.json: score 1 when the output
// says GOOD, else 0 (and exit 100, as promptfoo does on a failed test).
func stubScorer(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scorer.sh")
	write(t, path, `env > env.seen
if grep -q GOOD output.txt; then s=1; p=true; else s=0; p=false; fi
printf '{"results":{"results":[{"gradingResult":{"score":%s,"componentResults":[{"pass":%s,"score":%s,"reason":"looked for GOOD","assertion":{"type":"icontains","value":"GOOD"}}]}}],"stats":{}}}' $s $p $s > result.json
[ "$p" = true ] || exit 100
`)
	return "sh " + path
}

func twoProjectEnv(t *testing.T, calc *fixture, mkt *mktFixture) Env {
	env := calc.env(t)
	env.Projects = func(slug string) *tracker.Project {
		switch slug {
		case calc.proj.Slug:
			return calc.proj
		case mkt.proj.Slug:
			return mkt.proj
		}
		return nil
	}
	return env
}

func TestRunScoresAnOutputTaskWithItsWorkflowsEval(t *testing.T) {
	calc := newFixture(t)
	mkt := newMarketingFixture(t)
	env := twoProjectEnv(t, calc, mkt)
	env.Keep = true
	b := calc.benchmark(postWorker("GOOD"), mkt.task)
	b.Scorer = stubScorer(t)
	run, err := Execute(context.Background(), b, env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if r.Outcome != OutcomePass || r.Workflow != "marketing" || r.Eval != "piece" || r.EvalScore != 1 || r.EvalPassed != 1 || r.EvalTotal != 1 {
		t.Fatalf("record = %+v", r)
	}
	if len(r.EvalSkipped) != 1 || !strings.Contains(r.EvalSkipped[0], "reads like the brand") {
		t.Errorf("without a grader the rubric is left out and named: %q", r.EvalSkipped)
	}
	dir := filepath.Join(env.TmpRoot, run.ID, "01")
	// The sandbox ran the department's own workflow file.
	if data, _ := os.ReadFile(filepath.Join(dir, "tracker", "workflow.yaml")); string(data) != marketingWorkflow {
		t.Errorf("sandbox workflow = %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "eval", "reference.txt")); string(data) != "The approved post about tees.\n" {
		t.Errorf("reference = %q", data)
	}
	seen, _ := os.ReadFile(filepath.Join(dir, "eval", "env.seen"))
	for _, want := range []string{"PROMPTFOO_DISABLE_TELEMETRY=1", "PROMPTFOO_DISABLE_UPDATE=1", "PROMPTFOO_CONFIG_DIR=" + filepath.Join(dir, "eval", ".promptfoo")} {
		if !strings.Contains(string(seen), want) {
			t.Errorf("scorer env lacks %s", want)
		}
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "eval", "promptfooconfig.yaml"))
	if !strings.Contains(string(cfg), "icontains") || strings.Contains(string(cfg), "llm-rubric") || !strings.Contains(string(cfg), "echo") {
		t.Errorf("config = %s", cfg)
	}
}

func TestRunFailsAnOutputTaskWithNothingOnTheShelf(t *testing.T) {
	calc := newFixture(t)
	mkt := newMarketingFixture(t)
	b := calc.benchmark(`cat > /dev/null; echo '{}'`, mkt.task)
	b.Scorer = stubScorer(t)
	run, err := Execute(context.Background(), b, twoProjectEnv(t, calc, mkt), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r := run.Tasks[0]; r.Outcome != OutcomeFail || !strings.Contains(r.Reason, "no output on the shelf") || r.EvalScore != 0 {
		t.Fatalf("record = %+v", r)
	}
}

func TestRunRefusesAnEvalNothingCanGradeBeforeTheWorkerStarts(t *testing.T) {
	calc := newFixture(t)
	mkt := newMarketingFixture(t)
	write(t, filepath.Join(mkt.repo, "workflows", "marketing.yaml"), strings.Replace(marketingWorkflow, "    assert:\n      - {type: icontains, value: GOOD}\n", "", 1))
	b := calc.benchmark(`touch worker.ran; echo '{}'`, mkt.task)
	env := twoProjectEnv(t, calc, mkt)
	env.Keep = true
	// The department file is read from the project checkout at run time.
	run, err := Execute(context.Background(), b, env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if r.Outcome != OutcomeError || !strings.Contains(r.Reason, "nothing to score") {
		t.Fatalf("record = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(env.TmpRoot, run.ID, "01", "repo", "worker.ran")); err == nil {
		t.Error("the worker ran though nothing could score it")
	}
}

// AC4: a workflow change gets a verdict per workflow, with the numbers, on
// sandbox runs: the change to the marketing file makes the post worse, the
// development task is a control and holds.
func TestAWorkflowChangeGetsAVerdictPerWorkflow(t *testing.T) {
	calc := newFixture(t)
	mkt := newMarketingFixture(t)
	env := twoProjectEnv(t, calc, mkt)
	clock := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	env.Now = func() time.Time { clock = clock.Add(time.Second); return clock }
	scorer := stubScorer(t)
	runWith := func(word string) *Run {
		b := calc.benchmark(postWorker(word), calc.task, mkt.task)
		b.Scorer = scorer
		run, err := Execute(context.Background(), b, env, Options{})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	a, b := runWith("GOOD"), runWith("BAD")
	if a.ID == b.ID {
		t.Fatal("two runs, one id")
	}
	got := CompareByWorkflow(a, b)
	if len(got) != 2 || got[0].Workflow != "product" || got[1].Workflow != "marketing" {
		t.Fatalf("verdicts = %+v", got)
	}
	prod, m := got[0], got[1]
	if prod.Verdict != NoChange || prod.PassRateA != 1 || prod.PassRateB != 1 {
		t.Errorf("product = %+v", prod)
	}
	if m.Verdict != NetNegative || m.ScoreA != 1 || m.ScoreB != 0 || m.PassRateA != 1 || m.PassRateB != 0 ||
		strings.Join(m.Regressions, ",") != "shop/marketing/post-tees" || m.Tasks != 1 {
		t.Errorf("marketing = %+v", m)
	}
	if !strings.Contains(strings.Join(m.Why, "; "), "1 task(s) regressed") {
		t.Errorf("why = %q", m.Why)
	}
	// And back: the old file is a net positive against the bad one.
	if v := CompareByWorkflow(b, a)[1]; v.Verdict != NetPositive {
		t.Errorf("reverse = %+v", v)
	}
}

func TestParseScorerResultReadsPromptfoosOwnOutput(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "promptfoo-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ParseScorerResult(data)
	if err != nil {
		t.Fatal(err)
	}
	// promptfoo 0.124.1, echo provider: icontains passed (1), llm-rubric
	// graded 0.8 by a stand-in exec: grader.
	if res.Total != 2 || res.Passed != 2 || res.Score < 0.899 || res.Score > 0.901 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if _, err := ParseScorerResult([]byte(`{"results":{"results":[]}}`)); err == nil {
		t.Error("an empty result parsed")
	}
}

func TestScorerConfigGradesTheRubricWithTheGraderOnly(t *testing.T) {
	dir := t.TempDir()
	ev := tracker.WorkflowEval{ID: "piece", Score: "output", Rubric: []string{"as good as {{reference}}"}, Assert: []tracker.EvalAssert{{Type: "not-icontains", Value: "lorem"}}}
	skipped, err := WriteScorerConfig(dir, ScoreInput{Eval: ev, Output: "post", Reference: "ref", Grader: "claude -p --model claude-haiku-5-5"})
	if err != nil || len(skipped) != 0 {
		t.Fatalf("skipped %v err %v", skipped, err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "promptfooconfig.yaml"))
	script, _ := os.ReadFile(filepath.Join(dir, "grader.sh"))
	for _, want := range []string{"llm-rubric", "as good as {{reference}}", "not-icontains", "exec: ./grader.sh", "file://output.txt"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
	if !strings.Contains(string(script), `printf '%s' "$1" | claude -p --model claude-haiku-5-5`) {
		t.Errorf("grader.sh = %q", script)
	}
	if _, err := Score(context.Background(), t.TempDir(), "false", ScoreInput{Eval: tracker.WorkflowEval{ID: "x", Score: "output", Rubric: []string{"r"}}}, nil); err != ErrNothingToScore {
		t.Errorf("a rubric without a grader: err = %v", err)
	}
}

func TestWorkflowVerdictCostDecidesWhenQualityHolds(t *testing.T) {
	base := WorkflowVerdict{PassRateA: 1, PassRateB: 1, ScoreA: 0.8, ScoreB: 0.82, TokensA: 1000, USDA: 1}
	cases := []struct {
		tokens int64
		usd    float64
		wall   float64
		want   string
	}{{1200, 1.2, 100, NetNegative}, {800, 0.85, 100, NetPositive}, {1050, 1.02, 100, NoChange},
		{1000, 1, 130, NetNegative}, {1000, 1, 70, NetPositive}, {1000, 1, 120, NoChange}}
	for _, c := range cases {
		v := base
		v.WallA = 100
		v.TokensB, v.USDB, v.WallB = c.tokens, c.usd, c.wall
		if got, why := judgeWorkflow(v, 4); got != c.want {
			t.Errorf("tokens %d usd %v wall %v: %s (%v), want %s", c.tokens, c.usd, c.wall, got, why, c.want)
		}
	}
	v := base
	v.ScoreB = 0.9
	v.TokensB, v.USDB = 3000, 3
	if got, _ := judgeWorkflow(v, 4); got != NetPositive {
		t.Errorf("a better score at more cost is %s", got)
	}
}

// shelfFixture adds two done Marketing issues to the marketing fixture: one
// with a post the owner approved, one whose post still waits for review.
func shelfFixture(t *testing.T) (*mktFixture, []*tracker.Issue) {
	t.Helper()
	f := newMarketingFixture(t)
	issues := filepath.Join(f.repo, "issues")
	for _, slug := range []string{"post-tees", "post-caps"} {
		write(t, filepath.Join(issues, "marketing", slug+".md"), "---\ntitle: \"Post: "+slug+"\"\nslug: \""+slug+"\"\nstatus: \"done\"\ntype: piece\ndepartment: Marketing\n---\n\nA post.\n")
	}
	write(t, filepath.Join(issues, ".outputs", "files", "marketing", "o-1-post.md"), "The approved post about tees.\n")
	write(t, filepath.Join(issues, ".outputs", "files", "marketing", "o-2-post.md"), "A draft about caps.\n")
	write(t, filepath.Join(issues, ".outputs", "outputs.jsonl"),
		`{"op":"add","id":"o-1","date":"2026-10-20T10:00:00Z","department":"marketing","title":"Tees","kind":"post","file":"marketing/o-1-post.md","issue":"marketing/post-tees"}`+"\n"+
			`{"op":"review","id":"o-1","date":"2026-10-21T10:00:00Z","state":"approved","quote":"good, ship it","how":"on the shelf","by":"owner"}`+"\n"+
			`{"op":"add","id":"o-2","date":"2026-10-22T10:00:00Z","department":"marketing","title":"Caps","kind":"post","file":"marketing/o-2-post.md","issue":"marketing/post-caps"}`+"\n")
	list, err := tracker.LoadIssues(issues)
	if err != nil {
		t.Fatal(err)
	}
	return f, list
}

func TestNewOutputTaskCopiesTheApprovedPostAsTheReference(t *testing.T) {
	f, issues := shelfFixture(t)
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(t.TempDir(), "bench2"))
	var tees, caps *tracker.Issue
	for _, is := range issues {
		switch is.Slug {
		case "marketing/post-tees":
			tees = is
		case "marketing/post-caps":
			caps = is
		}
	}
	snap := filepath.Join(t.TempDir(), "snap.md")
	write(t, snap, "---\ntitle: \"Post: tees\"\nstatus: \"done\"\ntype: piece\n---\n\nA post about tees.\n")
	task, warnings, err := NewOutputTask(context.Background(), f.proj, tees, "", AddOptions{Start: "brief", Snapshot: snap}, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if task.Workflow != "marketing" || task.Output != "o-1" || task.Reference != "o-1-post.md" || task.Base == "" || task.Base != task.Merged || task.Start != "brief" {
		t.Errorf("task = %+v", task)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "starts at HEAD") {
		t.Errorf("warnings = %q", warnings)
	}
	if data, _ := os.ReadFile(ReferencePath(task)); string(data) != "The approved post about tees.\n" {
		t.Errorf("reference = %q", data)
	}
	if data, _ := os.ReadFile(SnapshotPath(task)); !strings.Contains(string(data), `status: "brief"`) {
		t.Errorf("snapshot = %q", data)
	}
	if _, _, err := NewOutputTask(context.Background(), f.proj, caps, "", AddOptions{Start: "brief", Snapshot: snap}, time.Now()); err == nil || !strings.Contains(err.Error(), "only an output the owner approved") {
		t.Errorf("an unreviewed post made a task: %v", err)
	}
}

// A run leaves nothing outside its sandbox: the worker and the scorer get
// the sandbox's state and config dirs (and the scorer its HOME), so a
// viewer.json or projects.yaml they write lands there, and neither sees
// the owner's benchmark folder (the holdout lives in it).
func TestARunLeavesNothingOutsideItsSandbox(t *testing.T) {
	calc := newFixture(t)
	mkt := newMarketingFixture(t)
	real := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(real, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(real, "config"))
	env := twoProjectEnv(t, calc, mkt)
	env.Keep = true
	touch := `mkdir -p "$XDG_STATE_HOME/ai-workflow-engine" "$XDG_CONFIG_HOME/ai-workflow-engine"; echo w > "$XDG_STATE_HOME/ai-workflow-engine/viewer.json"; echo w > "$XDG_CONFIG_HOME/ai-workflow-engine/projects.yaml"; echo "bench=$ISSUE_VIEWER_BENCH_DIR" > bench.seen; `
	b := calc.benchmark(touch+postWorker("GOOD"), mkt.task)
	scorer := filepath.Join(t.TempDir(), "scorer.sh")
	write(t, scorer, `mkdir -p "$XDG_STATE_HOME/ai-workflow-engine"; echo s > "$XDG_STATE_HOME/ai-workflow-engine/viewer.json"; echo s > "$HOME/.promptfoo-was-here"; echo "bench=$ISSUE_VIEWER_BENCH_DIR" > bench.seen
printf '{"results":{"results":[{"gradingResult":{"score":1,"componentResults":[{"pass":true,"score":1,"reason":"ok","assertion":{"type":"icontains","value":"GOOD"}}]}}],"stats":{}}}' > result.json
`)
	b.Scorer = "sh " + scorer
	run, err := Execute(context.Background(), b, env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r := run.Tasks[0]; r.Outcome != OutcomePass {
		t.Fatalf("record = %+v", r)
	}
	filepath.Walk(real, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			t.Errorf("the run wrote %s outside its sandbox", p)
		}
		return nil
	})
	dir := filepath.Join(env.TmpRoot, run.ID, "01")
	for _, p := range []string{
		filepath.Join(dir, "state", "ai-workflow-engine", "viewer.json"),
		filepath.Join(dir, "config", "ai-workflow-engine", "projects.yaml"),
		filepath.Join(dir, "eval", "home", ".promptfoo-was-here"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("not in the sandbox: %v", err)
		}
	}
	for _, p := range []string{filepath.Join(dir, "repo", "bench.seen"), filepath.Join(dir, "eval", "bench.seen")} {
		if data, _ := os.ReadFile(p); strings.TrimSpace(string(data)) != "bench=" {
			t.Errorf("%s: the benchmark folder was visible: %q", p, data)
		}
	}
}

func TestEstimateUsesTheLastFinishedRunsMeanPerTask(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	b := New("est")
	b.Tasks = []Task{{Project: "p", Slug: "a"}, {Project: "p", Slug: "b"}, {Project: "p", Slug: "c"}, {Project: "p", Slug: "d"}}
	if got := Estimate(b); !strings.Contains(got, "no previous run") {
		t.Fatalf("estimate = %q", got)
	}
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	r := &Run{ID: "r1", Benchmark: "est", Started: at, Ended: at.Add(time.Hour), Stop: StopDone}
	recs := []TaskRecord{
		{Task: "p/a", Outcome: OutcomePass, WallSec: 540, ScoreWallSec: 60, Usage: Usage{Known: true, Input: 1_000_000, USD: 2}},
		{Task: "p/b", Outcome: OutcomePass, WallSec: 600, Usage: Usage{Known: true, Input: 3_000_000, USD: 4}},
	}
	if err := Save(r, recs); err != nil {
		t.Fatal(err)
	}
	got := Estimate(b)
	if !strings.Contains(got, "about 40m0s") || !strings.Contains(got, "$12.00 for 4 tasks") || !strings.Contains(got, "r1's mean per task: 10m0s, $3.00") {
		t.Fatalf("estimate = %q", got)
	}
}

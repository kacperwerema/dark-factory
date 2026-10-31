package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/bench"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

const cliMarketingWorkflow = `template: marketing@v0.58.0
statuses: [{name: brief}, {name: draft}, {name: done}]
transitions: [{from: brief, to: draft}, {from: draft, to: done}]
default_type: piece
types:
  piece: {}
evals:
  - id: piece
    types: [piece]
    score: output
    assert: [{type: icontains, value: GOOD}]
`

// shopProject is a repo whose Marketing department runs on its own
// workflow file, with one done post the owner approved and one waiting for
// review.
func shopProject(t *testing.T) *tracker.Project {
	t.Helper()
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(t.TempDir(), "bench"))
	repo := filepath.Join(t.TempDir(), "shop")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	put := func(rel, data string) {
		t.Helper()
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	put("workflow.yaml", "statuses: [{name: backlog}, {name: done}]\ntransitions: [{from: backlog, to: done}]\n")
	put("workflows/marketing.yaml", cliMarketingWorkflow)
	for _, slug := range []string{"post-tees", "post-caps"} {
		put("issues/marketing/"+slug+".md", "---\ntitle: \"Post "+slug+"\"\nslug: \""+slug+"\"\nstatus: \"done\"\ntype: piece\ndepartment: Marketing\n---\n\nA post.\n")
	}
	put("issues/.outputs/files/marketing/o-1-post.md", "The approved post about tees.\n")
	put("issues/.outputs/files/marketing/o-2-post.md", "Caps draft.\n")
	put("issues/.outputs/outputs.jsonl",
		`{"op":"add","id":"o-1","date":"2026-10-20T10:00:00Z","department":"marketing","title":"Tees","kind":"post","file":"marketing/o-1-post.md","issue":"marketing/post-tees"}`+"\n"+
			`{"op":"review","id":"o-1","date":"2026-10-21T10:00:00Z","state":"approved","quote":"good, ship it","how":"on the shelf","by":"owner"}`+"\n"+
			`{"op":"add","id":"o-2","date":"2026-10-22T10:00:00Z","department":"marketing","title":"Caps","kind":"post","file":"marketing/o-2-post.md","issue":"marketing/post-caps"}`+"\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	return &tracker.Project{Name: "Shop", Slug: "shop", IssueDir: filepath.Join(repo, "issues"), WorkflowFile: filepath.Join(repo, "workflow.yaml"), WorkDir: repo,
		Departments: []tracker.Department{{Name: "Marketing", Workflow: "workflows/marketing.yaml"}}}
}

func TestBenchOutputTasksComeOnlyFromPostsTheOwnerApproved(t *testing.T) {
	proj := shopProject(t)
	asWorker(t)
	// Only the owner adds.
	ctx, _, _ := newTestContext(proj, false)
	if err := runBench(ctx, []string{"task", "add", "mkt", "marketing/post-tees"}); err == nil || !strings.Contains(err.Error(), "only the owner changes the benchmark") {
		t.Errorf("worker task add: %v", err)
	}
	asOwner(t)
	snap := filepath.Join(t.TempDir(), "snap.md")
	if err := os.WriteFile(snap, []byte("---\ntitle: \"Post tees\"\nstatus: \"done\"\ntype: piece\n---\n\nA post about tees.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, out, errOut := newTestContext(proj, false)
	if err := runBench(ctx, []string{"task", "add", "mkt", "marketing/post-tees", "--start", "brief", "--snapshot", snap}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(out.String(), "output task (marketing)") || !strings.Contains(out.String(), "reference: output o-1") {
		t.Errorf("task add said:\n%s", out)
	}
	b, err := bench.Load("mkt")
	if err != nil || len(b.Tasks) != 1 || b.Tasks[0].Workflow != "marketing" || b.Tasks[0].Output != "o-1" {
		t.Fatalf("benchmark = %+v %v", b, err)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"task", "add", "mkt", "marketing/post-caps", "--start", "brief", "--snapshot", snap}); err == nil || !strings.Contains(err.Error(), "only an output the owner approved") {
		t.Errorf("an unreviewed post: %v", err)
	}
}

func TestBenchCompareGivesEachWorkflowItsVerdictWithTimeAndCost(t *testing.T) {
	proj := shopProject(t)
	asWorker(t)
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	rec := func(task, wf, outcome string, score float64, holdout bool) bench.TaskRecord {
		r := bench.TaskRecord{Task: task, Workflow: wf, Outcome: outcome, Holdout: holdout, Judge: -1, EvalScore: -1, Usage: bench.Usage{Known: true, Input: 1000, USD: 1}, WallSec: 60}
		if score >= 0 {
			r.Eval, r.EvalScore, r.EvalTotal = "piece", score, 1
		}
		return r
	}
	save := func(id string, records ...bench.TaskRecord) {
		r := &bench.Run{ID: id, Benchmark: "mix", Started: at, Ended: at.Add(time.Hour), Stop: bench.StopDone}
		at = at.Add(2 * time.Hour)
		if err := bench.Save(r, records); err != nil {
			t.Fatal(err)
		}
	}
	save("r1", rec("shop/calc", "product", bench.OutcomePass, -1, false), rec("shop/marketing/post-tees", "marketing", bench.OutcomePass, 0.9, false), rec("shop/secret-one", "marketing", bench.OutcomePass, 1, true))
	save("r2", rec("shop/calc", "product", bench.OutcomePass, -1, false), rec("shop/marketing/post-tees", "marketing", bench.OutcomeFail, 0.4, false), rec("shop/secret-one", "marketing", bench.OutcomePass, 1, true))

	ctx, out, _ := newTestContext(proj, false)
	err := runBench(ctx, []string{"compare", "mix/r1", "mix/r2"})
	if code := exitOf(err); code != 1 {
		t.Errorf("exit %d (%v)", code, err)
	}
	got := out.String()
	for _, want := range []string{
		"By workflow (A → B):",
		"product: no change · 1 tasks",
		"marketing: net negative · 1 tasks",
		"pass 100% → 0% · score 0.90 → 0.40",
		"wall 1m0s → 1m0s (+0%) · tokens 1.0k → 1.0k (+0%) · cost $1.00 → $1.00 (+0%)",
		"1 task(s) regressed: shop/marketing/post-tees",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "secret-one") {
		t.Errorf("a holdout task is named:\n%s", got)
	}
	ctx, out, _ = newTestContext(proj, true)
	_ = runBench(ctx, []string{"compare", "mix/r1", "mix/r2"})
	if !strings.Contains(out.String(), `"workflows": [`) || !strings.Contains(out.String(), `"verdict": "net negative"`) {
		t.Errorf("json:\n%s", out)
	}
}

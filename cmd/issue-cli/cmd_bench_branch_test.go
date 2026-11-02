package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/experiment"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// shopBranch commits a change to files on branch work/mkt of the shop
// repo, chdirs into it and returns the branch's sha.
func shopBranch(t *testing.T, proj *tracker.Project, files ...string) string {
	t.Helper()
	repo := proj.WorkDir
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("checkout", "-q", "-b", "work/mkt")
	for _, f := range files {
		p := filepath.Join(repo, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		old, _ := os.ReadFile(p)
		if err := os.WriteFile(p, append(old, []byte("# changed\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("commit", "-q", "-m", "feat: the change")
	t.Chdir(repo)
	return git("rev-parse", "HEAD")
}

// saveMixRuns writes runs r1 and r2 of benchmark mix: a product task and a
// marketing task, with outcomes in r2 as given.
func saveMixRuns(t *testing.T, ref, productB, marketingB string) {
	t.Helper()
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	rec := func(task, wf, outcome string) bench.TaskRecord {
		return bench.TaskRecord{Task: task, Workflow: wf, Outcome: outcome, Judge: -1, EvalScore: -1, Usage: bench.Usage{Known: true, Input: 1000, USD: 1}, WallSec: 60}
	}
	for _, r := range []struct{ id, ref, p, m string }{{"r1", "main", bench.OutcomePass, bench.OutcomeFail}, {"r2", ref, productB, marketingB}} {
		run := &bench.Run{ID: r.id, Benchmark: "mix", Started: at, Ended: at.Add(time.Hour), Stop: bench.StopDone, Setup: bench.Setup{Ref: r.ref}}
		at = at.Add(2 * time.Hour)
		if err := bench.Save(run, []bench.TaskRecord{rec("shop/calc", "product", r.p), rec("shop/marketing/post-tees", "marketing", r.m)}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBenchCompareBranchMarksTheWorkflowsItChanges(t *testing.T) {
	proj := shopProject(t)
	asWorker(t)
	shopBranch(t, proj, "workflows/marketing.yaml", "docs/notes.md", "issues/marketing/post-tees.md")
	// marketing (changed) improves; product (the control) regresses.
	saveMixRuns(t, "work/mkt", bench.OutcomeFail, bench.OutcomePass)

	ctx, out, _ := newTestContext(proj, false)
	_ = runBench(ctx, []string{"compare", "mix/r1", "mix/r2"})
	assertContains(t, out.String(), "Mark the changed ones: issue-cli bench compare mix/r1 mix/r2 --branch <ref>")

	ctx, out, _ = newTestContext(proj, false)
	_ = runBench(ctx, []string{"compare", "mix/r1", "mix/r2", "--branch", "work/mkt"})
	got := out.String()
	for _, want := range []string{
		"product: net negative · 1 tasks [control]",
		"marketing: net positive · 1 tasks [changed: workflows/marketing.yaml]",
		"Branch work/mkt: 1 not mapped to a workflow (docs/notes.md)",
		"⚠ control product is net negative: the runs are noisy or the change reaches it",
	} {
		assertContains(t, got, want)
	}
	if strings.Contains(got, "post-tees.md") {
		t.Errorf("a work record was mapped:\n%s", got)
	}

	ctx, out, _ = newTestContext(proj, true)
	_ = runBench(ctx, []string{"compare", "mix/r1", "mix/r2", "--branch", "work/mkt"})
	for _, want := range []string{`"branch": "work/mkt"`, `"mark": "changed"`, `"mark": "control"`, `"changed_by": [`, `"not_mapped": [`} {
		assertContains(t, out.String(), want)
	}

	ctx, _, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"compare", "mix/r1", "mix/r2", "--branch", "no-such-ref"}); err == nil || !strings.Contains(err.Error(), "--branch no-such-ref: no such ref") {
		t.Errorf("unknown ref: %v", err)
	}
}

func TestBenchCompareBranchEngineCodeReachesEveryWorkflow(t *testing.T) {
	proj := shopProject(t)
	asWorker(t)
	shopBranch(t, proj, "internal/tracker/workflow.go")
	saveMixRuns(t, "work/mkt", bench.OutcomePass, bench.OutcomePass)
	ctx, out, _ := newTestContext(proj, false)
	_ = runBench(ctx, []string{"compare", "mix/r1", "mix/r2", "--branch", "work/mkt"})
	assertContains(t, out.String(), "product: no change · 1 tasks [changed: internal/tracker/workflow.go]")
	assertContains(t, out.String(), "marketing: net positive · 1 tasks [changed: internal/tracker/workflow.go]")
}

func TestExperimentRecordKeepsTheVerdictOfEachWorkflow(t *testing.T) {
	proj := shopProject(t)
	asWorker(t)
	issuePath := filepath.Join(proj.IssueDir, "Factory", "exp.md")
	if err := os.MkdirAll(filepath.Dir(issuePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(issuePath, []byte("---\ntitle: \"Exp\"\nstatus: \"in progress\"\nsystem: \"Factory\"\nlabels:\n  - experiment\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha := shopBranch(t, proj, "workflows/marketing.yaml")
	saveMixRuns(t, sha, bench.OutcomePass, bench.OutcomePass)

	ctx, out, errOut := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "pass_rate:up:0.1", "--benchmark", "mix"}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	out.Reset()
	if err := runExperiment(ctx, []string{"record", "factory/exp", "--baseline", "r1", "--candidate", "r2"}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	got := out.String()
	assertContains(t, got, "Verdict: promising")
	assertContains(t, got, "By workflow (advice beside the verdict; the owner adopts):")
	assertContains(t, got, "product: no change · 1 tasks [control]")
	assertContains(t, got, "marketing: net positive · 1 tasks [changed: workflows/marketing.yaml]")

	issue := loadLeadIssue(t, proj, "factory/exp")
	b, err := experiment.Load(issue.FilePath)
	if err != nil || b == nil {
		t.Fatalf("block %v %v", b, err)
	}
	if len(b.Workflows) != 2 || b.Workflows[0].Workflow != "product" || b.Workflows[0].Mark != bench.MarkControl ||
		b.Workflows[1].Workflow != "marketing" || b.Workflows[1].Mark != bench.MarkChanged || b.Workflows[1].Verdict != bench.NetPositive {
		t.Fatalf("workflows %+v (note %q)", b.Workflows, b.WorkflowsNote)
	}
	if b.Verdict != experiment.Promising {
		t.Errorf("the overall verdict changed: %s", b.Verdict)
	}
	assertContains(t, issue.BodyRaw, "### By workflow")
	assertContains(t, issue.BodyRaw, "| marketing | changed (workflows/marketing.yaml) | net positive | 0.00 → 1.00 |")
	assertContains(t, issue.BodyRaw, "| product | control | no change | 1.00 → 1.00 |")
	comments, _ := tracker.LoadComments(issue.FilePath)
	if len(comments) != 1 || !strings.Contains(comments[0].Text, "workflows: product no change (control), marketing net positive (changed)") {
		t.Errorf("comments %+v", comments)
	}

	ctx, out, _ = newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"show", "factory/exp"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "marketing: net positive · 1 tasks [changed: workflows/marketing.yaml]")
}

func TestExperimentRecordWithoutACommitLeavesWorkflowsUnmarked(t *testing.T) {
	proj, root := makeExperimentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "gates_first_try:up:0.05", "--benchmark", "b1"}); err != nil {
		t.Fatal(err)
	}
	writeBenchRun(t, root, "base", 3, 0.6)
	writeBenchRun(t, root, "cand", 3, 0.7)
	t.Chdir(root) // not a git repository
	out.Reset()
	if err := runExperiment(ctx, []string{"record", "factory/exp", "--baseline", "base", "--candidate", "cand"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "product: no change · 4 tasks")
	assertContains(t, out.String(), "(not marked: marking the changed workflows needs a git repository")
	b, _ := experiment.Load(loadLeadIssue(t, proj, "factory/exp").FilePath)
	if len(b.Workflows) != 1 || b.Workflows[0].Mark != "" || !strings.HasPrefix(b.WorkflowsNote, "not marked:") {
		t.Errorf("workflows %+v note %q", b.Workflows, b.WorkflowsNote)
	}
}

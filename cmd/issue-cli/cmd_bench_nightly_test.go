package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/experiment"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const nightlyWordsQuote = "run the factory every night"

// makeNightlyProject is makeJobProject plus the Product entry's
// nightly-improve job, a benchmark b1 with a task in the project in a temp
// bench dir, and a git repo (cwd) with main and work/exp.
func makeNightlyProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := makeJobProject(t)
	proj.Product = &tracker.Department{Name: tracker.ProductName, Jobs: []tracker.Job{{
		ID: "nightly-improve", Name: "Nightly improve", Every: "day", At: "02:00", Brief: NightlyBrief, Overlap: true,
	}}}
	benchDir := filepath.Join(t.TempDir(), "bench")
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", benchDir)
	if err := os.MkdirAll(benchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(benchDir, "b1.yaml"), []byte("name: b1\ntasks:\n  - project: proj\n    slug: cli/x\n    base: aaa\n    merged: bbb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, "workflow.yaml")
	return proj
}

// standInRuns swaps the bench run for a stand-in that writes run.json:
// pass[i] tasks passed of 4 on the i-th call, stop "done" unless stops
// says otherwise. No worker, no model (AC8). It returns the calls.
func standInRuns(t *testing.T, pass []int, stops ...string) *[]string {
	t.Helper()
	var calls []string
	orig := nightlyBenchRun
	nightlyBenchRun = func(ctx *Context, args []string) error {
		i := len(calls)
		calls = append(calls, strings.Join(args, " "))
		var setup, label string
		for j := 0; j+1 < len(args); j++ {
			switch args[j] {
			case "--setup":
				setup = args[j+1]
			case "--label":
				label = args[j+1]
			}
		}
		passed := pass[min(i, len(pass)-1)]
		stop := bench.StopDone
		if i < len(stops) && stops[i] != "" {
			stop = stops[i]
		}
		now := ctx.now().Add(time.Duration(i) * time.Minute)
		r := &bench.Run{ID: fmt.Sprintf("r%d-%s", i, now.Format("0102")), Benchmark: args[0], Started: now, Ended: now.Add(time.Hour), Stop: stop,
			Setup: bench.Setup{Ref: setup, Label: label}}
		var recs []bench.TaskRecord
		for k := 0; k < 4; k++ {
			o := bench.OutcomeFail
			if k < passed {
				o = bench.OutcomePass
			}
			recs = append(recs, bench.TaskRecord{Task: fmt.Sprintf("proj/t%d", k), Project: "proj", Outcome: o, Judge: -1,
				WallSec: float64(600 + 60*k), Usage: bench.Usage{Known: true, CacheRead: int64(10_000_000 + k*1_000_000), Model: "claude-opus-5-5"}})
		}
		return bench.Save(r, recs)
	}
	t.Cleanup(func() { nightlyBenchRun = orig })
	return &calls
}

func runNightly(t *testing.T, proj *tracker.Project, at time.Time, args ...string) (string, error) {
	t.Helper()
	ctx, out, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return at }
	err := runBench(ctx, append([]string{"nightly"}, args...))
	return out.String(), err
}

// AC1: issue-cli jobs lists the bundled job on Product; job run --scheduled
// and bench nightly --run refuse without the owner's standing start.
func TestNightly_JobListedAndRefusedWithoutStandingStart(t *testing.T) {
	proj := makeNightlyProject(t)
	calls := standInRuns(t, []int{3})
	started := stubStart(t)

	ctx, out, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return jobNow }
	if err := runJobs(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "product/nightly-improve — Nightly improve") {
		t.Fatalf("jobs does not list the nightly job:\n%s", out.String())
	}
	_, err := runJobCmd(t, proj, "run", "product/nightly-improve", "--scheduled")
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "has no standing start") || len(*started) != 0 {
		t.Fatalf("scheduled without a standing start: %v, started %v", err, *started)
	}

	// The owner's run: a Product issue with the bundled brief naming itself.
	if _, err := runJobCmd(t, proj, "run", "product/nightly-improve", "--quote", "run it once"); err != nil {
		t.Fatal(err)
	}
	run := loadLeadIssue(t, proj, "product/nightly-improve-2026-10-29")
	if run.Field("department") != "" || run.Field("job") != "product/nightly-improve" {
		t.Fatalf("run fields: department %q job %q", run.Field("department"), run.Field("job"))
	}
	if !strings.Contains(run.BodyRaw, "issue-cli bench nightly --run product/nightly-improve-2026-10-29") || strings.Contains(run.BodyRaw, "{{") {
		t.Fatalf("brief:\n%s", run.BodyRaw)
	}
	// Its night refuses without the standing start: the owner's words to
	// spend a night are the standing start's.
	_, err = runNightly(t, proj, jobNow, "--run", run.Slug)
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "job auto on product/nightly-improve") || len(*calls) != 0 {
		t.Fatalf("night without a standing start: %v, calls %v", err, *calls)
	}

	if _, err := runJobCmd(t, proj, "auto", "on", "product/nightly-improve", "--quote", nightlyWordsQuote); err != nil {
		t.Fatal(err)
	}
	out2, err := runNightly(t, proj, jobNow, "--run", run.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], "--quote "+nightlyWordsQuote) {
		t.Fatalf("the night's run should carry the standing start's words: %q", *calls)
	}
	for _, want := range []string{"Last night: pass 3/4 (first night)", "issue-cli --project proj output add --issue " + run.Slug + " --kind report"} {
		if !strings.Contains(out2, want) {
			t.Errorf("night output misses %q:\n%s", want, out2)
		}
	}
	// One night per date.
	if _, err := runNightly(t, proj, jobNow, "--run", run.Slug); exitOf(err) != 1 || !strings.Contains(err.Error(), "already ran") {
		t.Fatalf("second night same date: %v", err)
	}
}

// A worker may start only its own run's night; an agent with neither --run
// nor --quote is refused.
func TestNightly_WhoMayStartIt(t *testing.T) {
	proj := makeNightlyProject(t)
	calls := standInRuns(t, []int{3})
	if _, err := runNightly(t, proj, jobNow); err == nil || !strings.Contains(err.Error(), "needs --run") {
		t.Fatalf("a lead without words: %v", err)
	}
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "factory/other")
	path := filepath.Join(proj.IssueDir, "Product", "nightly-improve-2026-10-29.md")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("---\ntitle: \"N\"\nstatus: \"backlog\"\nslug: \"nightly-improve-2026-10-29\"\njob: \"product/nightly-improve\"\nrun: \"2026-10-29\"\n---\n\nBody.\n"), 0o644)
	if _, err := runNightly(t, proj, jobNow, "--run", "product/nightly-improve-2026-10-29"); err == nil || !strings.Contains(err.Error(), "not your run") {
		t.Fatalf("a worker on another issue: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("ran: %v", *calls)
	}
}

// AC6: memory at the 50% line, or the load over the cores, holds the night:
// no bench run, the night recorded held with the reason. A baseline that
// stopped on budget runs no experiment.
func TestNightly_HeldByMemoryLoadAndBudget(t *testing.T) {
	proj := makeNightlyProject(t)
	calls := standInRuns(t, []int{3}, bench.StopBudget)
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 50, CanStart: true} }
	out, err := runNightly(t, proj, jobNow, "--quote", "tonight")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 || !strings.Contains(out, "held: memory 50% ≥ 50%") {
		t.Fatalf("memory 50%%: calls %v\n%s", *calls, out)
	}
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 31, CanStart: true} }
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 20, Cores: 14} }
	out, err = runNightly(t, proj, jobNow, "--quote", "tonight")
	if err != nil || len(*calls) != 0 || !strings.Contains(out, "held: load 20.0 over 14 cores") {
		t.Fatalf("load: %v calls %v\n%s", err, *calls, out)
	}
	nights := bench.LoadNights("b1")
	if len(nights) != 2 || nights[0].Held == "" || nights[1].Held == "" || nights[0].Run != "" {
		t.Fatalf("held nights not recorded: %+v", nights)
	}

	// Budget: the baseline stops, the queued experiment waits.
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 4, Cores: 14} }
	queueExperiment(t, proj)
	out, err = runNightly(t, proj, jobNow, "--quote", "tonight")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !strings.Contains(out, "skipped: the baseline did not finish (budget); it stays queued") {
		t.Fatalf("budget: calls %v\n%s", *calls, out)
	}
	b, _ := experiment.Load(loadLeadIssue(t, proj, "factory/exp").FilePath)
	if !b.Queue.Waiting() || b.Verdict != "" {
		t.Fatalf("experiment should stay queued: %+v", b.Queue)
	}
}

// queueExperiment makes factory/exp an experiment queued on work/exp by the
// CEO with the owner's words.
func queueExperiment(t *testing.T, proj *tracker.Project) {
	t.Helper()
	path := filepath.Join(proj.IssueDir, "Factory", "exp.md")
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("---\ntitle: \"Exp\"\nstatus: \"in progress\"\nsystem: \"Factory\"\nlabels:\n  - experiment\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "pass_rate:up:0.1", "--benchmark", "b1"}); err != nil {
		t.Fatal(err)
	}
	if err := runExperiment(ctx, []string{"queue", "factory/exp", "--ref", "work/exp"}); err == nil || !strings.Contains(err.Error(), "needs --quote") {
		t.Fatalf("a lead queueing without the owner's words: %v", err)
	}
	if err := runExperiment(ctx, []string{"queue", "factory/exp", "--ref", "work/exp", "--quote", "try it tonight"}); err != nil {
		t.Fatal(err)
	}
}

// AC3 + AC7: a queued experiment runs once against the night's baseline;
// its verdict is recorded and nothing adopts, parks or reverts it. The next
// night runs only the baseline.
func TestNightly_QueuedExperimentRunsOnceNotAdopted(t *testing.T) {
	proj := makeNightlyProject(t)
	calls := standInRuns(t, []int{2, 3, 2})
	queueExperiment(t, proj)
	mainSHA, _ := experiment.SHA(".", "main")

	out, err := runNightly(t, proj, jobNow, "--quote", "tonight")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !strings.Contains((*calls)[0], "--setup "+mainSHA) || !strings.Contains((*calls)[1], "--label experiment:factory/exp@") {
		t.Fatalf("calls %q", *calls)
	}
	issue := loadLeadIssue(t, proj, "factory/exp")
	b, _ := experiment.Load(issue.FilePath)
	if b.Verdict != experiment.Promising || b.Baseline.Run != bench.LoadNights("b1")[0].Run || b.Baseline.SHA != mainSHA {
		t.Fatalf("verdict %q baseline %+v", b.Verdict, b.Baseline)
	}
	if b.Adopted != nil || b.Queue.Ran != "2026-10-29" || issue.Status != "in progress" {
		t.Fatalf("adopted %+v queue %+v status %q", b.Adopted, b.Queue, issue.Status)
	}
	if !strings.Contains(out, "Last night: pass 2/4 (first night)") || !strings.Contains(out, "experiment factory/exp: promising") {
		t.Fatalf("night output:\n%s", out)
	}
	report, err := os.ReadFile(bench.NightReportPath("b1", "2026-10-29"))
	if err != nil || !strings.Contains(string(report), "Adopting it is the owner's call.") {
		t.Fatalf("morning report: %v\n%s", err, report)
	}

	*calls = nil
	if _, err := runNightly(t, proj, jobNow.AddDate(0, 0, 1), "--quote", "tonight"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("the experiment ran twice: %q", *calls)
	}
	nights := bench.LoadNights("b1")
	if len(nights) != 2 || nights[1].Experiment != nil {
		t.Fatalf("nights %+v", nights)
	}
	if b2, _ := experiment.Load(issue.FilePath); b2.Adopted != nil || b2.Verdict != experiment.Promising {
		t.Fatalf("second night changed the block: %+v", b2)
	}
}

// AC7: workers never queue an experiment.
func TestExperimentQueue_WorkerRefused(t *testing.T) {
	proj := makeNightlyProject(t)
	path := filepath.Join(proj.IssueDir, "Factory", "exp.md")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("---\ntitle: \"Exp\"\nstatus: \"in progress\"\nsystem: \"Factory\"\nlabels:\n  - experiment\n---\n\nBody.\n"), 0o644)
	ctx, _, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "pass_rate:up:0.1"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "factory/exp")
	if err := runExperiment(ctx, []string{"queue", "factory/exp", "--quote", "words"}); err == nil || !strings.Contains(err.Error(), "workers never queue") {
		t.Fatalf("worker queue: %v", err)
	}
	b, _ := experiment.Load(path)
	if b.Queue != nil {
		t.Fatalf("queued by a worker: %+v", b.Queue)
	}
}

// The bundled brief is there and lint's bundled list names it.
func TestNightlyBriefIsBundled(t *testing.T) {
	found := false
	for _, n := range jobs.Builtins() {
		found = found || n == "nightly-improve"
	}
	if !found {
		t.Fatalf("builtins %v", jobs.Builtins())
	}
}

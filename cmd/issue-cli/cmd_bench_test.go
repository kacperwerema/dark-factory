package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/bench"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// benchProject is a project repo with one done issue: base has a wrong
// Add, merged fixes it and adds its test.
func benchProject(t *testing.T) (proj *tracker.Project, base, merged string) {
	t.Helper()
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(t.TempDir(), "bench"))
	repo := filepath.Join(t.TempDir(), "calc")
	run := func(args ...string) string {
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
	run("init", "-q", "-b", "main")
	put("go.mod", "module example.com/calc\n\ngo 1.21\n")
	put("calc.go", "package calc\n\nfunc Add(a, b int) int { return a - b }\n")
	put("issues/calc/fix-add.md", "---\ntitle: \"Fix add\"\nstatus: \"done\"\nsystem: \"calc\"\ntype: \"bugfix\"\nassignee: \"agent-x\"\n---\n\nAdd subtracts.\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	base = run("rev-parse", "HEAD")
	put("calc.go", "package calc\n\nfunc Add(a, b int) int { return a + b }\n")
	put("add_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAddAdds(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"2+3\")\n\t}\n}\n")
	run("add", "-A")
	run("commit", "-q", "-m", "fix add")
	merged = run("rev-parse", "HEAD")
	return &tracker.Project{Name: "Calc", Slug: "calc", IssueDir: filepath.Join(repo, "issues"), WorkDir: repo}, base, merged
}

// asOwner makes issue-cli see the owner's own shell: no lead or worker
// signal, not under .worktrees/, not a Claude Code session.
func asOwner(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CLAUDECODE", "ISSUE_VIEWER_ISSUE_SLUG", "ISSUE_VIEWER_LEAD_SCOPE"} {
		t.Setenv(k, "")
	}
	origWD, origTmux := workingDir, currentTmuxNames
	workingDir = func() (string, error) { return "/home/owner", nil }
	currentTmuxNames = func() (string, string) { return "", "" }
	t.Cleanup(func() { workingDir, currentTmuxNames = origWD, origTmux })
}

// asWorker makes issue-cli see a dispatched worker.
func asWorker(t *testing.T) {
	t.Helper()
	asOwner(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "calc/other")
}

func TestBenchOwnerAddsATaskAndAgentsCannotChangeTheBenchmark(t *testing.T) {
	proj, base, merged := benchProject(t)
	asOwner(t)
	snap := filepath.Join(t.TempDir(), "snap.md")
	if err := os.WriteFile(snap, []byte("---\ntitle: \"Fix add\"\nstatus: \"done\"\nassignee: \"agent-x\"\n---\n\nAdd subtracts.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, out, _ := newTestContext(proj, false)
	if err := runBench(ctx, []string{"task", "add", "factory-v1", "calc/fix-add", "--base", base, "--merged", merged, "--snapshot", snap, "--check", "go vet ./..."}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "✓ Added calc/calc/fix-add to factory-v1") || !strings.Contains(out.String(), "1 Go tests") {
		t.Fatalf("task add said:\n%s", out)
	}
	b, err := bench.Load("factory-v1")
	if err != nil || len(b.Tasks) != 1 {
		t.Fatalf("benchmark = %+v, %v", b, err)
	}
	task := b.Tasks[0]
	if task.Base != base || task.Merged != merged || strings.Join(task.HiddenTests, ",") != "TestAddAdds" || task.Start != "in progress" || task.Type != "bugfix" {
		t.Fatalf("task = %+v", task)
	}
	data, err := os.ReadFile(bench.SnapshotPath(task))
	if err != nil || !strings.Contains(string(data), `status: "in progress"`) || strings.Contains(string(data), "assignee") {
		t.Fatalf("snapshot = %q, %v", data, err)
	}
	before, _ := os.ReadFile(b.Path())

	asWorker(t)
	for _, args := range [][]string{
		{"task", "add", "factory-v1", "calc/fix-add", "--base", base, "--merged", merged, "--snapshot", snap},
		{"task", "remove", "factory-v1", "calc/fix-add"},
		{"holdout", "factory-v1", "calc/fix-add", "on"},
		{"budget", "factory-v1", "--tokens", "5"},
	} {
		ctx, _, _ := newTestContext(proj, false)
		err := runBench(ctx, args)
		if err == nil || !strings.Contains(err.Error(), "only the owner changes the benchmark") {
			t.Errorf("bench %v from a worker: err = %v", args, err)
		}
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("CLAUDECODE", "1")
	ctx, _, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"holdout", "factory-v1", "calc/fix-add", "on"}); err == nil || !strings.Contains(err.Error(), "Claude Code session") {
		t.Errorf("holdout from a Claude Code session: err = %v", err)
	}
	if after, _ := os.ReadFile(b.Path()); string(after) != string(before) {
		t.Errorf("a refused command changed the benchmark:\n%s", after)
	}
	// Reading stays open to agents.
	ctx, out, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"budget", "factory-v1"}); err != nil || !strings.Contains(out.String(), "factory-v1 budget:") {
		t.Errorf("bench budget (read) from an agent: %v\n%s", err, out)
	}
	// A run from an agent session needs the owner's words.
	ctx, _, _ = newTestContext(proj, false)
	var usage *usageError
	if err := runBench(ctx, []string{"run", "factory-v1"}); !errors.As(err, &usage) || !strings.Contains(err.Error(), "--quote") {
		t.Errorf("bench run from an agent without --quote: err = %v", err)
	}
}

func TestBenchTaskAddRefusesATaskWhoseBaseOrMergedCheckFails(t *testing.T) {
	proj, base, merged := benchProject(t)
	asOwner(t)
	snap := filepath.Join(t.TempDir(), "snap.md")
	if err := os.WriteFile(snap, []byte("---\ntitle: \"Fix add\"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ check, want string }{
		{"grep -q 'a + b' calc.go", "fails at the base"},
		{"grep -q 'nothing like this' calc.go", "fails on the merged commit"},
	} {
		ctx, _, _ := newTestContext(proj, false)
		err := runBench(ctx, []string{"task", "add", "v", "calc/fix-add", "--base", base, "--merged", merged, "--snapshot", snap, "--check", tc.check})
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "not added") {
			t.Errorf("check %q: err = %v, want %q", tc.check, err, tc.want)
		}
	}
	if _, err := bench.Load("v"); !errors.Is(err, bench.ErrNoBenchmark) {
		t.Errorf("a refused task left a benchmark behind: %v", err)
	}
}

func TestBenchCandidatesWritesNothing(t *testing.T) {
	proj, _, _ := benchProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runBench(ctx, []string{"candidates", "--all"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "calc/fix-add") || !strings.Contains(out.String(), "no recorded branch head") {
		t.Errorf("candidates said:\n%s", out)
	}
	if _, err := os.Stat(bench.Dir()); !os.IsNotExist(err) {
		t.Errorf("candidates created %s", bench.Dir())
	}
}

func TestBenchReportAndCompareShowTheHoldoutOnlyAsACountAndExitByVerdict(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	asWorker(t)
	b := bench.New("v")
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	rec := func(task, outcome string, holdout bool) bench.TaskRecord {
		return bench.TaskRecord{Task: task, Outcome: outcome, Holdout: holdout, Judge: -1, Usage: bench.Usage{Known: true, Input: 10}}
	}
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	save := func(id string, records ...bench.TaskRecord) {
		r := &bench.Run{ID: id, Benchmark: "v", Started: at, Ended: at.Add(time.Hour), Stop: bench.StopDone}
		at = at.Add(2 * time.Hour)
		if err := bench.Save(r, records); err != nil {
			t.Fatal(err)
		}
	}
	save("r1", rec("p/a", bench.OutcomePass, false), rec("p/b", bench.OutcomeFail, false), rec("p/hidden-one", bench.OutcomePass, true))
	save("r2", rec("p/a", bench.OutcomeFail, false), rec("p/b", bench.OutcomePass, false), rec("p/hidden-one", bench.OutcomePass, true))
	save("r3", rec("p/a", bench.OutcomePass, false), rec("p/b", bench.OutcomePass, false), rec("p/hidden-one", bench.OutcomePass, true))

	ctx, out, _ := newTestContext(nil, false)
	if err := runBench(ctx, []string{"report", "v/latest"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "v/r3") || !strings.Contains(out.String(), "holdout: 1/1 passed") || strings.Contains(out.String(), "hidden-one") {
		t.Errorf("report:\n%s", out)
	}
	for _, tc := range []struct {
		a, b string
		code int
		want string
	}{
		{"v/r1", "v/r2", 1, "B regresses"},
		{"v/r1", "v/r3", 0, "no regression"},
	} {
		ctx, out, _ := newTestContext(nil, false)
		err := runBench(ctx, []string{"compare", tc.a, tc.b})
		if code := exitOf(err); code != tc.code {
			t.Errorf("compare %s %s: exit %d (%v), want %d", tc.a, tc.b, code, err, tc.code)
		}
		if !strings.Contains(out.String(), tc.want) || strings.Contains(out.String(), "hidden-one") {
			t.Errorf("compare %s %s:\n%s", tc.a, tc.b, out)
		}
	}
	// An unfinished run can't be compared.
	r := &bench.Run{ID: "r4", Benchmark: "v", Started: at, Stop: bench.StopPaused}
	if err := bench.Save(r, []bench.TaskRecord{rec("p/a", bench.OutcomePass, false)}); err != nil {
		t.Fatal(err)
	}
	ctx, out, _ = newTestContext(nil, true)
	err := runBench(ctx, []string{"compare", "v/r1", "v/r4"})
	if code := exitOf(err); code != 2 || !strings.Contains(out.String(), `"comparable": false`) {
		t.Errorf("compare with a paused run: exit %d\n%s", code, out)
	}
}

func TestBenchIssueCLIBuildsTheSetupRef(t *testing.T) {
	engine := filepath.Join(t.TempDir(), "engine")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", engine}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	main := filepath.Join(engine, "cmd", "issue-cli", "main.go")
	if err := os.MkdirAll(filepath.Dir(main), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(engine, "go.mod"), []byte("module example.com/engine\n\ngo 1.21\n"), 0o644)
	os.WriteFile(main, []byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"issue-cli A\") }\n"), 0o644)
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-q", "-m", "A")
	a := git("rev-parse", "HEAD")
	os.WriteFile(main, []byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"issue-cli B\") }\n"), 0o644)
	git("commit", "-q", "-am", "B")

	var log strings.Builder
	cli, ref, err := benchIssueCLI(t.Context(), a[:10], engine, t.TempDir(), &log)
	if err != nil {
		t.Fatal(err)
	}
	if ref != a {
		t.Errorf("ref = %s, want %s", ref, a)
	}
	if v := cliVersion(cli); v != "issue-cli A" {
		t.Errorf("built issue-cli prints %q, want the setup ref's", v)
	}
	if _, _, err := benchIssueCLI(t.Context(), "no-such-ref", engine, t.TempDir(), &log); err == nil {
		t.Error("an unknown ref must fail")
	}
}

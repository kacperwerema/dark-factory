package main

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
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/slowtier"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
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

// TestBenchCandidatesLeavesOutTestsWithoutGoTestsAndNamesRisks: a done
// issue whose only test file holds no Go test is judged by its check alone,
// so it is left out with why; a proposed one names its risks and tests.
func TestBenchCandidatesLeavesOutTestsWithoutGoTestsAndNamesRisks(t *testing.T) {
	proj, base, merged := benchProject(t)
	repo := proj.WorkDir
	// The issues' work starts at t0; their commits come after it, the
	// fixture's own before.
	t0 := time.Now().Add(time.Hour)
	at := t0
	git := func(args ...string) string {
		t.Helper()
		at = at.Add(time.Minute)
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_AUTHOR_DATE="+at.Format(time.RFC3339), "GIT_COMMITTER_DATE="+at.Format(time.RFC3339))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// fix-add gets a tmux test and an owner box; js-only adds a non-Go test.
	if err := os.WriteFile(filepath.Join(repo, "add_test.go"), []byte("package calc\n\nimport \"testing\"\n\nfunc TestAddAdds(t *testing.T) {\n\t_ = \"tmux\"\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"2+3\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-qam", "tmux in the test")
	merged = git("rev-parse", "HEAD")
	t1 := at.Add(30 * time.Second)
	if err := os.WriteFile(filepath.Join(repo, "ui.test.js"), []byte("test('x', () => {})\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "js test")
	jsHead := git("rev-parse", "HEAD")
	fix := filepath.Join(proj.IssueDir, "calc", "fix-add.md")
	if err := os.WriteFile(fix, []byte("---\ntitle: \"Fix add\"\nstatus: \"done\"\nsystem: \"calc\"\ntype: \"bugfix\"\n---\n\n- [ ] (owner) The owner tries it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	js := filepath.Join(proj.IssueDir, "calc", "js-only.md")
	if err := os.WriteFile(js, []byte("---\ntitle: \"Js only\"\nstatus: \"done\"\nsystem: \"calc\"\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stats := func(path string, start time.Time, from, head string) {
		t.Helper()
		s := tracker.StatsStore{Transitions: []tracker.TransitionStat{
			{To: "in progress", TS: start, BranchHead: from},
			{To: "done", TS: at, BranchHead: head, MergeSHA: head},
		}}
		s.Dispatches = []tracker.Dispatch{{At: start}}
		if err := tracker.SaveStats(path, s); err != nil {
			t.Fatal(err)
		}
	}
	stats(fix, t0, base, merged)
	stats(js, t1, merged, jsHead)

	ctx, out, _ := newTestContext(proj, false)
	if err := runBench(ctx, []string{"candidates", "--all"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"risk: tmux, owner steps", "- calc/js-only  no Go tests in its test files"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("candidates --all lacks %q:\n%s", want, out)
		}
	}
	ctx, out, _ = newTestContext(proj, true)
	if err := runBench(ctx, []string{"candidates"}); err != nil {
		t.Fatal(err)
	}
	var cands []bench.Candidate
	if err := json.Unmarshal(out.Bytes(), &cands); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(cands) != 1 || cands[0].Slug != "calc/fix-add" || cands[0].System != "calc" || strings.Join(cands[0].TestNames, ",") != "TestAddAdds" {
		t.Errorf("candidates --json = %+v", cands)
	}
	ctx, out, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"candidates"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"about 10", "bench run <benchmark> --agents claude,codex"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("candidates lacks %q:\n%s", want, out)
		}
	}

	// A branch over the file cap is left out with why.
	for i := range bench.MaxCandidateFiles + 1 {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("wide%d.go", i)), []byte("package calc\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "wide_test.go"), []byte("package calc\n\nimport \"testing\"\n\nfunc TestWide(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t2 := at.Add(30 * time.Second)
	git("add", "-A")
	git("commit", "-qm", "wide")
	wide := filepath.Join(proj.IssueDir, "calc", "wide.md")
	if err := os.WriteFile(wide, []byte("---\ntitle: \"Wide\"\nstatus: \"done\"\nsystem: \"calc\"\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stats(wide, t2, jsHead, git("rev-parse", "HEAD"))
	ctx, out, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"candidates", "--all"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "files (over 15)") {
		t.Errorf("candidates --all:\n%s", out)
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
	cli, ref, sha, err := benchIssueCLI(t.Context(), a[:10], engine, t.TempDir(), &log)
	if err != nil {
		t.Fatal(err)
	}
	if ref != a || sha != a {
		t.Errorf("ref = %s, sha = %s, want both %s (the run record's setup.sha)", ref, sha, a)
	}
	if v := cliVersion(cli); v != "issue-cli A" {
		t.Errorf("built issue-cli prints %q, want the setup ref's", v)
	}
	if _, _, _, err := benchIssueCLI(t.Context(), "no-such-ref", engine, t.TempDir(), &log); err == nil {
		t.Error("an unknown ref must fail")
	}
}

// replayCandidateProject: the bench project with one done issue,
// calc/fix-add, whose own commit fixes a bug its hidden test catches on the
// base: the one candidate bench candidates lists. git runs in its repo.
func replayCandidateProject(t *testing.T) (*tracker.Project, func(time.Time, ...string) string) {
	t.Helper()
	proj, _, _ := benchProject(t)
	repo := proj.WorkDir
	t0 := time.Now().Add(time.Hour)
	git := func(at time.Time, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if !at.IsZero() {
			cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+at.Format(time.RFC3339), "GIT_COMMITTER_DATE="+at.Format(time.RFC3339))
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// A base from before the issue started, then the issue's own fix.
	if err := os.WriteFile(filepath.Join(repo, "calc.go"), []byte("package calc\n\nfunc Add(a, b int) int { return a - b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(time.Time{}, "rm", "-q", "add_test.go")
	git(time.Time{}, "commit", "-qam", "back to the bug")
	base := git(time.Time{}, "rev-parse", "HEAD")
	git(time.Time{}, "checkout", "-q", "HEAD~1", "--", "calc.go", "add_test.go")
	git(t0.Add(time.Minute), "commit", "-qm", "fix add")
	merged := git(time.Time{}, "rev-parse", "HEAD")
	fix := filepath.Join(proj.IssueDir, "calc", "fix-add.md")
	s := tracker.StatsStore{Transitions: []tracker.TransitionStat{
		{To: "in progress", TS: t0, BranchHead: base},
		{To: "done", TS: t0.Add(2 * time.Minute), BranchHead: merged, MergeSHA: merged},
	}, Dispatches: []tracker.Dispatch{{At: t0}}}
	if err := tracker.SaveStats(fix, s); err != nil {
		t.Fatal(err)
	}

	return proj, git
}

// TestBenchCandidatesCheckReplaysWithoutModelOrBenchmarkWrites: --check
// labels the proposed candidate solvable with why, leaves no worktree or
// temp folder behind, never runs a model (claude on PATH is a tripwire)
// and writes no benchmark file.
//
// Slow tier only: a whole replay (worktrees, go test twice) on top of the
// bench project; internal/bench's TestReplay* prove the replay itself.
func TestBenchCandidatesCheckReplaysWithoutModelOrBenchmarkWrites(t *testing.T) {
	slowtier.Only(t, "bench candidates --replay end to end; internal/bench TestReplay* and TestBenchCandidatesNeedReplayForSlugs run in validate")
	proj, git := replayCandidateProject(t)
	bin := t.TempDir()
	tripwire := filepath.Join(bin, "model-called")
	for _, name := range []string{"claude", "codex"} {
		standin.Write(t, filepath.Join(bin, name), "touch \"${0%/*}/model-called\"\n")
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	orig := replayMemGuard
	replayMemGuard = func(io.Writer) func() (bool, string) { return nil }
	t.Cleanup(func() { replayMemGuard = orig })

	ctx, out, errb := newTestContext(proj, false)
	if err := runBench(ctx, []string{"candidates", "--replay"}); err != nil {
		t.Fatal(err)
	}
	if want := "replay: solvable — 1 hidden tests fail on base and pass with its 1 own commits"; !strings.Contains(out.String(), want) {
		t.Errorf("candidates --replay lacks %q:\n%s\nstderr:\n%s", want, out, errb)
	}
	if !strings.Contains(errb.String(), "replaying calc/fix-add (1/1)") {
		t.Errorf("no progress on stderr:\n%s", errb)
	}
	ctx, out, _ = newTestContext(proj, true)
	if err := runBench(ctx, []string{"candidates", "--replay", "calc/fix-add"}); err != nil {
		t.Fatal(err)
	}
	var cands []bench.Candidate
	if err := json.Unmarshal(out.Bytes(), &cands); err != nil || len(cands) != 1 || cands[0].Replay != bench.ReplaySolvable || cands[0].ReplayWhy == "" {
		t.Errorf("candidates --replay --json = %+v (%v)\n%s", cands, err, out)
	}
	if wts := git(time.Time{}, "worktree", "list", "--porcelain"); strings.Count(wts, "worktree ") != 1 {
		t.Errorf("--replay left a worktree behind:\n%s", wts)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("--replay left %d entries in TMPDIR", len(left))
	}
	if _, err := os.Stat(tripwire); err == nil {
		t.Errorf("--replay called a model")
	}
	if _, err := os.Stat(bench.Dir()); !os.IsNotExist(err) {
		t.Errorf("--replay created %s", bench.Dir())
	}
}

// TestBenchCandidatesNeedReplayForSlugs: slugs without --replay are a usage
// error, and without --replay the footer names it. The fast-tier half of
// TestBenchCandidatesCheckReplaysWithoutModelOrBenchmarkWrites.
func TestBenchCandidatesNeedReplayForSlugs(t *testing.T) {
	proj, _ := replayCandidateProject(t)
	ctx, _, _ := newTestContext(proj, false)
	var usage *usageError
	if err := runBench(ctx, []string{"candidates", "calc/fix-add"}); !errors.As(err, &usage) {
		t.Errorf("candidates <slug> without --replay: err = %v", err)
	}
	ctx, out, _ := newTestContext(proj, false)
	if err := runBench(ctx, []string{"candidates"}); err != nil || !strings.Contains(out.String(), "bench candidates --replay") {
		t.Errorf("candidates footer lacks the --replay hint (%v):\n%s", err, out)
	}
}

func TestReplayGuardBlocksAtSeventyPercent(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		want bool
	}{
		{"ok: memory used 9% (< 75%)", false},
		{"ok: memory used 70% (< 75%)", true},
		{"BLOCKED: memory used 80% (>= 75%)", true},
	} {
		blocked, _ := replayGuard(func() (bool, string) { return strings.HasPrefix(tc.msg, "BLOCKED"), tc.msg })()
		if blocked != tc.want {
			t.Errorf("%q: blocked = %v, want %v", tc.msg, blocked, tc.want)
		}
	}
	if replayGuard(nil) != nil {
		t.Error("no mem-guard should mean no guard")
	}
}

// The budget line names the per-task default when no tokens are set, and a
// cap under one task (cache reads included) gets a note.
func TestBenchBudgetLineShowsTheCacheReadDefault(t *testing.T) {
	b := &bench.Benchmark{Name: "b", Tasks: []bench.Task{{}, {}}}
	if got := budgetLine(b, len(b.Tasks)); !strings.Contains(got, "tokens 50000000 (default: 25M a task × 2, cache reads included)") {
		t.Errorf("unset: %s", got)
	}
	b.Budget.Tokens = 6_000_000
	if got := budgetLine(b, len(b.Tasks)); !strings.HasPrefix(got, "tokens 6000000,") {
		t.Errorf("set: %s", got)
	}
	if note := lowTokensNote(b.Budget); !strings.Contains(note, "6M tokens is under one task") {
		t.Errorf("6M note = %q", note)
	}
	b.Budget.Tokens = 250_000_000
	if note := lowTokensNote(b.Budget); note != "" {
		t.Errorf("250M note = %q", note)
	}
}

func TestBenchReportShowsTheRunsWorkerHome(t *testing.T) {
	var out bytes.Buffer
	printRun(&out, &bench.Run{Benchmark: "b", ID: "r1", Setup: bench.Setup{WorkerHome: bench.WorkerHomeSandbox}})
	if !strings.Contains(out.String(), "worker home sandbox") {
		t.Errorf("report:\n%s", out.String())
	}
	out.Reset()
	printRun(&out, &bench.Run{Benchmark: "b", ID: "r0"})
	if !strings.Contains(out.String(), "worker home owner") {
		t.Errorf("an old run's report:\n%s", out.String())
	}
}

// bench run costs with the prices the rooms' spend reads: a prices.yaml key
// beats the benchmark file's and prefix keys arrive whole, so one model has
// one cost in both.
func TestBenchRunPricesThroughTheOneLoader(t *testing.T) {
	asOwner(t)
	cfg := t.TempDir()
	benchDir := filepath.Join(cfg, "bench")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", benchDir)
	if err := os.MkdirAll(benchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(spend.PricesPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spend.PricesPath(), []byte("prices:\n  claude-opus-5: {in: 3, out: 15}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := "name: b1\nprices:\n  claude-opus-5: {in: 99, out: 99}\n  claude-own: {in: 1, out: 2}\ntasks:\n  - project: calc\n    slug: calc/x\n    base: aaa\n    merged: bbb\n"
	if err := os.WriteFile(filepath.Join(benchDir, "b1.yaml"), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}

	var got map[string]bench.Price
	orig := benchExecute
	benchExecute = func(_ context.Context, b *bench.Benchmark, _ bench.Env, _ bench.Options) (*bench.Run, error) {
		got = b.Prices
		return nil, errors.New("no worker in this test")
	}
	t.Cleanup(func() { benchExecute = orig })
	// cliVersion runs the CLI: left alone that is this test binary, which
	// would run the whole suite again.
	origCLI := benchCLIFor
	benchCLIFor = func(context.Context, string, string, string, io.Writer) (string, string, string, error) {
		return "/usr/bin/true", "", "", nil
	}
	t.Cleanup(func() { benchCLIFor = origCLI })

	ctx, _, _ := newTestContext(&tracker.Project{Name: "Calc", Slug: "calc", IssueDir: t.TempDir()}, false)
	if err := runBench(ctx, []string{"run", "b1"}); err == nil || !strings.Contains(err.Error(), "no worker in this test") {
		t.Fatalf("bench run = %v", err)
	}
	if !reflect.DeepEqual(got, spend.LoadPrices()) {
		t.Fatalf("bench run prices %+v, spend %+v", got, spend.LoadPrices())
	}
	if p, ok := bench.PriceFor("claude-opus-5-5", got); !ok || p.In != 3 {
		t.Errorf("claude-opus-5-5 = %+v %v, want the prices.yaml price", p, ok)
	}
	if got["claude-own"].In != 1 {
		t.Errorf("the benchmark's own model lost its price: %+v", got)
	}
}

// abProject is a project whose routing: sends bugfixes to claude.
func abProject(t *testing.T) *tracker.Project {
	t.Helper()
	dir := t.TempDir()
	wf := filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(wf, []byte("statuses:\n  - name: backlog\n  - name: done\nrouting:\n  default: claude\n  rules:\n    - {type: bugfix, agent: claude}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{Slug: "orchard", IssueDir: filepath.Join(dir, "issues"), WorkflowFile: wf}
}

func saveABRun(t *testing.T, id string, at time.Time, agents string) {
	t.Helper()
	rec := func(task, agent, outcome string, usd float64) bench.TaskRecord {
		return bench.TaskRecord{Task: "orchard/" + task, Type: "bugfix", Agent: agent, Outcome: outcome, Judge: -1, WallSec: 90,
			Usage: bench.Usage{Known: true, Input: 2000, USD: usd, USDKnown: usd > 0, Model: agent + "-model"}}
	}
	var records []bench.TaskRecord
	for _, task := range []string{"a", "b", "c"} {
		records = append(records, rec(task, "claude", bench.OutcomeFail, 0.5), rec(task, "codex", bench.OutcomePass, 0))
	}
	records = append(records, bench.TaskRecord{Task: "orchard/hidden-one", Type: "bugfix", Agent: "codex", Holdout: true, Outcome: bench.OutcomePass, Judge: -1})
	r := &bench.Run{ID: id, Benchmark: "ab", Started: at, Ended: at.Add(time.Hour), Stop: bench.StopDone, Setup: bench.Setup{Agent: agents}}
	if err := bench.Save(r, records); err != nil {
		t.Fatal(err)
	}
}

func TestBenchCompareOfATwoAgentRunPrintsTheTableAndTheRuleItContradicts(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	asWorker(t)
	proj := abProject(t)
	at := time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
	saveABRun(t, "r1", at, "claude,codex")
	saveABRun(t, "r2", at.Add(2*time.Hour), "claude,codex")
	ctx, out, _ := newTestContext(proj, false)
	if err := runBench(ctx, []string{"compare", "ab/r1"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"By agent", "all", "bugfix", "0/3 0%", "3/3 100%", "cost unknown", "$0.50",
		"Routing: type bugfix → claude contradicted: codex passed 3/3, claude 0/3",
		"no prices.yaml at", "codex-model", "no price is ever guessed",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("compare lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "hidden-one") {
		t.Errorf("a holdout task is named:\n%s", s)
	}
	// Two two-agent runs: the verdict task by task, and the table.
	ctx, out, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"compare", "ab/r1", "ab/r2"}); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "no regression") || !strings.Contains(s, "orchard/a@codex") || !strings.Contains(s, "Routing: type bugfix") {
		t.Errorf("compare a b:\n%s", s)
	}
	ctx, out, _ = newTestContext(proj, true)
	if err := runBench(ctx, []string{"compare", "ab/r1", "ab/r2"}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		ByAgent struct {
			Agents []string `json:"agents"`
		} `json:"by_agent"`
		Routing []struct {
			Verdict string `json:"verdict"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ByAgent.Agents) != 2 || len(got.Routing) != 1 || got.Routing[0].Verdict != bench.RoutingContradicted {
		t.Errorf("json: %s", out)
	}
	// report shows it too.
	ctx, out, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"report", "ab/r1"}); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "By agent") || !strings.Contains(s, "orchard/a@codex") || !strings.Contains(s, "cost unknown") {
		t.Errorf("report:\n%s", s)
	}
}

func TestBenchCompareOfAOneAgentRunNamesTheFlag(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	asWorker(t)
	r := &bench.Run{ID: "r1", Benchmark: "solo", Stop: bench.StopDone, Setup: bench.Setup{Agent: "claude"}}
	if err := bench.Save(r, []bench.TaskRecord{{Task: "p/a", Outcome: bench.OutcomePass}}); err != nil {
		t.Fatal(err)
	}
	ctx, out, _ := newTestContext(nil, false)
	if err := runBench(ctx, []string{"compare", "solo/r1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--agents claude,codex") {
		t.Errorf("out:\n%s", out)
	}
}

func TestBenchRunAgentsFlag(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	asOwner(t)
	b := bench.New("flagged")
	b.Tasks = []bench.Task{{Project: "orchard", Slug: "a"}}
	b.Workers = map[string]string{"claude": "sh", "codex": "sh", "missing": "no-such-command-here"}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	var got bench.Options
	var worker string
	saveExec, saveCLI := benchExecute, benchCLIFor
	t.Cleanup(func() { benchExecute, benchCLIFor = saveExec, saveCLI })
	benchExecute = func(_ context.Context, bm *bench.Benchmark, _ bench.Env, opt bench.Options) (*bench.Run, error) {
		got, worker = opt, bm.Worker
		return &bench.Run{ID: "r", Benchmark: bm.Name, Stop: bench.StopDone}, nil
	}
	benchCLIFor = func(context.Context, string, string, string, io.Writer) (string, string, string, error) {
		return "issue-cli", "", "", nil
	}
	run := func(args ...string) (string, error) {
		ctx, _, errb := newTestContext(nil, false)
		err := runBench(ctx, append([]string{"run", "flagged"}, args...))
		return errb.String(), err
	}
	log, err := run("--agents", "claude, codex")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Agents, []string{"claude", "codex"}) || got.Setup.Agent != "claude,codex" {
		t.Errorf("options = %+v", got)
	}
	if !strings.Contains(log, "1 tasks on claude,codex (2 in all)") || !strings.Contains(log, "× 2") {
		t.Errorf("log:\n%s", log)
	}
	if _, err := run("--agents", "codex"); err != nil {
		t.Fatal(err)
	}
	if len(got.Agents) != 0 || got.Setup.Agent != "codex" || worker != "sh" {
		t.Errorf("one agent: %+v worker %q", got, worker)
	}
	got = bench.Options{}
	if _, err := run("--agents", "claude,codex", "--worker", "x"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("both flags: %v", err)
	}
	if _, err := run("--agents", "claude,missing"); err == nil || !strings.Contains(err.Error(), "not on PATH") {
		t.Errorf("missing command: %v", err)
	}
	if _, err := run("--agents", "claude,gemini"); err == nil || !strings.Contains(err.Error(), "workers:") {
		t.Errorf("unknown agent: %v", err)
	}
	if got.Setup.Agent != "" {
		t.Errorf("a refused run started: %+v", got)
	}
}

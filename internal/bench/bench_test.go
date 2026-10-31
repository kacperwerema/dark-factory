package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/health"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// fixture is a project repo with one golden task: base has a wrong Add,
// merged fixes it and adds add_test.go (the hidden test).
type fixture struct {
	repo, base, merged string
	proj               *tracker.Project
	task               Task
}

const fixtureWorkflow = `statuses:
  - name: backlog
  - name: in progress
  - name: testing
  - name: done
`

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(t.TempDir(), "bench"))
	f := &fixture{repo: filepath.Join(t.TempDir(), "proj")}
	mustGit(t, filepath.Dir(f.repo), "init", "-q", "-b", "main", f.repo)
	write(t, filepath.Join(f.repo, "go.mod"), "module example.com/calc\n\ngo 1.21\n")
	write(t, filepath.Join(f.repo, "calc.go"), "package calc\n\nfunc Add(a, b int) int { return a - b }\n")
	write(t, filepath.Join(f.repo, "workflow.yaml"), fixtureWorkflow)
	write(t, filepath.Join(f.repo, "issues", "calc", "fix-add.md"), "---\ntitle: \"Fix add\"\nstatus: \"in progress\"\nsystem: \"calc\"\n---\n\nAdd subtracts.\n")
	mustGit(t, f.repo, "add", "-A")
	mustGit(t, f.repo, "commit", "-q", "-m", "base")
	f.base = mustGit(t, f.repo, "rev-parse", "HEAD")
	write(t, filepath.Join(f.repo, "calc.go"), "package calc\n\nfunc Add(a, b int) int { return a + b }\n")
	write(t, filepath.Join(f.repo, "add_test.go"), "package calc\n\nimport \"testing\"\n\nfunc TestAddAdds(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"2+3\")\n\t}\n}\n")
	mustGit(t, f.repo, "add", "-A")
	mustGit(t, f.repo, "commit", "-q", "-m", "fix add")
	f.merged = mustGit(t, f.repo, "rev-parse", "HEAD")
	f.proj = &tracker.Project{Name: "Calc", Slug: "calc", IssueDir: filepath.Join(f.repo, "issues"), WorkflowFile: filepath.Join(f.repo, "workflow.yaml"), WorkDir: f.repo}
	f.task = Task{Project: "calc", Slug: "calc/fix-add", Type: "bugfix", Base: f.base, Merged: f.merged, Start: "in progress",
		Hidden: []string{"add_test.go"}, HiddenTests: []string{"TestAddAdds"}}
	write(t, SnapshotPath(f.task), "---\ntitle: \"Fix add\"\nstatus: \"in progress\"\nsystem: \"calc\"\n---\n\nAdd subtracts.\n")
	return f
}

// fakeCLI answers dispatch-prompt --json like issue-cli and logs nothing.
func fakeCLI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bin", "issue-cli")
	write(t, path, "#!/bin/sh\necho '{\"prompt\":\"Fix it.\",\"slug\":\"calc/fix-add\"}'\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fixWorker writes the right Add, records what it saw, and prints a
// Claude Code JSON result.
const fixWorker = `cat > prompt.seen; git remote > remotes.seen; printf 'package calc\n\nfunc Add(a, b int) int { return a + b }\n' > calc.go; ` +
	`echo '{"type":"result","session_id":"s1","num_turns":3,"total_cost_usd":0.25,"usage":{"input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":3000,"cache_creation_input_tokens":0},"modelUsage":{"claude-opus-5-5":{}}}'`

func (f *fixture) env(t *testing.T) Env {
	return Env{
		Projects: func(slug string) *tracker.Project {
			if slug == f.proj.Slug {
				return f.proj
			}
			return nil
		},
		IssueCLI: fakeCLI(t), TmpRoot: t.TempDir(), Poll: 50 * time.Millisecond, MemPoll: 10 * time.Millisecond,
	}
}

func (f *fixture) benchmark(worker string, tasks ...Task) *Benchmark {
	b := New("t")
	b.Worker = worker
	b.Tasks = tasks
	return b
}

func TestRunPassesATaskTheWorkerSolves(t *testing.T) {
	f := newFixture(t)
	env := f.env(t)
	env.Keep = true
	run, err := Execute(context.Background(), f.benchmark(fixWorker, f.task), env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Stop != StopDone || len(run.Tasks) != 1 {
		t.Fatalf("run = %+v", run)
	}
	r := run.Tasks[0]
	if r.Outcome != OutcomePass || r.HiddenPassed != 1 || r.HiddenTotal != 1 || !r.CheckOK {
		t.Fatalf("record = %+v", r)
	}
	if !r.Usage.Known || r.Usage.Tokens() != 4200 || r.Usage.USD != 0.25 || r.Usage.Model != "claude-opus-5-5" {
		t.Fatalf("usage = %+v", r.Usage)
	}
	if run.Summary.PassRate != 1 || run.SpentTokens != 4200 {
		t.Fatalf("summary = %+v spent %d", run.Summary, run.SpentTokens)
	}
	// The sandbox: a clone with no remote, the prompt with the bench note.
	sb := filepath.Join(env.TmpRoot, run.ID, "01", "repo")
	if data, _ := os.ReadFile(filepath.Join(sb, "remotes.seen")); strings.TrimSpace(string(data)) != "" {
		t.Errorf("the worker saw remotes %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(sb, "prompt.seen")); !strings.Contains(string(data), "Fix it.") || !strings.Contains(string(data), "Benchmark run") {
		t.Errorf("prompt = %q", data)
	}
	// Nothing changed in the real repo.
	if head := mustGit(t, f.repo, "rev-parse", "HEAD"); head != f.merged {
		t.Errorf("real repo HEAD moved to %s", head)
	}
	if st := mustGit(t, f.repo, "status", "--porcelain"); st != "" {
		t.Errorf("real repo changed:\n%s", st)
	}
	saved, err := LoadRun("t", run.ID)
	if err != nil || saved.Tasks[0].Outcome != OutcomePass {
		t.Fatalf("run.json = %+v, %v", saved, err)
	}
}

func TestRunFailsATaskTheWorkerDoesNotSolve(t *testing.T) {
	f := newFixture(t)
	run, err := Execute(context.Background(), f.benchmark("cat > /dev/null", f.task), f.env(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if r.Outcome != OutcomeFail || r.HiddenPassed != 0 || r.HiddenTotal != 1 {
		t.Fatalf("record = %+v", r)
	}
	if r.Usage.Known {
		t.Errorf("usage with no result and no transcript must be unknown, got %+v", r.Usage)
	}
	if run.Summary.TokensKnown {
		t.Error("summary says tokens known with an unknown task")
	}
}

func TestRunMarksABaseThatFailsItsCheckBrokenAndLeavesItOutOfThePassRate(t *testing.T) {
	f := newFixture(t)
	broken := f.task
	broken.Slug = "calc/broken"
	broken.Check = "exit 1"
	write(t, SnapshotPath(broken), "---\ntitle: \"Broken\"\nstatus: \"in progress\"\nsystem: \"calc\"\n---\n")
	run, err := Execute(context.Background(), f.benchmark(fixWorker, f.task, broken), f.env(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[1].Outcome != OutcomeBroken {
		t.Fatalf("broken task = %+v", run.Tasks[1])
	}
	if run.Summary.Scored != 1 || run.Summary.PassRate != 1 {
		t.Fatalf("summary = %+v", run.Summary)
	}
}

func TestRunStopsATaskAtItsWallCap(t *testing.T) {
	f := newFixture(t)
	b := f.benchmark("sleep 30", f.task)
	b.Budget.TaskWall = "300ms"
	start := time.Now()
	run, err := Execute(context.Background(), b, f.env(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].Outcome != OutcomeTimeout {
		t.Fatalf("record = %+v", run.Tasks[0])
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("the worker was not stopped: %s", d)
	}
}

func TestRunStopsWhenTheTokenBudgetIsSpent(t *testing.T) {
	f := newFixture(t)
	second := f.task
	second.Slug = "calc/again"
	write(t, SnapshotPath(second), "---\ntitle: \"Again\"\nstatus: \"in progress\"\nsystem: \"calc\"\n---\n")
	b := f.benchmark(fixWorker, f.task, second)
	b.Budget.Tokens = 4000
	run, err := Execute(context.Background(), b, f.env(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Stop != StopBudget || run.Tasks[1].Outcome != OutcomeBudget {
		t.Fatalf("run stop %s, tasks %+v", run.Stop, run.Tasks)
	}
	if !strings.Contains(run.StopNote, "token budget") {
		t.Errorf("stop note = %q", run.StopNote)
	}
}

func TestRunStopsAWorkerMidTaskWhenItsTranscriptSpendsTheBudget(t *testing.T) {
	f := newFixture(t)
	env := f.env(t)
	env.Home = t.TempDir()
	// The worker writes a transcript line over the budget, then hangs.
	worker := `d="$HOME_T/.claude/projects/$(pwd | sed 's/[^A-Za-z0-9]/-/g')"; mkdir -p "$d"; ` +
		`echo '{"type":"assistant","sessionId":"s","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":9000,"output_tokens":1}}}' > "$d/s.jsonl"; sleep 30`
	b := f.benchmark("HOME_T="+env.Home+" sh -c '"+strings.ReplaceAll(worker, "'", `'\''`)+"'", f.task)
	b.Budget.Tokens = 5000
	run, err := Execute(context.Background(), b, env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if r.Outcome != OutcomeBudget || !r.Usage.Known || r.Usage.Tokens() != 9001 || r.Usage.Source != "transcript" {
		t.Fatalf("record = %+v", r)
	}
}

func TestRunWaitsForMemoryThenPausesAndResumes(t *testing.T) {
	f := newFixture(t)
	env := f.env(t)
	calls := 0
	env.MemGuard = func() (bool, string) { calls++; return calls < 3, "BLOCKED: used 80%" }
	env.MemWait = time.Hour
	run, err := Execute(context.Background(), f.benchmark(fixWorker, f.task), env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Stop != StopDone || calls != 3 || run.Tasks[0].Outcome != OutcomePass {
		t.Fatalf("stop %s after %d checks: %+v", run.Stop, calls, run.Tasks)
	}

	env.MemGuard = func() (bool, string) { return true, "BLOCKED: used 90%" }
	env.MemWait = 0
	paused, err := Execute(context.Background(), f.benchmark(fixWorker, f.task), env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if paused.Stop != StopPaused || len(paused.Tasks) != 0 || paused.Finished() {
		t.Fatalf("paused run = %+v", paused)
	}
	env.MemGuard = nil
	resumed, err := Execute(context.Background(), f.benchmark(fixWorker, f.task), env, Options{Resume: paused.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != paused.ID || resumed.Stop != StopDone || len(resumed.Tasks) != 1 {
		t.Fatalf("resumed run = %+v", resumed)
	}
}

func TestHoldoutTasksAreOnlyAnAggregateInRunJSON(t *testing.T) {
	f := newFixture(t)
	hold := f.task
	hold.Slug = "calc/secret-task"
	hold.Holdout = true
	write(t, SnapshotPath(hold), "---\ntitle: \"Secret task\"\nstatus: \"in progress\"\nsystem: \"calc\"\n---\n")
	var log bytes.Buffer
	env := f.env(t)
	env.Log = &log
	run, err := Execute(context.Background(), f.benchmark(fixWorker, f.task, hold), env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(RunDir("t", run.ID), "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("secret-task")) {
		t.Errorf("run.json names the holdout task:\n%s", data)
	}
	if strings.Contains(log.String(), "secret-task") {
		t.Errorf("the run's log names the holdout task:\n%s", log.String())
	}
	if run.Holdout != (Holdout{Passed: 1, Total: 1}) || len(run.Tasks) != 1 {
		t.Fatalf("holdout %+v, tasks %d", run.Holdout, len(run.Tasks))
	}
	all, err := LoadRecords("t", run.ID)
	if err != nil || len(all) != 2 || all[1].Task != "calc/calc/secret-task" {
		t.Fatalf("holdout.json records = %+v, %v", all, err)
	}
	for _, m := range Metrics(run) {
		for _, v := range m.Labels {
			if strings.Contains(v, "secret") {
				t.Errorf("metric %s labels name the holdout: %v", m.Name, m.Labels)
			}
		}
	}
}

func TestRunRecordsGatesRefusalsAndReworkFromTheSandboxClilog(t *testing.T) {
	f := newFixture(t)
	// The worker writes the clilog and stats a real issue-cli would.
	worker := `mkdir -p .agent-logs; cat > .agent-logs/clilog.jsonl <<'EOF'
{"ts":"2026-10-30T10:00:00Z","args":["transition","calc/fix-add","--to","testing"],"exit":1}
{"ts":"2026-10-30T10:01:00Z","args":["transition","calc/fix-add","--to","testing"],"exit":0}
{"ts":"2026-10-30T10:02:00Z","args":["transition","calc/fix-add","--to","in progress"],"exit":0}
{"ts":"2026-10-30T10:03:00Z","args":["transition","calc/fix-add","--to","testing","--dry-run"],"exit":0}
{"ts":"2026-10-30T10:04:00Z","args":["transition","calc/fix-add","--to","testing"],"exit":0}
EOF
s=$(dirname "$ISSUE_VIEWER_CONFIG")/tracker/issues/calc/fix-add.stats.json
echo '{"transitions":[{"from":"in progress","to":"testing"},{"from":"testing","to":"in progress"},{"from":"in progress","to":"testing"}]}' > "$s"`
	run, err := Execute(context.Background(), f.benchmark(worker, f.task), f.env(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if r.Calls != 5 || r.Refused != 1 || r.Gates != 3 || r.FirstTryGates != 2 || r.Backward != 1 {
		t.Fatalf("calls %d refused %d gates %d first %d backward %d", r.Calls, r.Refused, r.Gates, r.FirstTryGates, r.Backward)
	}
	if strings.Join(r.Statuses, ",") != "testing,in progress,testing" || r.FinalStatus != "in progress" {
		t.Fatalf("statuses %v final %q", r.Statuses, r.FinalStatus)
	}
}

func TestRunJudgeScoresTheTask(t *testing.T) {
	f := newFixture(t)
	b := f.benchmark(fixWorker, f.task)
	b.Judge = `grep -q 'WORKER DIFF' && printf '%s\n' '{"result":"SCORE: 4\nREASON: same fix","usage":{"input_tokens":10,"output_tokens":5},"total_cost_usd":0.01}'`
	run, err := Execute(context.Background(), b, f.env(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if r.Judge != 4 || r.JudgeReason != "same fix" || r.Usage.Tokens() != 4215 {
		t.Fatalf("judge %d %q tokens %d", r.Judge, r.JudgeReason, r.Usage.Tokens())
	}
	if run.Summary.Judge != 4 {
		t.Errorf("summary judge = %v", run.Summary.Judge)
	}
}

func TestCompareListsRegressionsFirstAndSaysWhenBRegresses(t *testing.T) {
	rec := func(task, outcome string, tokens int64, model string) TaskRecord {
		return TaskRecord{Task: task, Outcome: outcome, Judge: -1, Usage: Usage{Known: true, Input: tokens, Model: model}}
	}
	a := &Run{ID: "a", Benchmark: "t", Stop: StopDone}
	Summarize(a, []TaskRecord{rec("p/one", OutcomePass, 100, "m1"), rec("p/two", OutcomeFail, 100, "m1"), rec("p/zed", OutcomePass, 100, "m1")})
	a.Tasks = []TaskRecord{rec("p/one", OutcomePass, 100, "m1"), rec("p/two", OutcomeFail, 100, "m1"), rec("p/zed", OutcomePass, 100, "m1")}
	b := &Run{ID: "b", Benchmark: "t", Stop: StopDone}
	b.Tasks = []TaskRecord{rec("p/one", OutcomePass, 150, "m1"), rec("p/two", OutcomePass, 100, "m1"), rec("p/zed", OutcomeFail, 100, "m2")}
	Summarize(b, b.Tasks)
	c := Compare(a, b)
	if !c.Comparable || !c.Regressed() {
		t.Fatalf("comparison = %+v", c)
	}
	if strings.Join(c.Regressions, ",") != "p/zed" || strings.Join(c.Improved, ",") != "p/two" {
		t.Fatalf("regressions %v improved %v", c.Regressions, c.Improved)
	}
	if c.Tasks[0].Task != "p/zed" {
		t.Errorf("regressions must come first: %+v", c.Tasks)
	}
	if c.Tasks[1].TokensDelta != 50 {
		t.Errorf("p/one tokens delta = %d", c.Tasks[1].TokensDelta)
	}
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "model m1") {
		t.Errorf("warnings = %v", c.Warnings)
	}
	same := Compare(a, a)
	if !same.Comparable || same.Regressed() {
		t.Errorf("a run against itself: %+v", same)
	}
	paused := &Run{ID: "c", Benchmark: "t", Stop: StopPaused, Tasks: a.Tasks}
	if Compare(a, paused).Comparable {
		t.Error("an unfinished run must not be comparable")
	}
	// The JSON is the experiment loop's interface.
	data, _ := json.Marshal(c)
	for _, key := range []string{`"regressions"`, `"improved"`, `"pass_rate_delta"`, `"tokens_delta"`, `"usd_delta"`, `"wall_delta_sec"`, `"warnings"`, `"holdout_a"`} {
		if !bytes.Contains(data, []byte(key)) {
			t.Errorf("compare JSON lacks %s", key)
		}
	}
}

func TestParseResultAndTranscripts(t *testing.T) {
	u, ok := ParseResult([]byte("noise\n" + `{"type":"result","session_id":"x","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}`))
	if !ok || u.Tokens() != 10 || u.USDKnown {
		t.Fatalf("ParseResult = %+v %v", u, ok)
	}
	if _, ok := ParseResult([]byte("not json")); ok {
		t.Error("ParseResult accepted text")
	}
	Cost(&u, map[string]Price{"": {In: 1e6, Out: 1e6, CacheRead: 1e6, CacheWrite: 1e6}})
	if !u.USDKnown || u.USD != 10 {
		t.Errorf("Cost = %+v", u)
	}

	dir := t.TempDir()
	lines := []string{
		`{"type":"assistant","version":"2.1.0","sessionId":"s","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":5,"output_tokens":1},"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/etc/evil"}}]}}`,
		`{"type":"assistant","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":5,"output_tokens":1},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/sandbox/repo/a.go"}}]}}`,
		`{"type":"user","message":{"content":"hi"}}`,
		`{"type":"assistant","message":{"id":"m2","model":"claude-opus-5-5","usage":{"input_tokens":2,"output_tokens":2}}}`,
	}
	write(t, filepath.Join(dir, "s.jsonl"), strings.Join(lines, "\n")+"\n")
	tr := ReadTranscripts(dir, "/sandbox")
	if !tr.Usage.Known || tr.Usage.Tokens() != 10 || tr.Usage.ToolVersion != "2.1.0" || tr.Usage.Model != "claude-opus-5-5" {
		t.Fatalf("transcript usage = %+v", tr.Usage)
	}
	if strings.Join(tr.Outside, ",") != "/etc/evil" {
		t.Errorf("outside = %v", tr.Outside)
	}
	if got := ReadTranscripts(filepath.Join(dir, "none"), "/"); got.Usage.Known {
		t.Error("a missing transcript must be unknown")
	}
	if d := TranscriptDir("/h", "/Users/a/Repo/x.y"); d != "/h/.claude/projects/-Users-a-Repo-x-y" {
		t.Errorf("TranscriptDir = %s", d)
	}
}

func TestFindRangeAndHiddenTestsFromAnIssuesStats(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "r")
	mustGit(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	commit := func(at time.Time, file, data string) string {
		write(t, filepath.Join(repo, file), data)
		mustGit(t, repo, "add", "-A")
		t.Setenv("GIT_COMMITTER_DATE", at.Format(time.RFC3339))
		mustGit(t, repo, "commit", "-q", "--date", at.Format(time.RFC3339), "-m", file)
		return mustGit(t, repo, "rev-parse", "HEAD")
	}
	base := commit(t0.Add(-2*time.Hour), "a.go", "package a\n\nfunc TestOld(t *testing.T) {}\n")
	commit(t0.Add(-30*time.Minute), "a.go", "package a\n\nfunc F() {}\n")
	head := commit(t0.Add(-10*time.Minute), "a_test.go", "package a\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) {}\n")
	stats := tracker.StatsStore{
		Dispatches:  []tracker.Dispatch{{At: t0.Add(-time.Hour)}},
		Transitions: []tracker.TransitionStat{{To: "in progress", TS: t0.Add(-time.Hour), BranchHead: base}, {To: "done", TS: t0, BranchHead: head, MergeSHA: head}},
	}
	r, err := FindRange(repo, stats)
	if err != nil {
		t.Fatal(err)
	}
	if r.Base != base || r.Merged != head || r.Commits != 2 || strings.Join(r.Files, ",") != "a.go,a_test.go" {
		t.Fatalf("range = %+v (base %s)", r, base)
	}
	hidden := HiddenFiles(repo, r)
	if strings.Join(hidden, ",") != "a_test.go" {
		t.Fatalf("hidden = %v", hidden)
	}
	if tests := HiddenTests(repo, r, hidden); strings.Join(tests, ",") != "TestNew" {
		t.Fatalf("hidden tests = %v", tests)
	}
	if f := ForeignFiles(repo, r); len(f) != 0 {
		t.Errorf("a branch with only its own commits: foreign = %v", f)
	}
	// Main moves under the branch: b.go is not the branch's.
	mustGit(t, repo, "checkout", "-q", "-b", "side", base)
	side := commit(t0.Add(-20*time.Minute), "b.go", "package a\n")
	mustGit(t, repo, "checkout", "-q", "main")
	mustGit(t, repo, "merge", "-q", "--no-ff", "-m", "merge", side)
	merge := mustGit(t, repo, "rev-parse", "HEAD")
	if f := ForeignFiles(repo, Range{Base: r.Base, Merged: merge, Files: r.Files}); strings.Join(f, ",") != "b.go" {
		t.Errorf("main merged in: foreign = %v, want b.go", f)
	}
	if _, err := FindRange(repo, tracker.StatsStore{}); err != ErrNoRange {
		t.Errorf("no stats: err = %v", err)
	}
}

func TestSetStatusRewindsTheFrontmatter(t *testing.T) {
	in := "---\ntitle: \"X\"\nstatus: \"done\"\nassignee: \"agent-x\"\nhuman_approval: \"done\"\ndone_at: \"2026-10-30\"\n---\n\nbody\n"
	got := string(SetStatus([]byte(in), "in progress"))
	want := "---\ntitle: \"X\"\nstatus: \"in progress\"\n---\n\nbody\n"
	if got != want {
		t.Fatalf("SetStatus =\n%q\nwant\n%q", got, want)
	}
}

func TestBenchmarkSaveLoadAndTaskLookup(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	b := New("factory-v1")
	b.Budget = Budget{Tokens: 100, TaskWall: "10m"}
	b.Tasks = []Task{{Project: "a", Slug: "x/one"}, {Project: "b", Slug: "x/one"}, {Project: "b", Slug: "y/two", Holdout: true}}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load("factory-v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tasks) != 3 || got.Budget.TaskWallDur() != 10*time.Minute || !got.Tasks[2].Holdout {
		t.Fatalf("loaded = %+v", got)
	}
	if _, tk := got.Task("x/one"); tk != nil {
		t.Error("an ambiguous slug must not resolve")
	}
	if _, tk := got.Task("b/x/one"); tk == nil || tk.Project != "b" {
		t.Error("project/slug must resolve")
	}
	if _, tk := got.Task("y/two"); tk == nil {
		t.Error("a unique slug must resolve")
	}
	if names := List(); strings.Join(names, ",") != "factory-v1" {
		t.Errorf("List = %v", names)
	}
	if _, err := Load("Bad Name"); err == nil {
		t.Error("a bad name must be refused")
	}
}

func TestBenchFamilyReportsTheNewestFinishedRunOfBenchmarksWithTheProject(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	b := New("factory-v1")
	b.Tasks = []Task{{Project: "orchard", Slug: "a"}}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	done := &Run{ID: "r1", Benchmark: "factory-v1", Started: at, Ended: at.Add(time.Hour), Stop: StopDone, Setup: Setup{Label: "baseline"}}
	if err := Save(done, []TaskRecord{{Task: "orchard/a", Outcome: OutcomePass, Judge: 5, Usage: Usage{Known: true, Input: 7}}}); err != nil {
		t.Fatal(err)
	}
	paused := &Run{ID: "r2", Benchmark: "factory-v1", Started: at.Add(2 * time.Hour), Ended: at.Add(3 * time.Hour), Stop: StopPaused}
	if err := Save(paused, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := benchFamily(context.Background(), health.Env{Project: &tracker.Project{Slug: "orchard"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, r := range rows {
		if r.Labels["benchmark"] != "factory-v1" || r.Labels["setup"] != "baseline" {
			t.Errorf("%s labels = %v", r.Name, r.Labels)
		}
		got[r.Name] = r.Value
	}
	if got["bench.pass_rate"] != 1 || got["bench.judge"] != 5 || got["bench.tasks"] != 1 {
		t.Fatalf("rows = %v", got)
	}
	if rows, _ := benchFamily(context.Background(), health.Env{Project: &tracker.Project{Slug: "other-game"}}); len(rows) != 0 {
		t.Errorf("a project with no task got rows: %v", rows)
	}
	if _, err := benchFamily(context.Background(), health.Env{Project: &tracker.Project{Slug: "orchard"}, Backfill: true}); err != health.ErrNoPast {
		t.Errorf("backfill: err = %v", err)
	}
}

func TestRunUsesTheSetupsIssueCLIAndWorkflow(t *testing.T) {
	f := newFixture(t)
	env := f.env(t)
	env.Keep = true
	wf := filepath.Join(t.TempDir(), "experiment.yaml")
	write(t, wf, fixtureWorkflow+"# experiment B\n")
	worker := `command -v issue-cli > cli.seen; cat "$(dirname "$ISSUE_VIEWER_CONFIG")/tracker/workflow.yaml" > wf.seen; ` + fixWorker
	run, err := Execute(context.Background(), f.benchmark(worker, f.task), env, Options{Workflow: wf, Setup: Setup{Ref: "abc", Label: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	sb := filepath.Join(env.TmpRoot, run.ID, "01", "repo")
	if data, _ := os.ReadFile(filepath.Join(sb, "cli.seen")); strings.TrimSpace(string(data)) != env.IssueCLI {
		t.Errorf("the worker's issue-cli = %q, want %s", data, env.IssueCLI)
	}
	if data, _ := os.ReadFile(filepath.Join(sb, "wf.seen")); !strings.Contains(string(data), "experiment B") {
		t.Errorf("the sandbox workflow = %q", data)
	}
	if saved, _ := LoadRun("t", run.ID); saved.Setup.Ref != "abc" || saved.Setup.Label != "B" {
		t.Errorf("setup = %+v", saved.Setup)
	}
}

func TestCandidateRisksReadOnlyTheHiddenTestsBodies(t *testing.T) {
	src := "package a\n\nfunc TestNew(t *testing.T) {\n\tnewTmux(t)\n\ttime.Sleep(time.Millisecond)\n}\n\nfunc TestOther(t *testing.T) {\n\tchromedp.Run(nil)\n}\n"
	got := CandidateRisks(map[string]string{"a_test.go": src}, []string{"TestNew"}, "## AC\n- [ ] (owner) The owner picks.\n")
	if strings.Join(got, ",") != "tmux,timing,owner steps" {
		t.Errorf("risks = %v, want tmux,timing,owner steps (TestOther's browser is not a hidden test)", got)
	}
	many := make([]string, BroadTests+1)
	for i := range many {
		many[i] = fmt.Sprintf("Test%d", i)
	}
	got = CandidateRisks(map[string]string{"a_test.go": "package a\n", "playtest/sessions/x.yaml": "steps: []\n"}, many, "")
	if strings.Join(got, ",") != fmt.Sprintf("browser,broad (%d tests)", BroadTests+1) {
		t.Errorf("risks = %v", got)
	}
	if got := CandidateRisks(nil, nil, "- [ ] The owner tries it (owner)\n"); strings.Join(got, ",") != "owner steps" {
		t.Errorf("a trailing (owner) box: %v", got)
	}
	if got := CandidateRisks(map[string]string{"a_test.go": src}, []string{"TestQuiet"}, "- [x] done\n"); len(got) != 0 {
		t.Errorf("a task with no risks: %v", got)
	}
}

// Tokens count cache reads, about 15M per dark-factory task: one task fits
// the default cap, and a run with no tokens set is capped per task.
func TestDefaultTokenCapFitsOneTaskWithCacheReads(t *testing.T) {
	task := Usage{Input: 40_000, Output: 300_000, CacheRead: 15_000_000, CacheWrite: 900_000}
	if task.Tokens() >= DefaultTaskTokens {
		t.Fatalf("one task's %d tokens do not fit DefaultTaskTokens %d", task.Tokens(), DefaultTaskTokens)
	}
	if got := (Budget{}).TokenCap(3); got != 3*DefaultTaskTokens {
		t.Errorf("unset cap for 3 tasks = %d", got)
	}
	if got := (Budget{Tokens: 1000}).TokenCap(3); got != 1000 {
		t.Errorf("set cap = %d", got)
	}
	run := &Run{SpentTokens: 2 * task.Tokens()}
	if over := overBudget(Budget{}, 3, run, time.Now(), task.Tokens(), 0); over != "" {
		t.Errorf("three tasks under the default cap stopped: %s", over)
	}
	if over := overBudget(Budget{}, 1, run, time.Now(), 0, 0); !strings.Contains(over, "token budget") {
		t.Errorf("two tasks' tokens on a one-task cap: %q", over)
	}
}

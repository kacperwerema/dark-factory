package experiment

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/retro"
)

// run builds a finished run: outcomes per task, holdout n/m, first-try rate.
func run(id string, outcomes map[string]string, hold [2]int, firstTry float64) *Run {
	r := &Run{ID: id, Benchmark: "factory-v1", Stop: "done", Ended: time.Now()}
	for task, o := range outcomes {
		t := struct {
			Task    string `json:"task"`
			Outcome string `json:"outcome"`
			Usage   struct {
				Model       string `json:"model"`
				ToolVersion string `json:"claude_code_version"`
			} `json:"usage"`
		}{Task: task, Outcome: o}
		t.Usage.Model = "claude-opus-5-5"
		r.Tasks = append(r.Tasks, t)
		if o != "broken" {
			r.Summary.Scored++
		}
		if o == "pass" {
			r.Summary.Passed++
		}
	}
	r.Holdout.Passed, r.Holdout.Total = hold[0], hold[1]
	r.Summary.FirstTryRate = firstTry
	r.Summary.Judge = -1
	r.Summary.TokensKnown = true
	return r
}

func TestJudge(t *testing.T) {
	target := Target{Metric: "gates_first_try", Direction: "up", Threshold: 0.05}
	base := map[string]string{"a": "pass", "b": "pass", "c": "fail", "d": "pass"}
	cases := []struct {
		name    string
		a, b    *Run
		t       Target
		verdict string
		reason  string
	}{
		{"improves", run("A", base, [2]int{1, 2}, 0.60), run("B", base, [2]int{1, 2}, 0.70), target, Promising, "no regressions"},
		{"target missed", run("A", base, [2]int{1, 2}, 0.60), run("B", base, [2]int{1, 2}, 0.62), target, Rejected, "target missed"},
		{"no move at threshold 0", run("A", base, [2]int{0, 0}, 0.60), run("B", base, [2]int{0, 0}, 0.60), Target{Metric: "gates_first_try", Direction: "up"}, Rejected, "target missed"},
		{"regression", run("A", base, [2]int{1, 2}, 0.60),
			run("B", map[string]string{"a": "fail", "b": "pass", "c": "pass", "d": "pass"}, [2]int{1, 2}, 0.9), target, Rejected, "regressed: a"},
		{"pass rate drop via holdout", run("A", base, [2]int{2, 2}, 0.60), run("B", base, [2]int{1, 2}, 0.9), target, Rejected, "pass rate dropped"},
		{"broken task is not a regression", run("A", base, [2]int{0, 0}, 0.60),
			run("B", map[string]string{"a": "broken", "b": "pass", "c": "fail", "d": "pass"}, [2]int{0, 0}, 0.9), target, Promising, ""},
		{"other task sets", run("A", base, [2]int{0, 0}, 0.6), run("B", map[string]string{"a": "pass"}, [2]int{0, 0}, 0.9), target, Broken, "only in A"},
		{"holdout size differs", run("A", base, [2]int{1, 2}, 0.6), run("B", base, [2]int{1, 3}, 0.9), target, Broken, "holdout differs"},
		{"no gates in a run", run("A", base, [2]int{0, 0}, -1), run("B", base, [2]int{0, 0}, 0.9), target, Broken, "no gates_first_try"},
		{"tokens down by a fraction", withTokens(run("A", base, [2]int{0, 0}, 0.6), 1000), withTokens(run("B", base, [2]int{0, 0}, 0.6), 800),
			Target{Metric: "tokens", Direction: "down", Threshold: 0.1}, Promising, ""},
		{"tokens down too little", withTokens(run("A", base, [2]int{0, 0}, 0.6), 1000), withTokens(run("B", base, [2]int{0, 0}, 0.6), 950),
			Target{Metric: "tokens", Direction: "down", Threshold: 0.1}, Rejected, "target missed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, why, res := Judge(c.a, c.b, c.t)
			if v != c.verdict {
				t.Fatalf("verdict %s, want %s (%v)", v, c.verdict, why)
			}
			if c.reason != "" && !strings.Contains(strings.Join(why, "; "), c.reason) {
				t.Errorf("reasons %v, want %q", why, c.reason)
			}
			if res == nil {
				t.Fatal("no result")
			}
		})
	}
}

func withTokens(r *Run, n int64) *Run { r.SpentTokens = n; return r }

func TestJudgeStoppedRunAndModels(t *testing.T) {
	base := map[string]string{"a": "pass"}
	a, b := run("A", base, [2]int{0, 0}, 0.5), run("B", base, [2]int{0, 0}, 0.9)
	b.Stop = "budget"
	if v, why, _ := Judge(a, b, Target{Metric: "gates_first_try", Direction: "up"}); v != Broken || !strings.Contains(why[0], "did not finish") {
		t.Fatalf("stopped run: %s %v", v, why)
	}
	b = run("B", base, [2]int{0, 0}, 0.9)
	b.Tasks[0].Usage.Model = "claude-sonnet-5-5"
	if v, why, _ := Judge(a, b, Target{Metric: "gates_first_try", Direction: "up"}); v != Broken || !strings.Contains(strings.Join(why, ";"), "other models") {
		t.Fatalf("model mismatch: %s %v", v, why)
	}
}

func TestPassRateUnderOneTaskIsNoChange(t *testing.T) {
	// 4 scored + holdout 0: one task is 0.25. B scored one task fewer
	// (broken), so its rate moves 0.75 → 0.667: under one task, no drop.
	a := run("A", map[string]string{"a": "pass", "b": "pass", "c": "pass", "d": "fail"}, [2]int{0, 0}, 0.5)
	b := run("B", map[string]string{"a": "pass", "b": "pass", "c": "broken", "d": "fail"}, [2]int{0, 0}, 0.9)
	v, why, _ := Judge(a, b, Target{Metric: "gates_first_try", Direction: "up", Threshold: 0.1})
	if v != Promising {
		t.Fatalf("%s %v", v, why)
	}
	v, why, _ = Judge(a, b, Target{Metric: "pass_rate", Direction: "up"})
	if v != Rejected || !strings.Contains(strings.Join(why, ";"), "target missed") {
		t.Fatalf("pass_rate target: %s %v", v, why)
	}
}

func TestParseTarget(t *testing.T) {
	if tg, err := ParseTarget("gates_first_try:up:0.05"); err != nil || tg.Threshold != 0.05 || tg.Direction != "up" {
		t.Fatalf("%+v %v", tg, err)
	}
	for _, bad := range []string{"speed:up:1", "pass_rate:sideways:1", "pass_rate:up", "pass_rate:up:-1"} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
	if _, err := ParseTarget("speed:up:1"); !strings.Contains(err.Error(), "gates_first_try") {
		t.Errorf("unknown metric error should list the metrics: %v", err)
	}
}

func TestGuard(t *testing.T) {
	changed := []string{"workflow.yaml", "internal/bench/score.go", "internal/tracker/dispatch.go"}
	got := Guard(changed, DefaultProtected, nil)
	if len(got) != 1 || got[0].Path != "internal/bench/score.go" || !strings.Contains(got[0].Why, "own yardstick") {
		t.Fatalf("%v", got)
	}
	got = Guard(changed, DefaultProtected, []string{"workflow.yaml"})
	if len(got) != 2 || !strings.Contains(got[1].Why, "outside the experiment's change") {
		t.Fatalf("%v", got)
	}
	if got := Guard([]string{"internal/experiment/verdict.go"}, DefaultProtected, nil); len(got) != 1 {
		t.Fatalf("the verdict rule is protected: %v", got)
	}
}

func TestWorkRecordsAreNotTheChange(t *testing.T) {
	changed := []string{"issues/Shop/x.md", "issues/Shop/x.data.json", "retros/r1.md", "issuesx/y", "workflow.yaml"}
	got := WorkRecords(changed, []string{"issues", "retros/"})
	if strings.Join(got, ",") != "issuesx/y,workflow.yaml" {
		t.Fatalf("%v", got)
	}
}

func TestProtectedFromOwnerConfig(t *testing.T) {
	dir := t.TempDir()
	if p, _ := Protected(dir, "factory-v1"); len(p) != len(DefaultProtected) {
		t.Fatalf("no config: %v", p)
	}
	os.WriteFile(filepath.Join(dir, "factory-v1.yaml"), []byte("name: factory-v1\nprotected:\n  - prompts/judge.md\n"), 0o644)
	p, err := Protected(dir, "factory-v1")
	if err != nil || len(p) != 1 || p[0] != "prompts/judge.md" {
		t.Fatalf("%v %v", p, err)
	}
}

func TestBlockRoundTripKeepsSidecar(t *testing.T) {
	dir := t.TempDir()
	issue := filepath.Join(dir, "x.md")
	os.WriteFile(issue, []byte("---\ntitle: x\n---\n"), 0o644)
	side := strings.TrimSuffix(issue, ".md") + ".data.json"
	os.WriteFile(side, []byte(`{"next_id":2,"entries":[{"id":1,"description":"keep me","status":"open","comment":""}]}`), 0o644)
	if b, err := Load(issue); err != nil || b != nil {
		t.Fatalf("no block yet: %v %v", b, err)
	}
	b := &Block{Hypothesis: "h", Target: Target{Metric: "rework", Direction: "down", Threshold: 1}, Verdict: Rejected,
		Result: &Result{MetricA: 3, MetricB: 3, PassA: 0.5, PassB: 0.5}}
	if err := Save(issue, b); err != nil {
		t.Fatal(err)
	}
	got, err := Load(issue)
	if err != nil || got.Hypothesis != "h" || got.Target.Metric != "rework" {
		t.Fatalf("%+v %v", got, err)
	}
	data, _ := os.ReadFile(side)
	var raw map[string]json.RawMessage
	json.Unmarshal(data, &raw)
	if !strings.Contains(string(raw["entries"]), "keep me") {
		t.Fatalf("entries lost: %s", data)
	}
	if line := got.VerdictLine(); !strings.HasPrefix(line, "rejected — rework 3→3 (down by 1), pass 0.50→0.50") {
		t.Fatalf("verdict line: %s", line)
	}
}

func TestBaselineReuse(t *testing.T) {
	dir := t.TempDir()
	write := func(id, sha, stop string, started time.Time) {
		r := map[string]any{"id": id, "benchmark": "b1", "stop": stop, "started": started, "setup": map[string]any{"ref": "main", "sha": sha, "label": "l-" + id}}
		data, _ := json.Marshal(r)
		os.MkdirAll(filepath.Join(RunsDir(dir, "b1"), id), 0o755)
		os.WriteFile(filepath.Join(RunsDir(dir, "b1"), id, "run.json"), data, 0o644)
	}
	now := time.Now()
	write("r1", "aaa", "done", now.Add(-3*time.Hour))
	write("r2", "aaa", "paused", now.Add(-2*time.Hour))
	write("r3", "bbb", "done", now.Add(-time.Hour))
	if r := Baseline(dir, "b1", "aaa"); r == nil || r.ID != "r1" {
		t.Fatalf("baseline for aaa: %+v", r)
	}
	if r := Baseline(dir, "b1", "ccc"); r != nil {
		t.Fatalf("no baseline for ccc: %+v", r)
	}
	if r := Labelled(dir, "b1", "l-r3"); r == nil || r.ID != "r3" {
		t.Fatalf("labelled: %+v", r)
	}
}

func TestCandidates(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	mk := func(file, slug, body string, age time.Duration) *retro.Entry {
		return &retro.Entry{FileName: file, IssueSlug: slug, Body: body, ModTime: now.Add(-age)}
	}
	entries := []*retro.Entry{
		mk("1.md", "orchard/a", "Base workflow: agents forget check-script before testing.\nTooling/workflow friction: check-script is slow.", time.Hour),
		mk("2.md", "orchard/b", "Subsystem workflow for UI: name check-script in the guidance.", 2*time.Hour),
		mk("3.md", "orchard/c", "Base workflow: (1) check-script is never run (2) the playtest-check box is unclear", 3*time.Hour),
		mk("4.md", "orchard/d", "Base workflow: none", time.Hour),
		mk("5.md", "orchard/e", "Base workflow: old note about worktree-cleanup.", 60*24*time.Hour),
		mk("6.md", "orchard/f", "Missing system-specific instructions: check-script docs.", time.Hour),
	}
	open := []Open{{Slug: "factory/exp-1", Source: []string{"retros/2.md"}}}
	patterns, singles := Candidates(entries, open, map[string]bool{"issue-cli": true}, 3, now)
	if len(patterns) != 1 || patterns[0].Term != "check-script" || len(patterns[0].Issues) != 3 {
		t.Fatalf("patterns: %+v", patterns)
	}
	if patterns[0].Experiment != "factory/exp-1" {
		t.Errorf("open experiment not named: %+v", patterns[0])
	}
	// Only the playtest-check item is left: recent, not in a pattern. The
	// 60-day-old finding and the none section are out.
	if len(singles) != 1 || !strings.Contains(singles[0].Sample, "playtest-check") {
		t.Fatalf("singles: %+v", singles)
	}
}

// A theme told in the workflow's own words (the tests: comment, Proves:
// trailers) groups across issues like a code-like term does.
func TestCandidatesGroupWorkflowPrefixes(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	mk := func(file, slug, body string) *retro.Entry {
		return &retro.Entry{FileName: file, IssueSlug: slug, Body: body, ModTime: now.Add(-time.Hour)}
	}
	entries := []*retro.Entry{
		mk("1.md", "cli/a", "Base workflow: the proof wanted a tests: comment before the trailer counted."),
		mk("2.md", "cli/b", "Base workflow: smooth. Friction: a tests: report goes stale on every commit."),
		mk("3.md", "ui/c", "Subsystem workflow for UI: the Proves: trailer read UNPROVEN until the tests: comment."),
	}
	patterns, _ := Candidates(entries, nil, nil, 3, now)
	var terms []string
	for _, p := range patterns {
		terms = append(terms, p.Term)
	}
	if len(patterns) != 1 || patterns[0].Term != "tests:" || len(patterns[0].Issues) != 3 {
		t.Fatalf("patterns: %q", terms)
	}
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRollbackPreparesRevertBranch(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	repo := t.TempDir()
	gitT(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "workflow.yaml"), []byte("v: 1\n"), 0o644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "feat: base")
	os.WriteFile(filepath.Join(repo, "workflow.yaml"), []byte("v: 2\n"), 0o644)
	gitT(t, repo, "commit", "-q", "-am", "feat(workflow): the change\n\nExperiment: factory/exp-1")
	os.WriteFile(filepath.Join(repo, "other.txt"), []byte("x\n"), 0o644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "feat: unrelated\n\nExperiment: factory/exp-10")
	mainBefore := gitT(t, repo, "rev-parse", "main")

	shas, err := AdoptedCommits(repo, "main", "factory/exp-1")
	if err != nil || len(shas) != 1 {
		t.Fatalf("adopted: %v %v", shas, err)
	}
	branch, err := PrepareRevert(repo, "main", "factory/exp-1", shas)
	if err != nil || branch != "revert/factory/exp-1" {
		t.Fatalf("%s %v", branch, err)
	}
	if got := gitT(t, repo, "rev-parse", "main"); got != mainBefore {
		t.Fatal("main moved")
	}
	if got := gitT(t, repo, "show", branch+":workflow.yaml"); got != "v: 1" {
		t.Fatalf("revert branch workflow.yaml = %q", got)
	}
	if got := gitT(t, repo, "show", branch+":other.txt"); got != "x" {
		t.Fatalf("unrelated change reverted: %q", got)
	}
	if msg := gitT(t, repo, "log", "-1", "--format=%s", branch); !strings.HasPrefix(msg, "revert(experiment): roll back factory/exp-1") {
		t.Fatalf("message %q", msg)
	}
	if _, err := PrepareRevert(repo, "main", "factory/exp-1", shas); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second rollback: %v", err)
	}
	if _, err := PrepareRevert(repo, "main", "factory/none", nil); err == nil {
		t.Fatal("no commits: want an error")
	}
	if !Contains(repo, "main", shas[0]) || Contains(repo, shas[0]+"~1", mainBefore) {
		t.Fatal("Contains")
	}
	if files, _ := Changed(repo, shas[0]+"~1", "main"); len(files) != 2 {
		t.Fatalf("changed %v", files)
	}
}

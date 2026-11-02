package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/experiment"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// makeExperimentProject is a project with one issue labelled experiment
// (factory/exp) and one plain issue (factory/plain), plus retros.
func makeExperimentProject(t *testing.T) (*tracker.Project, string) {
	t.Helper()
	root := realTempDir(t)
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("issues/Factory/exp.md", "---\ntitle: \"Exp\"\nstatus: \"in progress\"\nsystem: \"Factory\"\nlabels:\n  - experiment\n---\n\nBody.\n")
	write("issues/Factory/plain.md", "---\ntitle: \"Plain\"\nstatus: \"in progress\"\nsystem: \"Factory\"\n---\n\nBody.\n")
	retro := func(n int, slug, line string) {
		write(filepath.Join("retros", "r"+string(rune('0'+n))+".md"),
			"# Retrospective\n\nIssue: "+slug+"\nDate: "+time.Now().Format(time.RFC3339)+"\n\n"+line+"\nTooling/workflow friction: none\n")
	}
	retro(1, "orchard/a", "Base workflow: agents skip check-script before testing.")
	retro(2, "orchard/b", "Subsystem workflow for UI: name check-script in the guidance.")
	retro(3, "orchard/c", "Base workflow: check-script is never run.")
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(root, "bench"))
	return &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root}, root
}

func TestExperimentInit(t *testing.T) {
	proj, _ := makeExperimentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "speed:up:1"})
	if err == nil || !strings.Contains(err.Error(), "unknown metric") {
		t.Fatalf("unknown metric: %v", err)
	}
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "naming check-script helps",
		"--target", "gates_first_try:up:0.05", "--source", "r1.md", "--change", "workflow.yaml"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Experiment block written on factory/exp: gates_first_try up by 0.05")
	issue := loadLeadIssue(t, proj, "factory/exp")
	b, err := experiment.Load(issue.FilePath)
	if err != nil || b == nil || b.Hypothesis != "naming check-script helps" || b.Source[0] != "retros/r1.md" || b.Change[0] != "workflow.yaml" {
		t.Fatalf("block %+v %v", b, err)
	}
	if line := experimentHeaderLine(proj, issue); !strings.Contains(line, "the owner adopts experiments, even in YOLO") || !strings.Contains(line, "experiment run factory/exp --dry-run") {
		t.Fatalf("header line %q", line)
	}
}

// A proposal filed at idea, without the label, becomes an experiment with the
// one documented command: experiment init labels it and writes the block.
func TestExperimentInitLabelsAProposalAtIdea(t *testing.T) {
	proj, root := makeExperimentProject(t)
	path := filepath.Join(root, "issues/Factory/proposal.md")
	if err := os.WriteFile(path, []byte("---\ntitle: \"Proposal\"\nstatus: \"idea\"\nsystem: \"Factory\"\nlabels:\n  - workflow\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"show", "factory/proposal"}); err == nil || !strings.Contains(err.Error(), "experiment init factory/proposal") {
		t.Fatalf("show on a proposal should name experiment init: %v", err)
	}
	if err := runExperiment(ctx, []string{"init", "factory/proposal", "--hypothesis", "h", "--target", "rework:down:1"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Labelled factory/proposal experiment")
	issue := loadLeadIssue(t, proj, "factory/proposal")
	if !experiment.Is(issue.Labels) || !slices.Contains(issue.Labels, "workflow") || issue.Status != "idea" {
		t.Fatalf("labels %v status %q", issue.Labels, issue.Status)
	}
	if b, err := experiment.Load(issue.FilePath); err != nil || b == nil || b.Target.Metric != "rework" {
		t.Fatalf("block %+v %v", b, err)
	}
	// Twice is fine: the label is not added again.
	out.Reset()
	if err := runExperiment(ctx, []string{"init", "factory/proposal", "--hypothesis", "h2", "--target", "rework:down:1"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Labelled") {
		t.Fatalf("labelled twice: %s", out.String())
	}
}

// Every issue-cli command a hint in cmd_experiment.go names exists, and so
// does each --flag it gives that command (in the command's help).
func TestExperimentHintsNameRealFlags(t *testing.T) {
	src, err := os.ReadFile("cmd_experiment.go")
	if err != nil {
		t.Fatal(err)
	}
	hintRe := regexp.MustCompile(`issue-cli ([a-z][a-z-]*)((?: [^\s"()]+)*)`)
	flagRe := regexp.MustCompile(`--([a-z][a-z-]*)`)
	n := 0
	for _, m := range hintRe.FindAllStringSubmatch(string(src), -1) {
		cmd := lookupCommand(m[1])
		if cmd == nil {
			t.Errorf("hint %q: no command %s", m[0], m[1])
			continue
		}
		n++
		help := cmd.ShortHelp + " " + cmd.LongHelp
		for _, f := range flagRe.FindAllStringSubmatch(m[2], -1) {
			if f[1] == "project" || f[1] == "json" || f[1] == "help" { // every command takes these
				continue
			}
			if !strings.Contains(help, "--"+f[1]) {
				t.Errorf("hint %q names --%s, which issue-cli %s does not take", strings.TrimSpace(m[0]), f[1], m[1])
			}
		}
	}
	if n < 5 {
		t.Fatalf("found only %d hints", n)
	}
}

func TestExperimentCandidatesWritesNothing(t *testing.T) {
	proj, root := makeExperimentProject(t)
	before := snapshotTree(t, root)
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"candidates"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "check-script — 3 issues: orchard/a, orchard/b, orchard/c")
	if after := snapshotTree(t, root); after != before {
		t.Fatalf("candidates wrote files:\nbefore %s\nafter  %s", before, after)
	}
	ctx, out, _ = newTestContext(proj, true)
	if err := runExperiment(ctx, []string{"candidates"}); err != nil {
		t.Fatal(err)
	}
	var got struct{ Patterns []experiment.Candidate }
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got.Patterns) != 1 {
		t.Fatalf("json: %v %s", err, out)
	}
}

func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b.WriteString(p + ":" + info.ModTime().String() + ";")
		}
		return nil
	})
	return b.String()
}

// writeBenchRun writes a run.json the way internal/bench does.
func writeBenchRun(t *testing.T, root, id string, passed int, firstTry float64) {
	t.Helper()
	tasks := []map[string]any{}
	for i, name := range []string{"t1", "t2", "t3", "t4"} {
		o := "fail"
		if i < passed {
			o = "pass"
		}
		tasks = append(tasks, map[string]any{"task": "proj/" + name, "outcome": o, "usage": map[string]any{"model": "claude-opus-5-5"}})
	}
	r := map[string]any{"id": id, "benchmark": "b1", "stop": "done", "started": time.Now(), "ended": time.Now(),
		"setup": map[string]any{"ref": id, "label": "x"}, "tasks": tasks, "holdout": map[string]any{"passed": 1, "total": 2},
		"summary":      map[string]any{"scored": 4, "passed": passed, "judge": -1, "tokens_known": true, "gates_first_try_rate": firstTry},
		"spent_tokens": 1000, "spent_usd": 2.5}
	data, _ := json.Marshal(r)
	dir := filepath.Join(root, "bench", "runs", "b1", id)
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, "run.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExperimentRecord(t *testing.T) {
	proj, root := makeExperimentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "gates_first_try:up:0.05", "--benchmark", "b1"}); err != nil {
		t.Fatal(err)
	}
	writeBenchRun(t, root, "base", 3, 0.6)
	writeBenchRun(t, root, "cand", 3, 0.7)
	writeBenchRun(t, root, "worse", 2, 0.9)

	out.Reset()
	if err := runExperiment(ctx, []string{"record", "factory/exp", "--baseline", "base", "--candidate", "cand"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Verdict: promising — gates_first_try 0.60→0.70")
	issue := loadLeadIssue(t, proj, "factory/exp")
	comments, _ := tracker.LoadComments(issue.FilePath)
	if len(comments) != 1 || !strings.HasPrefix(comments[0].Text, "experiment: promising") || !strings.Contains(comments[0].Text, "holdout passed 1/2→passed 1/2") {
		t.Fatalf("comments %+v", comments)
	}
	if !strings.Contains(issue.BodyRaw, "## Experiment result") || !strings.Contains(issue.BodyRaw, "| run | base | cand |") {
		t.Fatalf("no result section:\n%s", issue.BodyRaw)
	}
	b, _ := experiment.Load(issue.FilePath)
	if b.Baseline.Run != "base" || b.Candidate.Run != "cand" || b.Verdict != experiment.Promising {
		t.Fatalf("block %+v", b)
	}

	out.Reset()
	if err := runExperiment(ctx, []string{"record", "factory/exp", "--baseline", "base", "--candidate", "worse"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Verdict: rejected")
	assertContains(t, out.String(), "1 task(s) regressed: proj/t3")
	assertContains(t, out.String(), "--to parked --field parked_reason=")

	ctx, out, _ = newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "factory/exp")
	assertContains(t, out.String(), "rejected — gates_first_try")
}

// gitRepo makes a repo with main and an experiment branch changing files,
// and chdirs into it (experiment run works on the current checkout).
func gitRepo(t *testing.T, files ...string) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	repo := realTempDir(t)
	g := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	g("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "README"), []byte("x\n"), 0o644)
	g("add", ".")
	g("commit", "-q", "-m", "feat: base")
	g("checkout", "-q", "-b", "work/exp")
	for _, f := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(repo, f)), 0o755)
		os.WriteFile(filepath.Join(repo, f), []byte("change\n"), 0o644)
	}
	g("add", ".")
	g("commit", "-q", "-m", "feat: the change\n\nExperiment: factory/exp")
	t.Chdir(repo)
	return repo
}

func TestExperimentRunGuardAndDryRun(t *testing.T) {
	proj, _ := makeExperimentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "rework:down:1", "--benchmark", "b1", "--change", "workflow.yaml"}); err != nil {
		t.Fatal(err)
	}

	gitRepo(t, "workflow.yaml", "internal/bench/score.go")
	out.Reset()
	err := runExperiment(ctx, []string{"run", "factory/exp", "--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("protected path: %v", err)
	}
	assertContains(t, out.String(), "internal/bench/score.go: protected (internal/bench/**): the experiment can't change its own yardstick")
}

func TestExperimentRunDryRunPlansWithoutRunning(t *testing.T) {
	proj, root := makeExperimentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "rework:down:1", "--benchmark", "b1"}); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, "workflow.yaml")
	out.Reset()
	if err := runExperiment(ctx, []string{"run", "factory/exp", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Guard: ok")
	assertContains(t, out.String(), "Baseline: bench run b1 --setup")
	assertContains(t, out.String(), "(dry run — nothing was run)")
	if _, err := os.Stat(filepath.Join(root, "bench", "runs")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote runs: %v", err)
	}
	// A finished run that measured main's sha is reused.
	sha, _ := experiment.SHA(".", "main")
	writeBenchRun(t, root, sha, 2, 0.5)
	out.Reset()
	if err := runExperiment(ctx, []string{"run", "factory/exp", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Baseline: reuse run "+sha)
}

func TestExperimentRunWithoutMainRefuses(t *testing.T) {
	proj, _ := makeExperimentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "rework:down:1", "--benchmark", "b1"}); err != nil {
		t.Fatal(err)
	}
	repo := gitRepo(t, "workflow.yaml")
	// main moves on: the branch no longer contains it.
	exec.Command("git", "-C", repo, "checkout", "-q", "main").Run()
	os.WriteFile(filepath.Join(repo, "later"), []byte("y\n"), 0o644)
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "commit", "-q", "-m", "feat: later").Run()
	exec.Command("git", "-C", repo, "checkout", "-q", "work/exp").Run()
	out.Reset()
	if err := runExperiment(ctx, []string{"run", "factory/exp", "--dry-run"}); err == nil {
		t.Fatal("want a refusal")
	}
	assertContains(t, out.String(), "does not contain main")
}

func TestExperimentRollback(t *testing.T) {
	proj, _ := makeExperimentProject(t)
	repo := gitRepo(t, "workflow.yaml")
	exec.Command("git", "-C", repo, "checkout", "-q", "main").Run()
	if out, err := exec.Command("git", "-C", repo, "merge", "-q", "--ff-only", "work/exp").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"rollback", "factory/exp"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Branch revert/factory/exp reverts 1 commit(s) of factory/exp; main is unchanged.")
	b, _ := experiment.Load(loadLeadIssue(t, proj, "factory/exp").FilePath)
	if b == nil || b.Adopted == nil || b.Adopted.RolledBack != "revert/factory/exp" {
		t.Fatalf("block %+v", b)
	}
}

func TestYOLONeverPassesAnExperimentGate(t *testing.T) {
	proj := makeYOLOProject(t)
	path := filepath.Join(proj.IssueDir, "design.md")
	os.WriteFile(path, []byte("---\ntitle: \"Design\"\nstatus: \"in design\"\nlabels:\n  - experiment\n---\n\nBody.\n"), 0o644)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)
	_, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err == nil {
		t.Fatal("YOLO passed an experiment's gate")
	}
	assertContains(t, err.Error(), "experiments are adopted by the owner, even in YOLO")
	issue := loadLeadIssue(t, proj, "design")
	if comments, _ := tracker.LoadComments(issue.FilePath); len(comments) != 0 {
		t.Fatalf("a YOLO approval was written: %+v", comments)
	}
	wf := proj.LoadWorkflowForIssue(issue)
	if yoloEligible(wf, issue, "human-testing", "documentation", "documentation") {
		t.Fatal("experiment gate eligible")
	}
	ctx, out, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	runTransition(ctx, []string{"design", "--to", "backlog", "--dry-run"})
	assertContains(t, out.String(), "experiments are adopted by the owner")
}

// fakeBench stands in for `issue-cli bench` (factory/factory-benchmark-and-
// evals): each run writes a finished run.json under the label it was given,
// main's setup with first-try 0.5, any other with 0.8. No worker, no tokens.
func fakeBench(t *testing.T, root string, calls *[]string) {
	t.Helper()
	prev, had := commandRegistry["bench"]
	commandRegistry["bench"] = &Command{Name: "bench", Run: func(ctx *Context, args []string) error {
		*calls = append(*calls, strings.Join(args, " "))
		var setup, label string
		for i := 0; i+1 < len(args); i++ {
			switch args[i] {
			case "--setup":
				setup = args[i+1]
			case "--label":
				label = args[i+1]
			}
		}
		mainSHA, _ := experiment.SHA(".", "main")
		first := 0.8
		if setup == mainSHA {
			first = 0.5
		}
		id := "run-" + string(rune('a'+len(*calls)))
		writeBenchRun(t, root, id, 3, first)
		path := filepath.Join(root, "bench", "runs", "b1", id, "run.json")
		data, _ := os.ReadFile(path)
		var r map[string]any
		json.Unmarshal(data, &r)
		r["setup"] = map[string]any{"ref": setup, "sha": setup, "label": label}
		r["started"] = time.Now().Add(time.Duration(len(*calls)) * time.Second)
		data, _ = json.Marshal(r)
		return os.WriteFile(path, data, 0o644)
	}}
	t.Cleanup(func() {
		if had {
			commandRegistry["bench"] = prev
		} else {
			delete(commandRegistry, "bench")
		}
	})
}

func TestExperimentRunEndToEndWithFakeBench(t *testing.T) {
	proj, root := makeExperimentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "gates_first_try:up:0.1", "--benchmark", "b1"}); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, "workflow.yaml")
	var calls []string
	fakeBench(t, root, &calls)

	out.Reset()
	if err := runExperiment(ctx, []string{"run", "factory/exp"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "run b1 --setup ") || !strings.Contains(calls[1], "--label experiment:factory/exp@") {
		t.Fatalf("bench calls %q", calls)
	}
	assertContains(t, out.String(), "Verdict: promising — gates_first_try 0.50→0.80")
	b, _ := experiment.Load(loadLeadIssue(t, proj, "factory/exp").FilePath)
	if b.Baseline.Run != "run-b" || b.Candidate.Run != "run-c" {
		t.Fatalf("runs %+v %+v", b.Baseline, b.Candidate)
	}

	// The second run reuses the finished baseline for main's sha.
	calls = nil
	out.Reset()
	if err := runExperiment(ctx, []string{"run", "factory/exp"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || strings.Contains(calls[0], "experiment-baseline") {
		t.Fatalf("baseline not reused: %q", calls)
	}
	assertContains(t, out.String(), "Baseline: reuse run run-b")
}

func TestExperimentRunWithoutBenchSaysWhatToDo(t *testing.T) {
	proj, _ := makeExperimentProject(t)
	ctx, _, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "rework:down:1", "--benchmark", "b1"}); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, "workflow.yaml")
	if lookupCommand("bench") != nil {
		t.Skip("bench is built in")
	}
	err := runExperiment(ctx, []string{"run", "factory/exp"})
	if err == nil || !strings.Contains(err.Error(), "experiment record factory/exp --baseline <run> --candidate <run>") {
		t.Fatalf("%v", err)
	}
}

func TestExperimentShowAndList(t *testing.T) {
	proj, root := makeExperimentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "factory/exp")
	assertContains(t, out.String(), "not run yet")
	out.Reset()
	if err := runExperiment(ctx, []string{"show", "factory/exp"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "No experiment block yet: issue-cli experiment init factory/exp")

	runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "fewer refusals", "--target", "gates_first_try:up:0.05", "--benchmark", "b1", "--source", "r1.md"})
	writeBenchRun(t, root, "base", 3, 0.6)
	writeBenchRun(t, root, "cand", 3, 0.7)
	runExperiment(ctx, []string{"record", "factory/exp", "--baseline", "base", "--candidate", "cand"})
	out.Reset()
	if err := runExperiment(ctx, []string{"show", "factory/exp"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Hypothesis: fewer refusals", "Target:     gates_first_try up by 0.05", "Source:     retros/r1.md", "run base", "run cand", "Verdict:    promising — gates_first_try 0.60→0.70"} {
		assertContains(t, out.String(), want)
	}
	ctx, out, _ = newTestContext(proj, true)
	if err := runExperiment(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	var rows []struct{ Slug, Verdict string }
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].Verdict != "promising" {
		t.Fatalf("list json: %v %s", err, out)
	}
}

// A project whose issues live in its own repo: the issue's sidecar and the
// retros change on the branch too, and they are not the experiment's change.
func TestExperimentGuardSkipsTheTrackerInTheRepo(t *testing.T) {
	proj, root := makeExperimentProject(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	g := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	g("init", "-q", "-b", "main")
	g("add", "-A")
	g("commit", "-q", "-m", "feat: base")
	g("checkout", "-q", "-b", "work/exp")
	t.Chdir(root)
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "rework:down:1", "--benchmark", "b1", "--change", "workflow.yaml"}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "workflow.yaml"), []byte("x: 1\n"), 0o644)
	os.WriteFile(filepath.Join(root, "retros", "r9.md"), []byte("new retro\n"), 0o644)
	g("add", "-A")
	g("commit", "-q", "-m", "feat: the change")
	out.Reset()
	if err := runExperiment(ctx, []string{"run", "factory/exp", "--dry-run"}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertContains(t, out.String(), "Guard: ok")
}

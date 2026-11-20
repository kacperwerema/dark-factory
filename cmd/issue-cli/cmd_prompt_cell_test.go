package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/experiment"
)

// cellWorkflow: feature walks every status on the base prompts, tweak
// skips in design and has its own in progress prompt, bugfix appends to
// the base one.
const cellWorkflow = `statuses:
  - name: idea
    prompt: "check main"
  - name: in design
    prompt: "design it"
  - name: in progress
    prompt: "build it"
  - name: testing
    prompt: "test it"
transitions:
  - from: idea
    to: in design
  - from: in design
    to: in progress
  - from: idea
    to: in progress
  - from: in progress
    to: testing
default_type: feature
types:
  feature:
    statuses: []
  tweak:
    path: [idea, in progress, testing]
    statuses:
      - name: in progress
        prompt: "tweak: test first"
  bugfix:
    path: [idea, in progress, testing]
    statuses:
      - name: in progress
        prompt_append: "repro first"
`

// cellRepo is a repo whose main has cellWorkflow; branch work/exp (when
// edit is set) is cut from it and edits the base in progress prompt. It
// chdirs into the repo and returns it and main's first sha.
func cellRepo(t *testing.T, edit bool) (string, string) {
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
	os.WriteFile(filepath.Join(repo, "workflow.yaml"), []byte(cellWorkflow), 0o644)
	g("add", ".")
	g("commit", "-q", "-m", "feat: base")
	if edit {
		g("checkout", "-q", "-b", "work/factory/exp")
		os.WriteFile(filepath.Join(repo, "workflow.yaml"), []byte(strings.Replace(cellWorkflow, `"build it"`, `"build it; name the guard tests"`, 1)), 0o644)
		g("commit", "-q", "-am", "feat: the change\n\nExperiment: factory/exp")
	}
	t.Chdir(repo)
	sha, err := experiment.SHA(repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	return repo, sha
}

// saveBench writes benchmark b1 with tasks.
func saveBench(t *testing.T, tasks ...bench.Task) {
	t.Helper()
	b := bench.New("b1")
	b.Tasks = tasks
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
}

// AC3, AC4: a branch that edits only the base in progress prompt edits the
// feature and bugfix cells, not tweak's own; a tweak task is missed with
// why; nothing is written.
func TestPromptCellBranchEditsTheBasePrompt(t *testing.T) {
	proj, root := makeExperimentProject(t)
	initCtx, _, _ := newTestContext(proj, false)
	if err := runExperiment(initCtx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "tokens:down:0.1", "--benchmark", "b1"}); err != nil {
		t.Fatal(err)
	}
	repo, _ := cellRepo(t, true)
	saveBench(t, bench.Task{Project: "proj", Slug: "factory/f1", Type: "feature"}, bench.Task{Project: "proj", Slug: "factory/t1", Type: "tweak"})
	before := snapshotTree(t, root) + snapshotTree(t, repo)

	ctx, out, _ := newTestContext(proj, false)
	if err := runPromptCell(ctx, []string{"factory/exp"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	assertContains(t, got, "Change: workflow.yaml at main ")
	assertContains(t, got, "→ work/factory/exp ")
	assertContains(t, got, "feature · in progress  base prompt, shared by feature, bugfix")
	assertContains(t, got, "bugfix · in progress   base prompt, shared by feature, bugfix")
	if strings.Contains(got, "tweak · in progress") {
		t.Fatalf("tweak has its own prompt, so no tweak cell:\n%s", got)
	}
	assertContains(t, got, "✓ factory/f1 (feature): feature · in progress")
	assertContains(t, got, "✗ factory/t1 (tweak): tweak has its own in progress prompt")
	if !strings.HasSuffix(strings.TrimSpace(got), "Reach: 1 of 2 tasks") {
		t.Fatalf("last line:\n%s", got)
	}
	if after := snapshotTree(t, root) + snapshotTree(t, repo); after != before {
		t.Fatal("prompt-cell wrote a file")
	}

	ctx, out, _ = newTestContext(proj, false)
	if err := runPromptCell(ctx, []string{"factory/exp", "--json"}); err != nil {
		t.Fatal(err)
	}
	var r struct {
		Cells   []experiment.Cell `json:"cells"`
		Reached int               `json:"reached"`
		Checked int               `json:"checked"`
	}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || len(r.Cells) != 2 || r.Reached != 1 || r.Checked != 2 {
		t.Fatalf("json %+v %v\n%s", r, err, out.String())
	}
}

// TestPromptCellWritesNothing: no file under the project or the repo
// changes, also on the --cell path.
func TestPromptCellWritesNothing(t *testing.T) {
	proj, root := makeExperimentProject(t)
	repo, _ := cellRepo(t, false)
	before := snapshotTree(t, root) + snapshotTree(t, repo)
	ctx, _, _ := newTestContext(proj, false)
	if err := runPromptCell(ctx, []string{"factory/plain", "--cell", "in progress"}); err != nil {
		t.Fatal(err)
	}
	if after := snapshotTree(t, root) + snapshotTree(t, repo); after != before {
		t.Fatal("prompt-cell wrote a file")
	}
}

// AC5: a proposal with no branch names its cells with --cell; with no
// benchmark the tasks come from its ## Bench tasks list, typed from the
// board.
func TestPromptCellNamedCellsAndTheBenchTasksList(t *testing.T) {
	proj, root := makeExperimentProject(t)
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(root, "issues", rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Factory/f1.md", "---\ntitle: \"F1\"\nstatus: \"done\"\nsystem: \"Factory\"\ntype: \"feature\"\n---\n")
	write("Factory/t1.md", "---\ntitle: \"T1\"\nstatus: \"done\"\nsystem: \"Factory\"\ntype: \"tweak\"\n---\n")
	write("Factory/idea.md", "---\ntitle: \"Idea\"\nstatus: \"idea\"\nsystem: \"Factory\"\n---\n\n## Change\n\nThe in progress prompt.\n\n## Bench tasks\n\n- factory/f1\n- `factory/t1` (a tweak)\n- factory/gone\n")
	cellRepo(t, false)

	ctx, out, _ := newTestContext(proj, false)
	err := runPromptCell(ctx, []string{"factory/idea"})
	if err == nil || !strings.Contains(err.Error(), "--cell") {
		t.Fatalf("no branch and no --cell should name --cell: %v", err)
	}
	if err := runPromptCell(ctx, []string{"factory/idea", "--cell", "in progress"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	assertContains(t, got, "Named: --cell in progress, read from workflow.yaml at main ")
	assertContains(t, got, "feature · in progress  base prompt, shared by feature, bugfix")
	assertContains(t, got, "Tasks (the proposal's ## Bench tasks, 3):")
	assertContains(t, got, "✓ factory/f1 (feature): feature · in progress")
	assertContains(t, got, "✗ factory/t1 (tweak): tweak has its own in progress prompt")
	assertContains(t, got, "– factory/gone: no issue factory/gone on this board")
	assertContains(t, got, "Reach: 1 of 2 tasks (1 not read)")

	out.Reset()
	if err := runPromptCell(ctx, []string{"factory/idea", "--cell", "tweak:in design"}); err == nil || !strings.Contains(err.Error(), "tweak's path skips in design") {
		t.Fatalf("a cell off the type's path: %v", err)
	}
}

// AC6: a task that starts after the edited status never reads its prompt.
func TestPromptCellTaskStartingAfterTheCell(t *testing.T) {
	proj, _ := makeExperimentProject(t)
	cellRepo(t, true)
	saveBench(t, bench.Task{Project: "proj", Slug: "factory/f1", Type: "feature", Start: "testing"},
		bench.Task{Project: "other", Slug: "x/y", Type: "feature"})
	ctx, out, _ := newTestContext(proj, false)
	if err := runPromptCell(ctx, []string{"factory/exp", "--benchmark", "b1"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✗ factory/f1 (feature): starts at testing, after in progress")
	assertContains(t, out.String(), "– other/x/y: from project other: its workflow is not read")
	assertContains(t, out.String(), "Reach: 0 of 1 tasks (1 not read): the runs would measure nothing this change does")
}

// mainMoves commits a file on main after the branch was cut.
func mainMoves(t *testing.T, repo string) {
	t.Helper()
	for _, args := range [][]string{{"checkout", "-q", "main"}, {"commit", "-q", "--allow-empty", "-m", "feat: later"}, {"checkout", "-q", "work/factory/exp"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// AC7, AC9: after main moves, the branch cut from the pinned run's commit
// passes the guard with --baseline and prints the pin and the Reach line;
// without it the refusal names --baseline.
func TestExperimentRunPinnedBaselineSurvivesAMainMove(t *testing.T) {
	proj, root := makeExperimentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "gates_first_try:up:0.1", "--benchmark", "b1"}); err != nil {
		t.Fatal(err)
	}
	repo, pinned := cellRepo(t, true)
	saveBench(t, bench.Task{Project: "proj", Slug: "factory/f1", Type: "feature"}, bench.Task{Project: "proj", Slug: "factory/t1", Type: "tweak"})
	writeBenchRun(t, root, pinned, 3, 0.5) // a finished run that measured the cut point
	mainMoves(t, repo)

	out.Reset()
	if err := runExperiment(ctx, []string{"run", "factory/exp", "--dry-run"}); err == nil {
		t.Fatal("want a refusal: the branch does not contain main")
	}
	assertContains(t, out.String(), "does not contain main")
	assertContains(t, out.String(), "--baseline <run> (newest finished: "+pinned+")")

	out.Reset()
	if err := runExperiment(ctx, []string{"run", "factory/exp", "--dry-run", "--baseline", pinned}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got := out.String()
	assertContains(t, got, "Guard: ok (no protected path, cut from the pinned "+pinned[:7]+")")
	assertContains(t, got, "Baseline: pinned run "+pinned+" ("+pinned[:7]+"; main is 1 commit(s) past it)")
	assertContains(t, got, "Reach: 1 of 2 tasks (issue-cli prompt-cell factory/exp --ref HEAD)")
	assertContains(t, got, "(dry run — nothing was run)")

	// The real run never runs the pinned baseline again, and records it.
	var calls []string
	fakeBench(t, root, &calls)
	out.Reset()
	if err := runExperiment(ctx, []string{"run", "factory/exp", "--baseline", pinned}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(calls) != 1 || strings.Contains(calls[0], "experiment-baseline") {
		t.Fatalf("bench calls %q", calls)
	}
	b, _ := experiment.Load(loadLeadIssue(t, proj, "factory/exp").FilePath)
	if b.Baseline == nil || b.Baseline.Ref != "pin" || b.Baseline.SHA != pinned || b.Baseline.Run != pinned {
		t.Fatalf("baseline %+v", b.Baseline)
	}
}

// AC8: a branch that took in main's commits after the pin is refused, and
// so is a pin that did not finish or is not a run of the benchmark.
func TestExperimentRunPinnedBaselineRefusals(t *testing.T) {
	proj, root := makeExperimentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runExperiment(ctx, []string{"init", "factory/exp", "--hypothesis", "h", "--target", "rework:down:1", "--benchmark", "b1"}); err != nil {
		t.Fatal(err)
	}
	repo, pinned := cellRepo(t, true)
	writeBenchRun(t, root, pinned, 3, 0.5)
	mainMoves(t, repo)
	if out, err := exec.Command("git", "-C", repo, "merge", "-q", "--no-edit", "-m", "chore(merge): main", "main").CombinedOutput(); err != nil {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	out.Reset()
	if err := runExperiment(ctx, []string{"run", "factory/exp", "--dry-run", "--baseline", pinned}); err == nil {
		t.Fatal("want a refusal: the branch has main's later commits")
	}
	assertContains(t, out.String(), "has main's commits after the pinned run's "+pinned[:7])

	path := filepath.Join(root, "bench", "runs", "b1", "half", "run.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"id":"half","benchmark":"b1","stop":"budget","setup":{"sha":"`+pinned+`"}}`), 0o644)
	if err := runExperiment(ctx, []string{"run", "factory/exp", "--dry-run", "--baseline", "half"}); err == nil || !strings.Contains(err.Error(), "did not finish (budget)") {
		t.Fatalf("unfinished pin: %v", err)
	}
	if err := runExperiment(ctx, []string{"run", "factory/exp", "--dry-run", "--baseline", "nope"}); err == nil || !strings.Contains(err.Error(), "no run nope of benchmark b1") {
		t.Fatalf("missing pin: %v", err)
	}
}

package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A docs eval: the development workflow scores a docs change by its diff,
// graded with the eval's rubric and assertions; the merged diff is the
// reference.
const docsWorkflow = fixtureWorkflow + `default_type: feature
types:
  feature: {}
  docs: {}
evals:
  - id: docs
    types: [docs]
    score: diff
    rubric: ["says what the issue asks"]
    assert:
      - {type: icontains, value: GOOD}
`

func docsTask(f *fixture) Task {
	t := f.task
	t.Slug, t.Type, t.Hidden, t.HiddenTests = "calc/fix-add", "docs", nil, nil
	return t
}

// AC: a score: diff eval grades the worker's diff (new files included) and
// reads the known good diff as its reference.
func TestRunScoresADocsTaskByTheWorkersDiff(t *testing.T) {
	calc := newFixture(t)
	write(t, filepath.Join(calc.repo, "workflow.yaml"), docsWorkflow)
	env := calc.env(t)
	env.Keep = true
	b := calc.benchmark(`cat > /dev/null; printf 'GOOD docs\n' > README.md; echo '{}'`, docsTask(calc))
	b.Scorer = stubScorer(t)
	run, err := Execute(context.Background(), b, env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if r.Outcome != OutcomePass || r.Eval != "docs" || r.EvalScore != 1 || r.EvalTotal != 1 {
		t.Fatalf("record = %+v", r)
	}
	dir := filepath.Join(env.TmpRoot, run.ID, "01", "eval")
	out, _ := os.ReadFile(filepath.Join(dir, "output.txt"))
	if !strings.Contains(string(out), "README.md") || !strings.Contains(string(out), "+GOOD docs") {
		t.Errorf("the graded text is the worker's diff: %q", out)
	}
	ref, _ := os.ReadFile(filepath.Join(dir, "reference.txt"))
	if !strings.Contains(string(ref), "a + b") {
		t.Errorf("the reference is the merged diff: %q", ref)
	}
}

func TestRunFailsADiffTaskWithNoChange(t *testing.T) {
	calc := newFixture(t)
	write(t, filepath.Join(calc.repo, "workflow.yaml"), docsWorkflow)
	b := calc.benchmark(`cat > /dev/null; echo '{}'`, docsTask(calc))
	b.Scorer = stubScorer(t)
	run, err := Execute(context.Background(), b, calc.env(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r := run.Tasks[0]; r.Outcome != OutcomeFail || r.Reason != "no diff against the base" || r.EvalScore != 0 {
		t.Fatalf("record = %+v", r)
	}
}

func TestCutDiffKeepsAGraderPromptBounded(t *testing.T) {
	long := strings.Repeat("x", diffLimit+10)
	if got := cutDiff(long); len(got) > diffLimit+20 || !strings.HasSuffix(got, "(cut)\n") {
		t.Fatalf("cut to %d bytes", len(got))
	}
	if cutDiff("short") != "short" {
		t.Fatal("short text changed")
	}
}

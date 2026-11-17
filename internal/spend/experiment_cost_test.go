package spend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
)

// costFixture is a bench dir with benchmark b1 of four tasks and, when
// withRun, a finished run of two tasks: 30 min and 10M tokens each.
func costFixture(t *testing.T, withRun bool) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(cfg, "ai-workflow-engine", "bench"))
	b := bench.New("b1")
	for _, s := range []string{"a", "b", "c", "d"} {
		b.Tasks = append(b.Tasks, bench.Task{Project: "p", Slug: s})
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if withRun {
		at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
		r := &bench.Run{ID: "r1", Benchmark: "b1", Started: at, Ended: at.Add(time.Hour), Stop: bench.StopDone}
		u := bench.Usage{Known: true, Model: "claude-opus-5-5", Input: 9_000_000, Output: 1_000_000}
		recs := []bench.TaskRecord{
			{Task: "p/a", Outcome: bench.OutcomePass, WallSec: 1500, ScoreWallSec: 300, Usage: u},
			{Task: "p/b", Outcome: bench.OutcomePass, WallSec: 1800, Usage: u},
		}
		if err := bench.Save(r, recs); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

func TestExperimentCostSaysWhenThereIsNothingToEstimateFrom(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	if c := ExperimentCost("b1", "", "abc1234def"); !strings.Contains(c.Line, "benchmark b1 is not set up") || len(c.Detail) != 0 {
		t.Fatalf("no benchmark: %+v", c)
	}
	costFixture(t, false)
	if c := ExperimentCost("b1", "", "abc1234def"); !strings.Contains(c.Line, "b1 has no finished run") {
		t.Fatalf("no run: %+v", c)
	}
}

func TestExperimentCostBaselineAndCandidateWithPricesUnset(t *testing.T) {
	costFixture(t, true)
	c := ExperimentCost("b1", "", "abc1234def")
	// 4 tasks × 30 min × 2 runs = 4 h; 4 × 10M × 2 = 80M tokens.
	if c.Line != "≈ 80M tokens, 4.0 h, prices.yaml unset" {
		t.Fatalf("line = %q", c.Line)
	}
	all := strings.Join(c.Detail, "\n")
	for _, want := range []string{"2 runs × 4 tasks: the baseline on main abc1234 and the candidate", "from run r1's mean per task (30 min, 10M tokens)", "no prices.yaml at"} {
		if !strings.Contains(all, want) {
			t.Fatalf("detail lacks %q:\n%s", want, all)
		}
	}
}

func TestExperimentCostReusedBaselineIsOneRunAndPriced(t *testing.T) {
	cfg := costFixture(t, true)
	path := filepath.Join(cfg, "ai-workflow-engine", "prices.yaml")
	if err := os.WriteFile(path, []byte("prices:\n  claude-opus-5-5: {in: 1, out: 5}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := ExperimentCost("b1", "base1", "abc1234def")
	// per task $9 + $5 = $14; 4 tasks, one run.
	if c.Line != "≈ 40M tokens, 2.0 h, $56.00" {
		t.Fatalf("line = %q", c.Line)
	}
	if !strings.Contains(c.Detail[0], "1 run × 4 tasks: the candidate only, baseline run base1 on main abc1234 is reused") || len(c.Detail) != 2 {
		t.Fatalf("detail = %q", c.Detail)
	}
	// A model the file doesn't name: no figure, and the note says where to add it.
	if err := os.WriteFile(path, []byte("prices:\n  other-model: {in: 1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c = ExperimentCost("b1", "base1", "abc1234def")
	if !strings.HasSuffix(c.Line, ", USD unknown") || !strings.Contains(strings.Join(c.Detail, "\n"), "cost unknown for claude-opus-5-5: add it to") {
		t.Fatalf("unpriced: %+v", c)
	}
}

package viewer

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

// TestExperimentPageShowsTheCostPreview: an experiment issue that hasn't
// run shows a "Cost preview" row in its Fields card, the estimate
// experiment run --dry-run prints; other issues and a judged one don't.
func TestExperimentPageShowsTheCostPreview(t *testing.T) {
	root := t.TempDir()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(cfg, "ai-workflow-engine", "bench"))
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	p := &tracker.Project{Slug: "proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root}
	exp := &tracker.Issue{Slug: "factory/exp", Labels: []string{"experiment"}, FilePath: filepath.Join(root, "issues", "Factory", "exp.md")}
	if err := os.MkdirAll(filepath.Dir(exp.FilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if row := stationExperimentCost(p, &tracker.Issue{Slug: "factory/plain", FilePath: filepath.Join(root, "issues", "plain.md")}); row != nil {
		t.Fatalf("plain issue: %+v", row)
	}
	block := &experiment.Block{Hypothesis: "h", Benchmark: "b1"}
	if err := experiment.Save(exp.FilePath, block); err != nil {
		t.Fatal(err)
	}
	if row := stationExperimentCost(p, exp); row == nil || row.Label != "Cost preview" || !strings.Contains(row.Value, "benchmark b1 is not set up") {
		t.Fatalf("no benchmark: %+v", row)
	}

	b := bench.New("b1")
	b.Tasks = []bench.Task{{Project: "p", Slug: "a"}, {Project: "p", Slug: "b"}}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	run := &bench.Run{ID: "r1", Benchmark: "b1", Started: at, Ended: at.Add(time.Hour), Stop: bench.StopDone}
	if err := bench.Save(run, []bench.TaskRecord{{Task: "p/a", Outcome: bench.OutcomePass, WallSec: 3600,
		Usage: bench.Usage{Known: true, Model: "claude-opus-5-5", Input: 50_000_000}}}); err != nil {
		t.Fatal(err)
	}
	row := stationExperimentCost(p, exp)
	if row == nil || row.Value != "≈ 200M tokens, 4.0 h, prices.yaml unset · baseline + candidate" {
		t.Fatalf("estimate: %+v", row)
	}
	if !strings.Contains(strings.Join(row.Detail, "\n"), "2 runs × 2 tasks: the baseline on main") {
		t.Fatalf("detail: %q", row.Detail)
	}

	block.Verdict = experiment.Rejected
	if err := experiment.Save(exp.FilePath, block); err != nil {
		t.Fatal(err)
	}
	if row := stationExperimentCost(p, exp); row != nil {
		t.Fatalf("judged experiment: %+v", row)
	}
}

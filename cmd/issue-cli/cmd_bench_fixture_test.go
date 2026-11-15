package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestBenchFixtureInitThenCandidatesThenTheOwnersTaskAdd(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(t.TempDir(), "bench"))
	dir := filepath.Join(t.TempDir(), "fx")
	config := filepath.Join(t.TempDir(), "projects.yaml")
	asOwner(t)
	ctx, out, _ := newTestContext(nil, false)
	if err := runBench(ctx, []string{"fixture", "init", dir, "--config", config}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"✓ Fixture project written to", "tally/topwords-the-n-most-frequent-words", "added project bench-fixture to", "bench task add <benchmark> <slug>"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("fixture init said no %q:\n%s", want, out)
		}
	}
	// No benchmark is written by it: only the owner adds tasks.
	if entries, _ := os.ReadDir(os.Getenv("ISSUE_VIEWER_BENCH_DIR")); len(entries) != 0 {
		t.Errorf("fixture init wrote into the benchmark folder: %v", entries)
	}
	ctx, _, _ = newTestContext(nil, false)
	if err := runBench(ctx, []string{"fixture", "init", filepath.Join(t.TempDir(), "again"), "--config", config}); err == nil || !strings.Contains(err.Error(), "already has a project bench-fixture") {
		t.Errorf("a second fixture into the same config: %v", err)
	}
	cfg, err := tracker.LoadProjectsConfig(config)
	if err != nil || len(cfg.Projects) != 1 {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	proj := &cfg.Projects[0]

	ctx, out, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"candidates"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"stages: design · code (3 tests) · docs (1 file)", "stages: no design: never went through a design status", "only the owner adds, removes or changes a task"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("candidates said no %q:\n%s", want, out)
		}
	}

	slug := "tally/topwords-the-n-most-frequent-words"
	asWorker(t)
	ctx, _, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"task", "add", "golden", slug}); err == nil || !strings.Contains(err.Error(), "only the owner changes the benchmark") {
		t.Errorf("task add of a fixture task from a worker: %v", err)
	}
	asOwner(t)
	ctx, out, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"task", "add", "golden", slug, "--docs-rubric", "Names the tie order"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stages: design ✓ · code 3 tests · docs rubric 4, reference 1 file") {
		t.Errorf("task add said:\n%s", out)
	}
	b, err := bench.Load("golden")
	if err != nil || len(b.Tasks) != 1 || b.Tasks[0].Stages == nil || b.Tasks[0].Stages.Design == nil {
		t.Fatalf("benchmark = %+v, %v", b, err)
	}
	raw, _ := os.ReadFile(b.Path())
	if !strings.Contains(string(raw), "stages:") || !strings.Contains(string(raw), "reference: design.md") {
		t.Errorf("benchmark file has no stages block:\n%s", raw)
	}
	ctx, out, _ = newTestContext(proj, false)
	if err := runBench(ctx, []string{"show", "golden"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stages: design ✓") {
		t.Errorf("show said:\n%s", out)
	}
}

// The Next lines run as printed: with --config they carry the file the entry
// went into, without it they stay as they were.
func TestFixtureInitNextCarriesConfig(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(t.TempDir(), "bench"))
	asOwner(t)
	config := filepath.Join(realTempDir(t), "my projects.yaml")
	ctx, out, _ := newTestContext(nil, false)
	if err := runBench(ctx, []string{"fixture", "init", filepath.Join(t.TempDir(), "fx"), "--config", config}); err != nil {
		t.Fatal(err)
	}
	q := shell.Quote(config)
	for _, want := range []string{
		"\nNext: issue-cli --config " + q + " --project bench-fixture bench candidates\n",
		"the owner adds: issue-cli --config " + q + " --project bench-fixture bench task add <benchmark> <slug>\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("with --config, no %q:\n%s", want, out)
		}
	}

	ctx, out, _ = newTestContext(nil, false)
	if err := runBench(ctx, []string{"fixture", "init", filepath.Join(t.TempDir(), "fx")}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\nNext: issue-cli --project bench-fixture bench candidates\n",
		"the owner adds: issue-cli --project bench-fixture bench task add <benchmark> <slug>\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("without --config, no %q:\n%s", want, out)
		}
	}
}

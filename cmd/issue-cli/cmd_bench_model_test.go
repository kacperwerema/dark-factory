package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
)

func TestBenchRunModelFlag(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	asOwner(t)
	b := bench.New("models")
	b.Tasks = []bench.Task{{Project: "orchard", Slug: "a"}}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	var got bench.Options
	var worker string
	saveExec, saveCLI := benchExecute, benchCLIFor
	t.Cleanup(func() { benchExecute, benchCLIFor = saveExec, saveCLI })
	benchExecute = func(_ context.Context, bm *bench.Benchmark, _ bench.Env, opt bench.Options) (*bench.Run, error) {
		got, worker = opt, bm.Worker
		return &bench.Run{ID: "r", Benchmark: bm.Name, Stop: bench.StopDone, Setup: opt.Setup}, nil
	}
	benchCLIFor = func(context.Context, string, string, string, io.Writer) (string, string, string, error) {
		return "issue-cli", "", "", nil
	}
	run := func(args ...string) (string, string, error) {
		ctx, out, errb := newTestContext(nil, false)
		err := runBench(ctx, append([]string{"run", "models"}, args...))
		return out.String(), errb.String(), err
	}
	out, log, err := run("--model", "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	if got.Setup.Model != "claude-sonnet-5-5" || !strings.HasPrefix(worker, "claude --model claude-sonnet-5-5 -p ") {
		t.Errorf("setup %+v worker %q", got.Setup, worker)
	}
	if !strings.Contains(log, "on model claude-sonnet-5-5") || !strings.Contains(out, "model claude-sonnet-5-5") || !strings.Contains(out, "by model and type") {
		t.Errorf("log:\n%s\nout:\n%s", log, out)
	}
	if _, _, err := run(); err != nil || got.Setup.Model != "" || strings.Contains(worker, "--model") {
		t.Errorf("no --model: err %v setup %+v worker %q", err, got.Setup, worker)
	}
	got = bench.Options{}
	if _, _, err := run("--model", "x;rm"); err == nil || !strings.Contains(err.Error(), "not a model name") {
		t.Errorf("bad name: %v", err)
	}
	if got.Setup.IssueCLI != "" {
		t.Errorf("a refused run started: %+v", got)
	}
}

func TestWithModelPlacesTheFlagAfterTheCommand(t *testing.T) {
	for in, want := range map[string]string{
		bench.DefaultWorker:      "claude --model m " + strings.TrimPrefix(bench.DefaultWorker, "claude "),
		bench.DefaultCodexWorker: "codex exec --model m " + strings.TrimPrefix(bench.DefaultCodexWorker, "codex exec "),
		"claude":                 "claude --model m",
	} {
		if got := bench.WithModel(in, "m"); got != want {
			t.Errorf("WithModel(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	if got := bench.WithModel("claude -p", ""); got != "claude -p" {
		t.Errorf("no model: %q", got)
	}
}

// Two runs of the same tasks, one on the CLI's own model and one on
// Sonnet, sit side by side by type in bench compare.
func TestBenchCompareTwoModelsSideBySide(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	asWorker(t)
	at := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	opus := &bench.Run{ID: "r1", Benchmark: "m", Stop: bench.StopDone, Started: at, Setup: bench.Setup{Agent: "claude"}}
	sonnet := &bench.Run{ID: "r2", Benchmark: "m", Stop: bench.StopDone, Started: at.Add(time.Hour), Setup: bench.Setup{Agent: "claude", Model: "claude-sonnet-5-5"}}
	recs := []bench.TaskRecord{{Task: "p/a", Type: "docs", Outcome: bench.OutcomePass}, {Task: "p/b", Type: "feature", Outcome: bench.OutcomePass}}
	if err := bench.Save(opus, recs); err != nil {
		t.Fatal(err)
	}
	recs[1].Outcome = bench.OutcomeFail
	if err := bench.Save(sonnet, recs); err != nil {
		t.Fatal(err)
	}
	tab := bench.ByAgent(opus, sonnet)
	if tab == nil {
		t.Fatal("no table for two models")
	}
	if r, ok := tab.Row("claude · claude-sonnet-5-5", "feature"); !ok || r.Passed != 0 || r.Scored != 1 {
		t.Errorf("sonnet feature row = %+v ok=%v", r, ok)
	}
	if r, ok := tab.Row("claude", "feature"); !ok || r.Passed != 1 {
		t.Errorf("default feature row = %+v ok=%v", r, ok)
	}
	ctx, out, _ := newTestContext(nil, false)
	// Sonnet failed a task Opus passed: compare exits 1 (regressed).
	if err := runBench(ctx, []string{"compare", "m/r1", "m/r2"}); err != nil {
		if e, ok := err.(*exitCodeError); !ok || e.Code != 1 {
			t.Fatal(err)
		}
	}
	if !strings.Contains(out.String(), "By agent and model") || !strings.Contains(out.String(), "claude · claude-sonnet-5-5") {
		t.Errorf("compare out:\n%s", out)
	}
}

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/standin"
)

// smokeCLI points bench smoke at this test binary run as issue-cli, through
// a stand-in on its own PATH folder. The binary comes from SMOKE_TEST_SELF:
// a run drops every ISSUE_CLI_* variable from its children.
func smokeCLI(t *testing.T) *string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SMOKE_TEST_SELF", self)
	cli := filepath.Join(t.TempDir(), "bin", "issue-cli")
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	standin.Write(t, cli, "ISSUE_CLI_TEST_AS_MAIN=1 exec \"$SMOKE_TEST_SELF\" \"$@\"\n")
	saveCLI, saveScorer := benchCLIFor, benchSmokeScorer
	t.Cleanup(func() { benchCLIFor, benchSmokeScorer = saveCLI, saveScorer })
	setup := new(string)
	// The setup ref goes to the same builder bench run --setup uses.
	benchCLIFor = func(_ context.Context, ref, _, _ string, _ io.Writer) (string, string, string, error) {
		*setup = ref
		if ref == "" {
			return cli, "", "", nil
		}
		return cli, ref, "0123456789abcdef", nil
	}
	t.Setenv("TMPDIR", realTempDir(t))
	return setup
}

func TestBenchSmokePassesOnTheFixture(t *testing.T) {
	benchDir := filepath.Join(t.TempDir(), "bench")
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", benchDir)
	asWorker(t) // no model is called: an agent session may run it, no --quote
	setup := smokeCLI(t)
	ctx, out, errb := newTestContext(nil, false)
	if err := runBench(ctx, []string{"smoke", "--setup", "work/new-prompts"}); err != nil {
		t.Fatalf("smoke: %v\n%s\n%s", err, out, errb)
	}
	for _, name := range bench.SmokeCheckNames {
		if !strings.Contains(out.String(), "✓ "+name+": ") {
			t.Errorf("no ✓ %s line:\n%s", name, out)
		}
	}
	if !strings.Contains(out.String(), "bench smoke: PASS (3 tasks, ") || !strings.Contains(out.String(), "no model, no tokens") {
		t.Errorf("no PASS line:\n%s", out)
	}
	if strings.Contains(errb.String(), "no login found") {
		t.Errorf("the stand-in's run printed the login note:\n%s", errb)
	}
	// The owner's bench folder holds only smoke.json; the temp folder is gone.
	entries, _ := os.ReadDir(benchDir)
	if len(entries) != 1 || entries[0].Name() != "smoke.json" {
		t.Errorf("bench folder holds %v, want only smoke.json", entries)
	}
	if left, _ := filepath.Glob(filepath.Join(os.Getenv("TMPDIR"), "awe-bench-smoke-*")); len(left) != 0 {
		t.Errorf("smoke folder left behind: %v", left)
	}
	rec, ok := bench.LoadSmoke()
	if !ok || rec.IssueCLIVersion != Version || rec.Ref != "work/new-prompts" || rec.SHA != "0123456789abcdef" {
		t.Errorf("smoke.json = %+v (ok %v), want version %s and the setup ref", rec, ok, Version)
	}
	if *setup != "work/new-prompts" {
		t.Errorf("the issue-cli was built from %q, want the --setup ref", *setup)
	}
}

func TestBenchSmokeKeepLeavesItsFolder(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", filepath.Join(t.TempDir(), "bench"))
	smokeCLI(t)
	// A scorer that dies stops nothing but the stages: quick to fail, and the
	// folder still has to stay.
	benchSmokeScorer = "false"
	ctx, out, _ := newTestContext(nil, false)
	_ = runBench(ctx, []string{"smoke", "--keep"})
	var kept string
	for _, line := range strings.Split(out.String(), "\n") {
		if p, ok := strings.CutPrefix(line, "kept: "); ok {
			kept = p
		}
	}
	if kept == "" {
		t.Fatalf("no kept: line:\n%s", out)
	}
	for _, sub := range []string{"fx/repo", "bench/smoke.yaml", "standin/worker.sh"} {
		if _, err := os.Stat(filepath.Join(kept, sub)); err != nil {
			t.Errorf("kept folder has no %s: %v", sub, err)
		}
	}
}

func TestBenchSmokeFailsWhenTheHarnessBreaks(t *testing.T) {
	benchDir := filepath.Join(t.TempDir(), "bench")
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", benchDir)
	smokeCLI(t)
	// A scorer that writes no result: the docs and design rubrics go
	// ungraded, as with a broken promptfoo.
	benchSmokeScorer = "true"
	ctx, out, _ := newTestContext(nil, false)
	err := runBench(ctx, []string{"smoke"})
	var code *exitCodeError
	if !errors.As(err, &code) || code.Code != 1 {
		t.Fatalf("err = %v, want exit 1\n%s", err, out)
	}
	if !strings.Contains(out.String(), "✗ stages: ") || !strings.Contains(out.String(), "bench smoke: FAIL (stages)") {
		t.Errorf("want ✗ stages and FAIL (stages):\n%s", out)
	}
	// The rest still ran and passed: one broken piece names one check.
	for _, name := range []string{bench.SmokeRun, bench.SmokeTrace, bench.SmokeControl} {
		if !strings.Contains(out.String(), "✓ "+name+": ") {
			t.Errorf("no ✓ %s line:\n%s", name, out)
		}
	}
	if _, ok := bench.LoadSmoke(); ok {
		t.Error("a failed smoke wrote smoke.json")
	}
}

func TestBenchRunAndListNameTheLastSmoke(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	asOwner(t)
	list := func() string {
		ctx, out, _ := newTestContext(nil, false)
		if err := runBench(ctx, nil); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if out := list(); !strings.Contains(out, "smoke: none for this issue-cli ("+Version+"): issue-cli bench smoke") {
		t.Errorf("bench with no smoke:\n%s", out)
	}
	b := bench.New("hint")
	b.Tasks = []bench.Task{{Project: "orchard", Slug: "a"}}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	saveExec, saveCLI := benchExecute, benchCLIFor
	t.Cleanup(func() { benchExecute, benchCLIFor = saveExec, saveCLI })
	benchExecute = func(_ context.Context, bm *bench.Benchmark, _ bench.Env, opt bench.Options) (*bench.Run, error) {
		return &bench.Run{ID: "r", Benchmark: bm.Name, Stop: bench.StopDone, Setup: opt.Setup}, nil
	}
	cli := filepath.Join(t.TempDir(), "issue-cli")
	standin.Write(t, cli, "echo \"issue-cli $BENCH_HINT_VERSION\"\n")
	t.Setenv("BENCH_HINT_VERSION", "v9.9.9")
	benchCLIFor = func(context.Context, string, string, string, io.Writer) (string, string, string, error) {
		return cli, "", "", nil
	}
	runLog := func() string {
		ctx, _, errb := newTestContext(nil, false)
		if err := runBench(ctx, []string{"run", "hint"}); err != nil {
			t.Fatal(err)
		}
		return errb.String()
	}
	if log := runLog(); !strings.Contains(log, "smoke: none for this issue-cli (v9.9.9)") {
		t.Errorf("bench run with no smoke:\n%s", log)
	}
	at := time.Date(2026, 11, 17, 18, 0, 0, 0, time.Local)
	if err := bench.SaveSmoke(bench.SmokeRecord{At: at, IssueCLIVersion: "v9.9.9"}); err != nil {
		t.Fatal(err)
	}
	if log := runLog(); !strings.Contains(log, "smoke: PASS 2026-11-17 18:00 on v9.9.9") {
		t.Errorf("bench run after a smoke:\n%s", log)
	}
	// Another issue-cli: the smoke on v9.9.9 doesn't count for it.
	if out := list(); !strings.Contains(out, "smoke: none for this issue-cli ("+Version+")") || !strings.Contains(out, "the last PASS was on v9.9.9") {
		t.Errorf("bench after a smoke on another version:\n%s", out)
	}
}

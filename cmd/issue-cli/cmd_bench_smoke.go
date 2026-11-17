package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
)

// benchSmokeScorer replaces the smoke's stand-in scorer; tests break the
// harness with it.
var benchSmokeScorer = ""

// runBenchSmoke is bench smoke: the fixture's tasks run end to end with
// stand-ins (docs/benchmark.md "Smoke before a run"). It calls no model, so
// anyone may run it; the owner's bench folder gets only smoke.json.
func runBenchSmoke(ctx *Context, args []string) error {
	fs := newFlagSet("bench smoke", ctx)
	setup := fs.String("setup", "", "smoke the issue-cli built from this engine ref, as bench run --setup uses it")
	engine := fs.String("engine", "", "the engine checkout --setup is resolved in (default: the current git repository)")
	keep := fs.Bool("keep", false, "leave the smoke's folder (fixture, benchmark, sandboxes) in place")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return &usageError{msg: "usage: issue-cli bench smoke [--setup <ref>] [--engine <dir>] [--keep] [--json]"}
	}
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cli, ref, sha, err := benchCLIFor(runCtx, *setup, *engine, filepath.Join(os.TempDir(), "awe-bench"), ctx.Stderr)
	if err != nil {
		return err
	}
	version := smokeVersion(cliVersion(cli))
	if !ctx.JSONOutput {
		fmt.Fprintf(ctx.Stdout, "bench smoke: the fixture's tasks with a stand-in worker, judge and scorer (no model, no tokens), issue-cli %s\n", version)
	}
	res, err := bench.Smoke(runCtx, bench.SmokeOptions{
		Env: bench.Env{
			IssueCLI: cli, MemGuard: memGuard(ctx.Stderr), MemWait: 30 * time.Minute,
			Poll: time.Second, Log: ctx.Stderr, Now: ctx.now,
		},
		Setup:  bench.Setup{Ref: ref, SHA: sha, IssueCLI: cli, IssueCLIVersion: version},
		Report: func(r *bench.Run) string { var b strings.Builder; printRun(&b, r); return b.String() },
		Keep:   *keep,
		Scorer: benchSmokeScorer,
	})
	if err != nil {
		return err
	}
	if res.Pass {
		if err := bench.SaveSmoke(bench.SmokeRecord{At: ctx.now(), IssueCLIVersion: version, Ref: ref, SHA: sha, Seconds: res.Seconds}); err != nil {
			fmt.Fprintf(ctx.Stderr, "warning: smoke.json not written: %v\n", err)
		}
	}
	if ctx.JSONOutput {
		if err := writeJSON(ctx.Stdout, res); err != nil {
			return err
		}
	} else {
		for _, c := range res.Checks {
			mark := "✓"
			if !c.OK {
				mark = "✗"
			}
			fmt.Fprintf(ctx.Stdout, "%s %s: %s\n", mark, c.Name, c.Detail)
		}
		for _, name := range bench.SmokeCheckNames[len(res.Checks):] {
			fmt.Fprintf(ctx.Stdout, "- %s: not reached\n", name)
		}
		if res.Dir != "" {
			fmt.Fprintf(ctx.Stdout, "kept: %s\n", res.Dir)
		}
	}
	if res.Pass {
		if !ctx.JSONOutput {
			fmt.Fprintf(ctx.Stdout, "bench smoke: PASS (%d tasks, %.0fs)\n", res.Tasks, res.Seconds)
		}
		return nil
	}
	failed := res.Failed()
	if len(failed) == 0 {
		failed = []string{"not reached: " + strings.Join(bench.SmokeCheckNames[len(res.Checks):], ", ")}
	}
	if !ctx.JSONOutput {
		fmt.Fprintf(ctx.Stdout, "bench smoke: FAIL (%s)\n", strings.Join(failed, ", "))
	}
	return &exitCodeError{Code: 1}
}

// smokeVersion is the version word of an issue-cli version line
// ("issue-cli v0.63.0 …" → "v0.63.0"), what smoke.json records and bench run
// matches.
func smokeVersion(line string) string {
	f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "issue-cli "))
	if len(f) == 0 {
		return "unknown"
	}
	return f[0]
}

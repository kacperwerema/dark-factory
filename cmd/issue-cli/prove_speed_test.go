package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/timing"
)

// proveVsChecklist is how many checklist calls a plain prove link may cost
// on a slow machine: prove also reads git, the playtest runs and the
// sessions, so it runs a few times a checklist even when nothing is wrong.
const proveVsChecklist = 6

// A plain prove link (no --run) on a board whose checkout holds 400 other
// issues' playtest runs and 200 sessions, with each worktree holding a copy:
// under 1 s on a quiet machine, or proveVsChecklist× a checklist call timed
// beside it on a loaded one. Before the fix every results.yaml and session
// file was parsed to keep the issue's few.
func TestPlainProveLinkFastOnABigBoard(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 1000 playtest files")
	}
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	root := projectRoot(proj)
	var files []string
	write := func(p, body string) {
		files = append(files, p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var steps strings.Builder
	for s := 1; s <= 30; s++ {
		fmt.Fprintf(&steps, "  - id: TP%d\n    status: pass\n    proves: [AC1]\n    checks:\n      - kind: text\n        subject: \"#card-%d\"\n        passed: true\n", s, s)
	}
	for i := 0; i < 400; i++ {
		write(filepath.Join(root, ".playtest", "runs", "s"+strconv.Itoa(i%40), "r"+strconv.Itoa(i), "results.yaml"),
			fmt.Sprintf("session: s%d\nissue: cli/other-%d\nmode: auto\nstarted: \"2026-11-01T10:00:00Z\"\nsteps:\n%s", i%40, i, steps.String()))
	}
	for _, dir := range []string{root, filepath.Join(root, ".worktrees", "cli", "sample")} {
		for i := 0; i < 200; i++ {
			write(filepath.Join(dir, "playtest", "sessions", "s"+strconv.Itoa(i)+".yaml"),
				fmt.Sprintf("session: s%d\nissue: cli/other-%d\nscenario: board\nsteps:\n%s", i, i, steps.String()))
		}
	}

	timed := func(run func(*Context, []string) error, args ...string) time.Duration {
		t.Helper()
		ctx, _, _ := newTestContext(proj, false)
		start := time.Now()
		if err := run(ctx, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return time.Since(start)
	}
	round := 0
	prove := func() time.Duration {
		round++
		// A process parses each file once per mtime: a new one makes this
		// round a fresh process's cold read, not one warmed by the checklist.
		stamp := time.Now().Add(time.Duration(round) * time.Minute)
		for _, f := range files {
			if err := os.Chtimes(f, stamp, stamp); err != nil {
				t.Fatal(err)
			}
		}
		return timed(runProve, "cli/sample", "AC1", "--test", "TestCount"+strconv.Itoa(round))
	}
	checklist := func() time.Duration { return timed(runChecklist, "cli/sample") }
	r := timing.Check{Limit: time.Second, Ratio: proveVsChecklist}.Run(prove, checklist)
	if r.Slow {
		t.Errorf("plain prove link: %s (baseline: checklist)", r)
	}
	t.Logf("plain prove link: %s", r)
}

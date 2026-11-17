package ci

import (
	"time"

	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/playtestruns"
)

// Local CI's test results and the ux-test runs reach the health snapshot's
// tests family from here: health imports no package that plugs into it
// (docs/workbench-files.md, "The tests family").
func init() {
	health.SetTestResults(healthTestRuns)
	health.SetE2ERuns(healthE2ERuns)
}

// healthTestRuns are the newest n runs at or before now that wrote results
// of kind, newest first; the newest carries its per-test list.
func healthTestRuns(root, kind string, now time.Time, n int) []health.TestRun {
	runs, sums := RecentResults(root, kind, now, n)
	out := make([]health.TestRun, len(runs))
	for i, r := range runs {
		out[i] = health.TestRun{Finished: r.Finished, Summary: sums[i]}
	}
	if len(out) > 0 {
		out[0].Tests = ReadTests(root, sums[0])
	}
	return out
}

// healthE2ERuns reads the ux-test runs of the main checkout (an issue's own
// runs land there at done) and of local CI's worktree. .ci/results' e2e
// files are made from the CI worktree's runs, so they are not read again.
func healthE2ERuns(root string, from, to time.Time) (runs, steps int, flaky []string, ok bool) {
	c := playtestruns.Flaky([]string{playtestruns.RunsDir(root), playtestruns.RunsDir(WorktreePath(root))}, from, to)
	return c.Runs, c.Steps, c.Names, c.Runs > 0
}

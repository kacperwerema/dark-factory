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
	health.SetTestResults(healthTestRun)
	health.SetE2ERuns(healthE2ERuns)
}

// healthTestRun is the newest run at or before now that wrote results of
// kind, with its per-test list.
func healthTestRun(root, kind string, now time.Time) (health.TestRun, bool) {
	run, sum, ok := LatestResults(root, kind, now)
	if !ok {
		return health.TestRun{}, false
	}
	return health.TestRun{Finished: run.Finished, Summary: sum, Tests: ReadTests(root, sum)}, true
}

// healthE2ERuns reads the ux-test runs of the main checkout (an issue's own
// runs land there at done) and of local CI's worktree. .ci/results' e2e
// files are made from the CI worktree's runs, so they are not read again.
func healthE2ERuns(root string, from, to time.Time) (runs, steps int, flaky []string, ok bool) {
	c := playtestruns.Flaky([]string{playtestruns.RunsDir(root), playtestruns.RunsDir(WorktreePath(root))}, from, to)
	return c.Runs, c.Steps, c.Names, c.Runs > 0
}

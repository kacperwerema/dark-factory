package agentrun

import (
	"fmt"
	"runtime"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/testbudget"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// NumCPU is the machine's cores; a test sets another machine.
var NumCPU = runtime.NumCPU

// WorkerTestBudget is the test-thread budget a worker of proj gets and the dispatch
// step's line saying where it came from: projects.yaml test_threads:, else
// the cores over the project's auto-start max_workers (root is the project
// checkout; the state lives in the main checkout), at least
// testbudget.MachineWorkers.
func WorkerTestBudget(proj *tracker.Project, root string) (int, string) {
	cores := NumCPU()
	if proj != nil && proj.TestThreads > 0 {
		return proj.TestThreads, fmt.Sprintf("%d threads (projects.yaml test_threads:)", proj.TestThreads)
	}
	workers := 0
	if proj != nil && proj.Slug != "" {
		if st, err := autostart.Load(ci.RepoRoot(root), proj.Slug); err == nil && st.On {
			workers = st.MaxWorkers
		}
	}
	if workers < testbudget.MachineWorkers {
		workers = testbudget.MachineWorkers
	}
	n := testbudget.Threads(cores, workers, 0)
	return n, fmt.Sprintf("%d threads (%d cores / %d workers, at least %d): %s and GOFLAGS -p", n, cores, workers, testbudget.Min, testbudget.Var)
}

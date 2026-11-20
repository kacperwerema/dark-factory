package experiment

import (
	"fmt"
	"strings"
)

// Pinned is a finished run of benchmark that an experiment uses as its
// baseline instead of one at main's sha, and the commit it measured. A run
// keeps its commit when main moves, so one baseline can serve several
// candidates cut from that commit (experiment run --baseline).
func Pinned(benchDir, benchmark, id string) (*Run, string, error) {
	r, err := LoadRun(benchDir, benchmark, id)
	if err != nil {
		return nil, "", fmt.Errorf("--baseline %s: no run %s of benchmark %s (%s)", id, id, benchmark, RunsDir(benchDir, benchmark))
	}
	if r.Benchmark != "" && r.Benchmark != benchmark {
		return nil, "", fmt.Errorf("--baseline %s: a run of benchmark %s, not %s", id, r.Benchmark, benchmark)
	}
	if !r.Finished() {
		stop := strings.TrimSpace(r.Stop)
		if stop == "" {
			stop = "still running"
		}
		return nil, "", fmt.Errorf("--baseline %s: the run did not finish (%s); pin a finished one", id, stop)
	}
	sha := r.Setup.SHA
	if sha == "" {
		sha = r.Setup.Ref
	}
	if sha == "" {
		return nil, "", fmt.Errorf("--baseline %s: the run names no commit it measured (bench run --setup <ref>)", id)
	}
	return r, sha, nil
}

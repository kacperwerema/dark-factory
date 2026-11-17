package ci

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/testresults"
)

// The nightly slow run wrote e2e.json; a day of merges then ran the default
// tier more than keepRuns times. Prune used to keep the newest keepRuns
// records of all tiers, so the slow run and its results went and health
// said "no e2e run in local CI" while ci status --tier slow said green.
// Each tier keeps its own keepRuns: the next snapshot scores e2e_wait from
// the slow run's wall time.
func TestE2EWaitReadsTheSlowTiersWall(t *testing.T) {
	root := newRepo(t)
	now := time.Date(2026, 11, 16, 1, 0, 0, 0, time.Local)
	slowAt := now.Add(-22 * time.Hour)
	slow := &Run{Sha: "b7bdfc2", Branch: "main", Tier: "slow", Started: slowAt.Add(-40 * time.Minute), Finished: slowAt, State: Green,
		Results: map[string]testresults.Summary{"e2e": {Tests: 120, Passed: 120, WallS: 1500}}}
	if err := os.MkdirAll(RunsDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeRun(root, slow); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ResultsDir(root, slow.Sha, slow.Tier), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < keepRuns+10; i++ {
		at := slowAt.Add(time.Duration(i+1) * 10 * time.Minute)
		r := &Run{Sha: fmt.Sprintf("d%06d", i), Branch: "main", Tier: defaultTier, Started: at.Add(-5 * time.Minute), Finished: at, State: Green,
			Results: map[string]testresults.Summary{"unit": {Tests: 10, Passed: 10, WallS: 60}}}
		if err := writeRun(root, r); err != nil {
			t.Fatal(err)
		}
	}

	prune(root)

	if n := len(RecentRuns(root, 0)); n != keepRuns+1 {
		t.Errorf("kept %d runs, want %d default ones and the slow one", n, keepRuns+1)
	}
	if _, err := os.Stat(filepath.Join(RunsDir(root), "b7bdfc2.slow.json")); err != nil {
		t.Fatalf("the slow run was pruned: %v", err)
	}
	if _, err := os.Stat(ResultsDir(root, slow.Sha, slow.Tier)); err != nil {
		t.Errorf("the slow run's results folder was pruned: %v", err)
	}

	snap, _, err := health.Take(context.Background(), health.Env{Root: root, Branch: "main", Commit: run(t, root, "git", "rev-parse", "HEAD"), Now: now}, health.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := snap.Value("tests.wall_seconds", map[string]string{"kind": "e2e"}); !ok || v != 1500 {
		t.Errorf("tests.wall_seconds{kind=e2e} = %v, %v; want the slow run's 1500", v, ok)
	}
	l := map[string]string{"component": "e2e_wait"}
	if v, ok := snap.Value("score.component.value", l); !ok || v != 1500 {
		t.Errorf("e2e_wait = %v, %v; want data: the slow run's 1500 s", v, ok)
	}
}

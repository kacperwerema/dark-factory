package health

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/playtestruns"
)

// writeUXRun writes one ux-test run's results.yaml under root's
// .playtest/runs, as cmd/playtest does.
func writeUXRun(t *testing.T, root, session, name, build string, started time.Time, steps ...string) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "session: %s\nissue: ui/x\nmode: auto\nbuild: %q\nstarted: %q\nsteps:\n", session, build, started.Format(time.RFC3339))
	for _, s := range steps {
		id, status, _ := strings.Cut(s, "=")
		fmt.Fprintf(&b, "    - id: %s\n      status: %s\n", id, status)
	}
	dir := filepath.Join(playtestruns.RunsDir(root), session, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "results.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// plugRuns plugs in the real run reader over root's .playtest/runs, the way
// internal/ci does over the main checkout and its CI worktree.
func plugRuns(t *testing.T) {
	t.Helper()
	SetE2ERuns(func(root string, from, to time.Time) (int, int, []string, bool) {
		c := playtestruns.Flaky([]string{playtestruns.RunsDir(root)}, from, to)
		return c.Runs, c.Steps, c.Names, c.Runs > 0
	})
	t.Cleanup(func() { SetE2ERuns(nil) })
}

func flakyRows(t *testing.T, root string, now time.Time) *Snapshot {
	t.Helper()
	env := Env{Root: root, Now: now, Commit: "HEAD"}
	m, err := testsFamily(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	s := &Snapshot{}
	for _, r := range m {
		s.Metrics = append(s.Metrics, Row{ID: RowID(r.Name, r.Labels), Metric: r.Name, Labels: r.Labels, Value: r.Value, Group: "tests"})
	}
	return s
}

// One step red then green on one build is flaky; red then green across two
// builds is a fix, not a flake.
func TestFlakyShareFromRuns(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.Local)
	root, _ := healthRepo(t, now)
	plugRuns(t)
	writeUXRun(t, root, "board", "20261110-100000.000-auto", "abc1234", now.AddDate(0, 0, -5), "TP1=fail", "TP2=pass")
	writeUXRun(t, root, "board", "20261110-110000.000-auto", "abc1234", now.AddDate(0, 0, -5).Add(time.Hour), "TP1=pass", "TP2=pass")
	writeUXRun(t, root, "list", "20261111-100000.000-auto", "abc1234", now.AddDate(0, 0, -4), "TP1=fail")
	writeUXRun(t, root, "list", "20261112-100000.000-auto", "def5678", now.AddDate(0, 0, -3), "TP1=pass")

	s := flakyRows(t, root, now)
	want := map[string]float64{
		"tests.e2e.flaky_share{window=14d}": 0.333, // board/TP1 of board/TP1, board/TP2, list/TP1
		"tests.e2e.flaky_steps{window=14d}": 1,
		"tests.e2e.steps_run{window=14d}":   3,
		"tests.e2e.runs{window=14d}":        4,
		"tests.e2e.flaky{step=board/TP1}":   1,
	}
	for id, v := range want {
		if got, ok := value(s, id); !ok || got != v {
			t.Errorf("%s = %v (present %v), want %v", id, got, ok, v)
		}
	}
	if _, ok := value(s, "tests.e2e.flaky{step=list/TP1}"); ok {
		t.Error("list/TP1 failed on abc1234 and passed on def5678: a fix, not a flake")
	}

	// The score's flaky component reads the share: 33% is past Bad (5%).
	comps := DefaultComponents()
	for _, r := range Derive(s, nil, comps) {
		if r.Name == "score.component" && r.Labels["component"] == "flaky" {
			if r.Value != 0 {
				t.Errorf("flaky sub-score = %v, want 0 for a 33%% share", r.Value)
			}
			return
		}
	}
	t.Error("the score has no flaky sub-score")
}

// No ux-test run in the window is no rows, so the score says "no data" and
// what fills it, never a 0% share that would read as perfect.
func TestFlakyShareNoRuns(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.Local)
	root, _ := healthRepo(t, now)
	plugRuns(t)
	// A run 15 calendar days back is outside the 14-day window.
	writeUXRun(t, root, "board", "20261031-100000.000-auto", "abc1234", now.AddDate(0, 0, -15), "TP1=fail", "TP1=pass")

	s := flakyRows(t, root, now)
	for _, r := range s.Metrics {
		if strings.HasPrefix(r.Metric, "tests.e2e.flaky") || r.Metric == "tests.e2e.runs" {
			t.Errorf("row %s with no run in the window", r.ID)
		}
	}
	for _, r := range Derive(s, nil, DefaultComponents()) {
		if r.Name == "score.component.value" && r.Labels["component"] == "flaky" {
			t.Errorf("flaky value %v with no data", r.Value)
		}
	}
	for _, c := range DefaultComponents() {
		if c.Name == "flaky" && !strings.Contains(c.Missing, "make ux-test") {
			t.Errorf("flaky Missing = %q, want it to say what fills it", c.Missing)
		}
	}
}

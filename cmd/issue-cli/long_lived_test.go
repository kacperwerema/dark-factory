package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/leaks"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// stubLong makes longReport return r while the test runs.
func stubLong(t *testing.T, r leaks.LongReport) {
	t.Helper()
	orig := longReport
	longReport = func([]*tracker.Project, time.Time) leaks.LongReport { return r }
	t.Cleanup(func() { longReport = orig })
}

func longFixture() leaks.LongReport {
	return leaks.LongReport{
		Sessions: []leaks.SessionFinding{
			{Kind: leaks.SessionUnrotated, Session: "lead-orchard-marketing", Project: "orchard", Age: 8 * 24 * time.Hour, Command: "issue-cli --project orchard lead rotate --lead lead-orchard-marketing"},
			{Kind: leaks.SessionUnrotated, Session: "lead-quince", Project: "quince", Age: 6 * 24 * time.Hour, Command: "issue-cli --project quince lead rotate --lead lead-quince"},
			{Kind: leaks.SessionOrphan, Session: "lead-test-project", Age: 19 * 24 * time.Hour, Command: "tmux kill-session -t lead-test-project"},
		},
		Grown: []leaks.Growth{
			{Name: "issue-viewer", FromMB: 400, ToMB: 1020, Since: time.Date(2026, 11, 20, 7, 0, 0, 0, time.Local)},
			{Name: "lead-orchard", FromMB: 300, ToMB: 900, Since: time.Date(2026, 11, 20, 7, 0, 0, 0, time.Local)},
		},
	}
}

// TestLeadTickSaysLongSessionsOnce: a project's tick says its own lead
// past its rotation and its own grown lead, with their commands, once; the
// orphan lead and the viewer only in the engine's own project.
func TestLeadTickSaysLongSessionsOnce(t *testing.T) {
	stubLong(t, longFixture())
	orchard := &tracker.Project{Slug: "orchard", WorkDir: t.TempDir(), Departments: []tracker.Department{{Name: "Marketing"}}}
	ctx, _, _ := newTestContext(orchard, false)
	ctx.AllProjects = []tracker.Project{*orchard, {Slug: "quince"}}
	tick := func(prev leadTickState) (string, map[string]time.Time) {
		var b strings.Builder
		tk := &leadTicker{w: &b, prev: prev, seen: map[string]time.Time{}, now: time.Date(2026, 11, 21, 8, 0, 0, 0, time.Local)}
		leadTickLong(ctx, tk)
		return b.String(), tk.seen
	}
	out, seen := tick(leadTickState{})
	for _, want := range []string{
		"⚠ session: lead-orchard-marketing has run 8d0h without rotating: issue-cli --project orchard lead rotate --lead lead-orchard-marketing",
		"⚠ memory: lead-orchard grew 300 → 900 MB since 11-20 07:00: rotate it: issue-cli lead rotate --lead lead-orchard",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tick lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"lead-quince", "lead-test-project", "issue-viewer"} {
		if strings.Contains(out, not) {
			t.Errorf("orchard's tick names %s:\n%s", not, out)
		}
	}
	if again, _ := tick(leadTickState{Said: seen}); again != "" {
		t.Errorf("said again on the next tick:\n%s", again)
	}
	// The engine's own checkout hears of the orphan and the viewer.
	os.MkdirAll(filepath.Join(orchard.WorkDir, "cmd", "issue-viewer"), 0o755)
	out, _ = tick(leadTickState{})
	if !strings.Contains(out, "lead-test-project has run 19d0h and no project in projects.yaml owns it: tmux kill-session -t lead-test-project") ||
		!strings.Contains(out, "issue-viewer grew 400 → 1020 MB") {
		t.Errorf("the engine's tick lacks the machine's lines:\n%s", out)
	}
}

// TestHealthPrintsWhatIsPastItsBound: issue-cli health lists every
// finding with its command, also before the first snapshot.
func TestHealthPrintsWhatIsPastItsBound(t *testing.T) {
	stubLong(t, longFixture())
	proj, _ := makeCIFixture(t, "")
	orig := reclaimableSessions
	reclaimableSessions = func(*tracker.Project, []*tracker.Issue, time.Time) []agentrun.Reclaim {
		return []agentrun.Reclaim{{Name: "agent-report-x", Slug: "report-x", Status: "owner review", Why: "an open question: comment", IdleSince: time.Now().Add(-3 * time.Hour)}}
	}
	t.Cleanup(func() { reclaimableSessions = orig })
	ctx, out, _ := newTestContext(proj, false)
	if err := runHealth(ctx, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"== Past its bound (sessions and processes) ==", "lead-quince has run 6d0h without rotating", "tmux kill-session -t lead-test-project", "issue-viewer grew 400 → 1020 MB", "idle worker holds a slot: agent-report-x", "lead tick closes it"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("health lacks %q:\n%s", want, out)
		}
	}
}

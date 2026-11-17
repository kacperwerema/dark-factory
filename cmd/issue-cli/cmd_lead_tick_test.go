package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// tickProject is makeAutostartProject with the panes stubbed: no stalls,
// and the sweep finding what finished says, closing into closed.
func tickProject(t *testing.T) (proj *tracker.Project, finished *[]agentrun.Finished, closed *[]string) {
	t.Helper()
	proj = makeAutostartProject(t)
	var f []agentrun.Finished
	var c []string
	origSweep, origClose, origStalls := sweepFinished, closeWorkerSession, scanStalls
	sweepFinished = func(*tracker.Project, []*tracker.Issue, map[string]bool) []agentrun.Finished { return f }
	closeWorkerSession = func(_ *tracker.Project, name string) error { c = append(c, name); return nil }
	scanStalls = func(*tracker.Project, string, time.Time) []agentrun.PaneStall { return nil }
	t.Cleanup(func() { sweepFinished, closeWorkerSession, scanStalls = origSweep, origClose, origStalls })
	return proj, &f, &c
}

func tick(t *testing.T, proj *tracker.Project, args ...string) string {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runLead(ctx, append([]string{"tick"}, args...)); err != nil {
		t.Fatalf("lead tick %v: %v\n%s", args, err, stdout.String())
	}
	return stdout.String()
}

func clearGap() { os.Remove(filepath.Join(autostart.SharedDir(), "last.json")) }

func writeIssue(t *testing.T, proj *tracker.Project, name, status string) {
	t.Helper()
	body := "---\ntitle: \"" + name + "\"\nstatus: \"" + status + "\"\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(proj.IssueDir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Each thing that needs the lead is said once, and again only after it
// went away and came back; a tick where nothing changed prints nothing.
func TestLeadTickSaysEachThingOnce(t *testing.T) {
	proj, finished, closed := tickProject(t)
	started := stubStart(t)
	root := filepath.Dir(proj.IssueDir)

	// A done issue whose branch never landed, and one that did.
	writeIssue(t, proj, "shipped", "done")
	writeIssue(t, proj, "landed", "done")
	queueShipRepo(t, root, "main", "shipped")
	queueGit(t, root, "branch", "work/landed", "main")
	*finished = []agentrun.Finished{{Name: "agent-shipped", Slug: "shipped", Why: "done"}, {Name: "agent-landed", Slug: "landed", Why: "done"}}

	mustQ(t, proj, "add", "high", "--quote", queueWords)
	out := tick(t, proj)
	for _, want := range []string{
		"start: ✓ Started queue item 1: high (marked [>])",
		"land: shipped is done but work/shipped has 1 commit(s) not on main",
		"queue: nothing open left: ∞ think up more, then issue-cli --project proj queue add <slug>",
	} {
		assertContains(t, out, want)
	}
	assertNotContains(t, out, "landed is done")
	if len(*started) != 1 {
		t.Fatalf("started %v", *started)
	}
	if strings.Join(*closed, ",") != "agent-landed" {
		t.Fatalf("closed %v: the unlanded branch's session must stay", *closed)
	}

	// Nothing changed (the gap holds, which is no news): quiet.
	if out := tick(t, proj); out != "" {
		t.Fatalf("quiet tick printed:\n%s", out)
	}

	// A new hold is said once, whatever its numbers do.
	clearGap()
	mustQ(t, proj, "add", "low", "--quote", queueWords)
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 60, CanStart: true} }
	out = tick(t, proj)
	assertContains(t, out, "hold: memory 60% ≥ 50%")
	assertContains(t, out, "queue: 1 open item(s) left (refill at 5)")
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 63, CanStart: true} }
	if out := tick(t, proj); out != "" {
		t.Fatalf("same hold again:\n%s", out)
	}

	// The owner's comment is said once.
	low := loadLeadIssue(t, proj, "low")
	if err := tracker.AddCommentBy(low.FilePath, 0, "please do low next", "viewer", "owner"); err != nil {
		t.Fatal(err)
	}
	out = tick(t, proj)
	assertContains(t, out, "owner: low: please do low next")
	if out := tick(t, proj); out != "" {
		t.Fatalf("owner comment twice:\n%s", out)
	}

	// Local CI red: said once per red streak; it holds the start too.
	wf := "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\nci:\n  run:\n    - \"true\"\n"
	os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644)
	ciDir := filepath.Join(root, ".ci")
	os.MkdirAll(ciDir, 0o755)
	os.WriteFile(filepath.Join(ciDir, "state.json"), []byte(`{"branch":"main","last_sha":"deadbeefcafe","red_since":"deadbeefcafe","failed":"make validate","state":"red"}`), 0o644)
	out = tick(t, proj)
	assertContains(t, out, "ci: main red since deadbee: make validate")
	if out := tick(t, proj); out != "" {
		t.Fatalf("CI red twice:\n%s", out)
	}

	// The hold goes away and comes back: said again.
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 31, CanStart: true} }
	os.WriteFile(filepath.Join(ciDir, "state.json"), []byte(`{"branch":"main","state":"green","green_sha":"deadbeefcafe"}`), 0o644)
	out = tick(t, proj)
	assertContains(t, out, "start: ✓ Started queue item 2: low")
	clearGap()
	mustQ(t, proj, "add", "base", "--quote", queueWords)
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 61, CanStart: true} }
	assertContains(t, tick(t, proj), "hold: memory 61% ≥ 50%")
}

// A tick starts only what queue next --start starts: with no owner's words
// and auto-start off, nothing.
func TestLeadTickStartsOnlyWhatQueueNextStarts(t *testing.T) {
	proj, _, _ := tickProject(t)
	started := stubStart(t)
	mustQ(t, proj, "add", "low")
	out := tick(t, proj)
	assertContains(t, out, "hold: nothing ready to start (first: low, no owner's words")
	if len(*started) != 0 {
		t.Fatalf("started %v", *started)
	}
}

func TestLeadTickRefusesWorker(t *testing.T) {
	proj, _, _ := tickProject(t)
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "high")
	ctx, _, _ := newTestContext(proj, false)
	if err := runLead(ctx, []string{"tick"}); err == nil || !strings.Contains(err.Error(), "workers don't run the lead's tick") {
		t.Fatalf("worker: %v", err)
	}
}

// --watch ticks until stopped and quiet ticks print nothing; with
// --exit-on-news it returns after the first tick that said something.
func TestLeadTickWatchQuietTicks(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	ticks := 0
	orig := leadTickSleep
	leadTickSleep = func(d time.Duration) bool {
		if d != 2*time.Minute {
			t.Errorf("slept %s, want 2m", d)
		}
		ticks++
		return ticks < 3
	}
	t.Cleanup(func() { leadTickSleep = orig })
	out := tick(t, proj, "--watch", "--every", "2m")
	if ticks != 3 || strings.Count(out, "start:") != 1 || strings.Count(out, "\n") != 2 {
		t.Fatalf("%d ticks, output:\n%s", ticks, out)
	}

	ticks = 0
	clearGap()
	mustQ(t, proj, "add", "low", "--quote", queueWords)
	out = tick(t, proj, "--watch", "--exit-on-news", "--verbose", "--every", "2m")
	if ticks != 0 || !strings.Contains(out, "start: ✓ Started queue item 2: low") {
		t.Fatalf("exit-on-news --verbose: %d sleeps, output:\n%s", ticks, out)
	}

	ctx, _, _ := newTestContext(proj, false)
	if err := runLead(ctx, []string{"tick", "--every", "10s"}); exitOf(err) != 2 {
		t.Fatalf("--every 10s: %v", err)
	}
}

// With --exit-on-news a start is no news: three ticks that each start a
// queue item don't exit, and the red CI on the fourth exits with the three
// starts printed above it, one line each (the CI's own hold of the start
// follows it).
func TestLeadTickExitOnNewsHoldsStarts(t *testing.T) {
	proj, _, _ := tickProject(t)
	started := stubStart(t)
	for _, name := range []string{"one", "two", "three"} {
		writeIssue(t, proj, name, "backlog")
		mustQ(t, proj, "add", name, "--quote", queueWords)
	}
	for i := 0; i < 6; i++ {
		// Enough open items that the refill line stays quiet.
		name := fmt.Sprintf("more-%d", i)
		writeIssue(t, proj, name, "backlog")
		mustQ(t, proj, "add", name, "--quote", queueWords)
	}
	root := filepath.Dir(proj.IssueDir)
	ticks := 0
	orig := leadTickSleep
	leadTickSleep = func(time.Duration) bool {
		ticks++
		switch ticks {
		case 1, 2:
			clearGap() // the next tick starts the next item
		case 3:
			// Local CI goes red on main: news the lead must act on. It
			// holds the start too, so this tick starts nothing.
			wf := "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\nci:\n  run:\n    - \"true\"\n"
			os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644)
			ciDir := filepath.Join(root, ".ci")
			os.MkdirAll(ciDir, 0o755)
			os.WriteFile(filepath.Join(ciDir, "state.json"), []byte(`{"branch":"main","last_sha":"deadbeefcafe","red_since":"deadbeefcafe","failed":"make validate","state":"red"}`), 0o644)
			clearGap()
		default:
			t.Errorf("tick %d: should have exited on the red CI", ticks)
			return false
		}
		return true
	}
	t.Cleanup(func() { leadTickSleep = orig })
	clearGap()
	out := tick(t, proj, "--watch", "--exit-on-news", "--every", "1m")
	if ticks != 3 || len(*started) != 3 {
		t.Fatalf("%d sleeps, started %v, output:\n%s", ticks, *started, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 4 || strings.Count(out, "start:") != 3 {
		t.Fatalf("want 3 starts above the CI line, got:\n%s", out)
	}
	for i, name := range []string{"one", "two", "three"} {
		assertContains(t, lines[i], fmt.Sprintf("start: ✓ Started queue item %d: %s", i+1, name))
	}
	assertContains(t, lines[3], "ci: main red since deadbee: make validate")
}

// watchTick is one --watch tick at now.
func watchTick(t *testing.T, proj *tracker.Project, now time.Time) string {
	t.Helper()
	orig := leadTickSleep
	leadTickSleep = func(time.Duration) bool { return false }
	t.Cleanup(func() { leadTickSleep = orig })
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := runLead(ctx, []string{"tick", "--watch"}); err != nil {
		t.Fatalf("lead tick --watch: %v\n%s", err, stdout.String())
	}
	return stdout.String()
}

// Under --watch a hold that clears by itself (memory, the load) says
// nothing until the start has been held an hour, then once; a by-hand tick
// still says it, and a hold the lead can act on is said at once.
func TestLeadTickWatchQuietOnHoldsThatClear(t *testing.T) {
	proj, _, _ := tickProject(t)
	started := stubStart(t)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	for i := 0; i < 6; i++ {
		// Enough open items that the refill line stays quiet.
		name := fmt.Sprintf("more-%d", i)
		writeIssue(t, proj, name, "backlog")
		mustQ(t, proj, "add", name, "--quote", queueWords)
	}
	t0 := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)

	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 60, CanStart: true} }
	if out := watchTick(t, proj, t0); out != "" {
		t.Fatalf("memory hold under --watch printed:\n%s", out)
	}
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 20, CanStart: true} }
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 15.5, Avg5: 15.5, Cores: 14} }
	if out := watchTick(t, proj, t0.Add(5*time.Minute)); out != "" {
		t.Fatalf("load hold under --watch printed:\n%s", out)
	}
	if out := watchTick(t, proj, t0.Add(59*time.Minute)); out != "" {
		t.Fatalf("held under 1h printed:\n%s", out)
	}

	// Held an hour: said once, with the hold of the moment.
	out := watchTick(t, proj, t0.Add(time.Hour))
	assertContains(t, out, "hold: nothing started for 1h (since 09:00), now: load 15.5/15.5 on 14 cores (max 14)")
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 60, CanStart: true} }
	if out := watchTick(t, proj, t0.Add(70*time.Minute)); out != "" {
		t.Fatalf("1h line twice:\n%s", out)
	}

	// A by-hand tick still says why nothing started.
	assertContains(t, tick(t, proj), "hold: memory 60% ≥ 50%")
	if len(*started) != 0 {
		t.Fatalf("started %v", *started)
	}

	// A start ends the run: the next self-clearing hold is quiet again.
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 20, CanStart: true} }
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 2, Avg5: 2, Cores: 14} }
	assertContains(t, watchTick(t, proj, t0.Add(3*time.Hour)), "start: ✓ Started queue item 1: high")
	clearGap()
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 15.5, Avg5: 15.5, Cores: 14} }
	if out := watchTick(t, proj, t0.Add(5*time.Hour+5*time.Minute)); out != "" {
		t.Fatalf("new run of holds printed:\n%s", out)
	}
}

// Under --watch a hold the lead can act on is still said: nothing ready
// to start, and local CI red.
func TestLeadTickWatchSaysHoldsTheLeadCanActOn(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	mustQ(t, proj, "add", "low")
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	assertContains(t, watchTick(t, proj, now), "hold: nothing ready to start (first: low, no owner's words")

	root := filepath.Dir(proj.IssueDir)
	wf := "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\nci:\n  run:\n    - \"true\"\n"
	os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644)
	os.MkdirAll(filepath.Join(root, ".ci"), 0o755)
	os.WriteFile(filepath.Join(root, ".ci", "state.json"), []byte(`{"branch":"main","last_sha":"deadbeefcafe","red_since":"deadbeefcafe","failed":"make validate","state":"red"}`), 0o644)
	assertContains(t, watchTick(t, proj, now.Add(5*time.Minute)), "hold: local CI red on main at deadbee")
}

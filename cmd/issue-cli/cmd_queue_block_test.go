package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
)

// A queues A, then B blocked on A, and starts A. While A runs, neither
// start path takes B: queue next --start (the lead tick's) and autostart
// next (dispatch --autostart's), and queue list says why. Once A is done,
// the next tick starts B. Before, autostart next picked B from the backlog
// without reading the queue's blockers.
func TestQueueBlockHoldsOnEveryStartPath(t *testing.T) {
	proj, _, _ := tickProject(t)
	started := stubStart(t)
	autostartOnFor(t, proj)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	mustQ(t, proj, "add", "low", "--quote", queueWords, "--blocked-on", "high")

	mustQ(t, proj, "next", "--start")
	if len(*started) != 1 || (*started)[0] != "high" {
		t.Fatalf("started = %v, want high", *started)
	}
	live := map[string]bool{"=agent-high:": true}
	agentrun.HasSession = func(name string) bool { return live[name] }
	clearGap()

	if out, err := runQ(t, proj, "next", "--start"); err == nil || len(*started) != 1 {
		t.Fatalf("queue next --start took the blocked item: %v %v\n%s", err, *started, out)
	}
	assertContains(t, mustQ(t, proj, "list"), "2. [ ] low · blocked on high\n")

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"next"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "start low") {
		t.Fatalf("autostart next took the blocked item:\n%s", stdout.String())
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runDispatch(ctx, []string{"low", "--autostart"}); err == nil || !strings.Contains(err.Error(), "queue item 2: blocked on high") {
		t.Fatalf("dispatch --autostart of the blocked item: %v", err)
	}
	if len(*started) != 1 {
		t.Fatalf("started = %v", *started)
	}

	// A is done: the next tick starts B, and autostart next would too.
	writeIssue(t, proj, "high", "done")
	delete(live, "=agent-high:")
	clearGap()
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"next"}); err != nil || !strings.HasPrefix(stdout.String(), "start low ") {
		t.Fatalf("autostart next after A is done: %v\n%s", err, stdout.String())
	}
	assertContains(t, tick(t, proj), "Started queue item 2: low")
	if len(*started) != 2 || (*started)[1] != "low" {
		t.Fatalf("started = %v, want high then low", *started)
	}
}

// The same for a --merged blocker: autostart next holds B while A's
// branch is ahead of main and starts it once main has A's work.
func TestAutostartNextWaitsForAMergedBlockerOnMain(t *testing.T) {
	proj, root, _, _ := queueMergedProject(t)
	stubStart(t)
	autostartOnFor(t, proj)
	mustQ(t, proj, "add", "high", "--quote", queueWords, "--blocked-on", "base", "--merged")
	assertContains(t, mustQ(t, proj, "list"), "1. [ ] high · waits for base on main")

	next := func() string {
		t.Helper()
		clearGap()
		ctx, stdout, _ := newTestContext(proj, false)
		if err := runAutostart(ctx, []string{"next"}); err != nil {
			t.Fatal(err)
		}
		return stdout.String()
	}
	if out := next(); strings.HasPrefix(out, "start high ") {
		t.Fatalf("autostart next took high before base is on main:\n%s", out)
	}
	queueGit(t, root, "merge", "-q", "--ff-only", "work/base")
	if out := next(); !strings.HasPrefix(out, "start high ") {
		t.Fatalf("autostart next once base is on main:\n%s", out)
	}
}

// Under --exit-on-news the starts of quiet ticks wait for the next news.
// Each says the time of its tick, so starts an hour apart don't read as
// one moment (on 2026-11-22 four starts from 00:35 to 02:07 came out as
// one block, and the lead took a held queue block for a broken one). The
// news tick's own lines carry no time: they are now.
func TestLeadTickExitOnNewsHeldLinesSayTheirTickTime(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	for i := 0; i < 8; i++ { // two starts leave 6: the refill line stays quiet
		name := fmt.Sprintf("item-%d", i)
		writeIssue(t, proj, name, "backlog")
		mustQ(t, proj, "add", name, "--quote", queueWords)
	}
	clearGap()
	root := filepath.Dir(proj.IssueDir)
	now := time.Date(2026, 11, 23, 0, 35, 0, 0, time.Local)
	ticks := 0
	orig := leadTickSleep
	leadTickSleep = func(time.Duration) bool {
		ticks++
		switch ticks {
		case 1:
			now = time.Date(2026, 11, 23, 1, 36, 0, 0, time.Local)
			clearGap()
		case 2:
			now = time.Date(2026, 11, 23, 2, 7, 0, 0, time.Local)
			wf := "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\nci:\n  run:\n    - \"true\"\n"
			os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644)
			os.MkdirAll(filepath.Join(root, ".ci"), 0o755)
			os.WriteFile(filepath.Join(root, ".ci", "state.json"), []byte(`{"branch":"main","last_sha":"deadbeefcafe","red_since":"deadbeefcafe","failed":"make validate","state":"red"}`), 0o644)
		default:
			t.Errorf("tick %d: should have exited on the red CI", ticks)
			return false
		}
		return true
	}
	t.Cleanup(func() { leadTickSleep = orig })
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := runLead(ctx, []string{"tick", "--watch", "--exit-on-news", "--every", "1m"}); err != nil {
		t.Fatalf("lead tick: %v\n%s", err, stdout.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("want two held starts and the news:\n%s", stdout.String())
	}
	if !strings.HasPrefix(lines[0], "[00:35] start: ✓ Started queue item 1: item-0") ||
		!strings.HasPrefix(lines[1], "[01:36] start: ✓ Started queue item 2: item-1") {
		t.Fatalf("held starts without their tick's time:\n%s", stdout.String())
	}
	if !strings.HasPrefix(lines[2], "ci: main red since deadbee") {
		t.Fatalf("the news tick's line is not now:\n%s", stdout.String())
	}
}

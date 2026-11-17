package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// reclaimPanes stands in for tmux and ps: sessions live since 08:00 on
// 2026-11-15, each pane at a prompt except busy ones, nothing paused.
func reclaimPanes(t *testing.T, busy map[string]bool, sessions ...string) {
	t.Helper()
	origList, origPanes, origPS, origCapture, origAttached := agentrun.ListSessions, agentrun.ListPanes, agentrun.ProcessTable, agentrun.CapturePane, agentrun.SessionAttached
	t.Cleanup(func() {
		agentrun.ListSessions, agentrun.ListPanes, agentrun.ProcessTable, agentrun.CapturePane, agentrun.SessionAttached = origList, origPanes, origPS, origCapture, origAttached
	})
	var listed []agentrun.Session
	for _, s := range sessions {
		listed = append(listed, agentrun.Session{Name: s, StartTime: "2026-11-15 08:00:00"})
	}
	agentrun.ListSessions = func() []agentrun.Session { return listed }
	agentrun.ListPanes = func() []agentrun.Pane { return nil }
	agentrun.ProcessTable = func() []agentrun.Proc { return nil }
	agentrun.SessionAttached = func(string) bool { return false }
	agentrun.CapturePane = func(target string) (string, error) {
		for name := range busy {
			if strings.Contains(target, name) {
				return "✻ Working… (esc to interrupt)\n", nil
			}
		}
		return "> \n", nil
	}
}

// A memory hold names the idle session of a done issue, with its status
// and idle since, and how to free it; an in-progress issue's idle session
// and a done issue's busy one are not named.
func TestQueueMemoryHoldListsSessionsItCouldFree(t *testing.T) {
	proj := makeAutostartProject(t)
	writeIssue(t, proj, "shipped", "done")
	writeIssue(t, proj, "busy", "done")
	reclaimPanes(t, map[string]bool{"agent-busy": true}, "agent-shipped", "agent-busy", "agent-base", "lead-proj")
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 60, CanStart: true} }

	out := mustQ(t, proj, "next")
	assertContains(t, out, "hold: memory 60% ≥ 50%")
	assertContains(t, out, "could free: agent-shipped (done, idle since ")
	assertContains(t, out, "close them: issue-cli --project proj lead tick")
	for _, not := range []string{"agent-busy", "agent-base", "lead-proj"} {
		assertNotContains(t, out, "could free: "+not)
	}

	_, err := runQ(t, proj, "next", "--start")
	if err == nil || !strings.Contains(err.Error(), "could free: agent-shipped (done, idle since ") {
		t.Fatalf("next --start: %v", err)
	}

	// Not a memory hold: nothing is named.
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 31, CanStart: true} }
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 15, Cores: 14} }
	assertNotContains(t, mustQ(t, proj, "next"), "could free")
}

// An issue waiting on the owner's approval, or with an open question, is
// what a worker waits on; one still being worked is not.
func TestOwnerWaitReason(t *testing.T) {
	proj := makeAutostartProject(t)
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := issueExists(issues, "base")
	if got := ownerWaitReason(proj, base); got != "" {
		t.Fatalf("in progress, no question: %q", got)
	}
	if err := tracker.AddCommentBy(base.FilePath, 0, "question: which port?", "cli", "agent-base"); err != nil {
		t.Fatal(err)
	}
	issues, _ = tracker.LoadIssues(proj.IssueDir)
	base, _ = issueExists(issues, "base")
	if got := ownerWaitReason(proj, base); got != "an open question: comment" {
		t.Fatalf("open question: %q", got)
	}
}

// Under a memory hold the tick closes the idle sessions it could free,
// says so in one line, and asks the queue again: the start goes ahead
// once memory came down. A busy pane and an in-progress issue's are left.
func TestLeadTickFreesOnAMemoryHold(t *testing.T) {
	proj, _, closed := tickProject(t)
	started := stubStart(t)
	writeIssue(t, proj, "shipped", "done")
	writeIssue(t, proj, "busy", "done")
	reclaimPanes(t, map[string]bool{"agent-busy": true}, "agent-shipped", "agent-busy", "agent-base")
	origPause := leadTickPause
	leadTickPause = func(time.Duration) {}
	t.Cleanup(func() { leadTickPause = origPause })
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	// The memory comes down once agent-shipped is closed.
	autostart.ReadMemory = func() autostart.Memory {
		if strings.Contains(strings.Join(*closed, ","), "agent-shipped") {
			return autostart.Memory{Found: true, Used: 40, CanStart: true}
		}
		return autostart.Memory{Found: true, Used: 60, CanStart: true}
	}

	out := tick(t, proj)
	var freed []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "freed under the memory hold:") {
			freed = append(freed, line)
		}
	}
	if len(freed) != 1 || freed[0] != "freed under the memory hold: closed agent-shipped (done)" {
		t.Fatalf("freed lines %q in:\n%s", freed, out)
	}
	if strings.Join(*closed, ",") != "agent-shipped" {
		t.Fatalf("closed %v", *closed)
	}
	assertContains(t, out, "start: ✓ Started queue item 1: high")
	assertNotContains(t, out, "hold: memory")
	if len(*started) != 1 {
		t.Fatalf("started %v", *started)
	}
}

// Off a memory hold the tick closes nothing beyond the sweep.
func TestLeadTickFreesOnlyOnAMemoryHold(t *testing.T) {
	proj, _, closed := tickProject(t)
	stubStart(t)
	writeIssue(t, proj, "shipped", "done")
	reclaimPanes(t, nil, "agent-shipped")
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 15, Cores: 14} }
	out := tick(t, proj)
	assertNotContains(t, out, "freed")
	if len(*closed) != 0 {
		t.Fatalf("closed %v off a memory hold", *closed)
	}
}

// The lead tick and the queue tell a memory hold by its kind, never by
// the hold's words: a memory hold reworded still frees sessions, and a
// hold of another kind whose words start "memory " frees nothing.
func TestReclaimSurvivesAHoldReworded(t *testing.T) {
	for kind, want := range map[autostart.HoldKind]bool{
		autostart.HoldMemory: true, autostart.HoldMemGuard: true,
		autostart.HoldMemGuardMissing: false, autostart.HoldLoad: false, autostart.HoldWorkers: false,
		autostart.HoldGap: false, autostart.HoldTurn: false, autostart.HoldNone: false,
	} {
		if got := isMemoryHold(kind); got != want {
			t.Errorf("isMemoryHold(%q) = %v, want %v", kind, got, want)
		}
	}

	// queue next --start's refusal carries the verdict's kind, whatever
	// its text says.
	proj := makeAutostartProject(t)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	for _, c := range []struct {
		mem  autostart.Memory
		cpu  autostart.CPU
		kind autostart.HoldKind
	}{
		{autostart.Memory{Found: true, Used: 60, CanStart: true}, autostart.CPU{}, autostart.HoldMemory},
		{autostart.Memory{Found: true, Used: 10, CanStart: false, Note: "used 80%"}, autostart.CPU{}, autostart.HoldMemGuard},
		{autostart.Memory{Found: true, Used: 31, CanStart: true}, autostart.CPU{Found: true, Avg1: 15, Avg5: 15, Cores: 14}, autostart.HoldLoad},
	} {
		autostart.ReadMemory = func() autostart.Memory { return c.mem }
		autostart.ReadLoad = func() autostart.CPU { return c.cpu }
		_, err := runQ(t, proj, "next", "--start")
		var held *exitCodeError
		if !errors.As(err, &held) || !held.Hold || held.HoldKind != c.kind {
			t.Fatalf("%+v: next --start = %#v, want a %q hold", c.mem, err, c.kind)
		}
		held.Msg = "✗ Nothing started: the machine is full." // reworded
		if isMemoryHold(held.HoldKind) != c.kind.Memory() {
			t.Fatalf("%q reworded: isMemoryHold changed", c.kind)
		}
	}
}

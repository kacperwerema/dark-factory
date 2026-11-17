package main

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// After a crash the lead's session is resumed with its background tick
// gone: the tick's watch stays in the store with a dead pid. The lead's
// next issue-cli call says so with the command to start it again; the
// tick's own run and a worker say nothing, and a live tick silences it.
func TestLeadHearsWhenItsTickIsGone(t *testing.T) {
	proj := makeAutostartProject(t)
	lead := tracker.LeadSessionName(proj)
	now := time.Date(2026, 11, 16, 9, 30, 0, 0, time.Local)
	live := map[int]bool{}
	origAlive := pidAlive
	pidAlive = func(pid int) bool { return live[pid] }
	t.Cleanup(func() { pidAlive = origAlive })
	tick := "issue-cli --project proj lead tick --watch --every 5m --exit-on-news"
	s, _ := leadstate.Load(lead)
	s.Register(leadstate.Watch{PID: 4242, Command: tick, Started: now.Add(-time.Hour)})
	if err := s.Save(lead); err != nil {
		t.Fatal(err)
	}
	say := func(command string, args ...string) string {
		ctx, _, stderr := newTestContext(proj, false)
		ctx.Now = func() time.Time { return now }
		printLeadTickMissing(ctx, command, args)
		return stderr.String()
	}

	out := say("status")
	for _, want := range []string{"no background tick is running", "pid 4242", "run_in_background", "\n  " + tick + "\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if out := say("lead", "tick", "--watch"); out != "" {
		t.Errorf("the tick's own run spoke: %s", out)
	}

	live[4242] = true
	if out := say("status"); out != "" {
		t.Errorf("a live tick still warned: %s", out)
	}

	live[4242] = false
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "low")
	if out := say("status"); out != "" {
		t.Errorf("a worker heard the lead's tick: %s", out)
	}
}

// A tick that exited on news gives the lead TickGrace to act before the
// call says it; a lead with no tick on record gets its role's command.
func TestLeadTickMissingGraceAndDefault(t *testing.T) {
	proj := makeAutostartProject(t)
	lead := tracker.LeadSessionName(proj)
	now := time.Date(2026, 11, 16, 9, 30, 0, 0, time.Local)
	say := func(at time.Time) string {
		ctx, _, stderr := newTestContext(proj, false)
		ctx.Now = func() time.Time { return at }
		printLeadTickMissing(ctx, "show", nil)
		return stderr.String()
	}
	if out := say(now); !strings.Contains(out, "none has run") || !strings.Contains(out, "issue-cli --project proj lead tick --watch --exit-on-news") {
		t.Fatalf("no tick on record:\n%s", out)
	}
	s, _ := leadstate.Load(lead)
	s.Register(leadstate.Watch{PID: 7, Command: "issue-cli lead tick --watch", Started: now.Add(-time.Hour)})
	s.Ended(7, now)
	if err := s.Save(lead); err != nil {
		t.Fatal(err)
	}
	if out := say(now.Add(5 * time.Minute)); out != "" {
		t.Fatalf("within the grace: %s", out)
	}
	if out := say(now.Add(leadstate.TickGrace + time.Minute)); !strings.Contains(out, "exited at") || !strings.Contains(out, "issue-cli lead tick --watch\n") {
		t.Fatalf("past the grace:\n%s", out)
	}
}

// A tick --watch that exits records when, through registerLeadWatch.
func TestLeadTickExitRecordsItsEnd(t *testing.T) {
	proj := makeAutostartProject(t)
	lead := tracker.LeadSessionName(proj)
	origArgs := leadWatchArgs
	leadWatchArgs = func() []string { return []string{"--project", "proj", "lead", "tick", "--watch", "--exit-on-news"} }
	t.Cleanup(func() { leadWatchArgs = origArgs })
	ctx, _, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return stateNow }
	issues, _ := proj.LoadIssues()
	registerLeadWatch(ctx, whoAmI(proj, issues))()
	s, _ := leadstate.Load(lead)
	if len(s.Watches) != 0 || s.Tick == nil || !s.Tick.Ended.Equal(stateNow) || s.Tick.Command != "issue-cli --project proj lead tick --watch --exit-on-news" {
		t.Fatalf("store after exit: %+v %+v", s.Watches, s.Tick)
	}
}

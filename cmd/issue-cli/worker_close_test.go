package main

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// workerCloseStubs stands in for tmux: live are the live sessions, paused
// and working the ones mem-guard paused or in a turn, own the pane this
// process runs in. It returns what was closed now and what closes later.
type workerCloseStubs struct {
	live, paused, working map[string]bool
	own                   string
	closed, later         []string
}

func stubWorkerClose(t *testing.T, s *workerCloseStubs) {
	t.Helper()
	origLive, origClose, origPaused, origWorking, origIdle, origNames := liveWorkerSessions, closeWorkerSession, workerPaused, workerWorking, agentrun.CloseWhenIdle, currentTmuxNames
	t.Cleanup(func() {
		liveWorkerSessions, closeWorkerSession, workerPaused, workerWorking, agentrun.CloseWhenIdle, currentTmuxNames = origLive, origClose, origPaused, origWorking, origIdle, origNames
	})
	liveWorkerSessions = func(_ *tracker.Project, slug string) []string {
		var out []string
		for _, name := range agentrun.WorkerSessions(slug) {
			if s.live[name] {
				out = append(out, name)
			}
		}
		return out
	}
	closeWorkerSession = func(_ *tracker.Project, name string) error {
		s.closed = append(s.closed, name)
		return nil
	}
	agentrun.CloseWhenIdle = func(_ *tracker.Project, name string) error {
		s.later = append(s.later, name)
		return nil
	}
	workerPaused = func(_ *tracker.Project, name string) bool { return s.paused[name] }
	workerWorking = func(_ *tracker.Project, name string) bool { return s.working[name] }
	currentTmuxNames = func() (string, string) { return s.own, "" }
}

// A lead's transition into done closes the issue's worker and the session a
// takeover left, and says so in one line; another issue's worker, the lead
// and the post-mortem agent stay.
func TestTransitionToDoneClosesTheWorkerSessions(t *testing.T) {
	proj := makeCloseProject(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	agent, limited := tracker.AgentSessionName("cli/other"), tracker.LimitedSessionName("cli/other")
	s := &workerCloseStubs{live: map[string]bool{
		agent: true, limited: true, tracker.AgentSessionName("cli/main"): true,
		"lead-proj": true, postmortemSession("cli/other"): true,
	}}
	stubWorkerClose(t, s)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/other", "--to", "done"}); err != nil {
		t.Fatalf("transition: %v\n%s", err, stdout.String())
	}
	if got := strings.Join(s.closed, ","); got != agent+","+limited {
		t.Errorf("closed %q, want %s and %s", got, agent, limited)
	}
	if len(s.later) != 0 {
		t.Errorf("closed later: %v", s.later)
	}
	want := "Worker session: closed " + agent + ", " + limited + " (cli/other is done)."
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, stdout.String())
	}
}

// An issue that moves on but stays open keeps its worker.
func TestTransitionToOpenStatusKeepsTheWorker(t *testing.T) {
	proj := makeCloseProject(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	s := &workerCloseStubs{live: map[string]bool{tracker.AgentSessionName("cli/dup"): true}}
	stubWorkerClose(t, s)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/dup", "--to", "in progress"}); err != nil {
		t.Fatalf("transition: %v\n%s", err, stdout.String())
	}
	if len(s.closed)+len(s.later) != 0 || strings.Contains(stdout.String(), "Worker session") {
		t.Errorf("closed %v, later %v:\n%s", s.closed, s.later, stdout.String())
	}
}

// The worker that moves its own issue to done is not cut off mid-turn: its
// session closes when the turn ends. One in a turn elsewhere does the same,
// and one mem-guard paused is left for the sweep.
func TestFinishedWorkerOwnPaneWorkingAndPaused(t *testing.T) {
	proj := makeCloseProject(t)
	agent, limited := tracker.AgentSessionName("cli/other"), tracker.LimitedSessionName("cli/other")

	cases := []struct {
		name          string
		stubs         workerCloseStubs
		closed, later string
		line          string
	}{
		{"own pane", workerCloseStubs{live: map[string]bool{agent: true}, own: agent}, "", agent, agent + " closes when its turn ends"},
		{"in a turn", workerCloseStubs{live: map[string]bool{agent: true}, working: map[string]bool{agent: true}}, "", agent, agent + " closes when its turn ends"},
		{"paused", workerCloseStubs{live: map[string]bool{agent: true, limited: true}, paused: map[string]bool{agent: true}}, limited, "", "left " + agent + " (paused by mem-guard"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.stubs
			stubWorkerClose(t, &s)
			ctx, stdout, _ := newTestContext(proj, false)
			issue := loadLeadIssue(t, proj, "cli/shipped")
			issue.Slug = "cli/other" // a done issue under the stubs' names
			closeFinishedWorker(ctx, stdout, issue)
			if got := strings.Join(s.closed, ","); got != c.closed {
				t.Errorf("closed %q, want %q", got, c.closed)
			}
			if got := strings.Join(s.later, ","); got != c.later {
				t.Errorf("later %q, want %q", got, c.later)
			}
			if !strings.Contains(stdout.String(), c.line) {
				t.Errorf("output lacks %q:\n%s", c.line, stdout.String())
			}
		})
	}
}

// close (parking a duplicate) ends the parked issue's worker too.
func TestCloseParksAndClosesTheWorker(t *testing.T) {
	proj := makeCloseProject(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	agent := tracker.AgentSessionName("cli/other")
	s := &workerCloseStubs{live: map[string]bool{agent: true}}
	stubWorkerClose(t, s)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runClose(ctx, []string{"cli/other", "--fixed-by", "cli/main"}); err != nil {
		t.Fatalf("close: %v", err)
	}
	if strings.Join(s.closed, ",") != agent || !strings.Contains(stdout.String(), "(cli/other is parked)") {
		t.Errorf("closed %v:\n%s", s.closed, stdout.String())
	}
}

// agents sweep closes what the sweep finds (a dry run only lists it), never
// this process's own pane, and refuses a worker.
func TestAgentsSweep(t *testing.T) {
	proj := makeCloseProject(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	s := &workerCloseStubs{own: "lead-proj"}
	stubWorkerClose(t, s)
	orig := sweepFinished
	t.Cleanup(func() { sweepFinished = orig })
	var kept map[string]bool
	sweepFinished = func(_ *tracker.Project, _ []*tracker.Issue, keep map[string]bool) []agentrun.Finished {
		kept = keep
		return []agentrun.Finished{{Name: "agent-cli-shipped", Slug: "cli/shipped", Why: "cli/shipped is done"}}
	}

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runAgents(ctx, []string{"sweep", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	if len(s.closed) != 0 || !strings.Contains(stdout.String(), "Would close agent-cli-shipped (cli/shipped is done)") {
		t.Fatalf("dry run closed %v:\n%s", s.closed, stdout.String())
	}
	if !kept["lead-proj"] {
		t.Errorf("own pane not kept: %v", kept)
	}

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runAgents(ctx, []string{"sweep"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.closed, ",") != "agent-cli-shipped" || !strings.Contains(stdout.String(), "Closed agent-cli-shipped") {
		t.Fatalf("closed %v:\n%s", s.closed, stdout.String())
	}

	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "cli/main")
	ctx, _, _ = newTestContext(proj, false)
	if err := runAgents(ctx, []string{"sweep"}); err == nil || !strings.Contains(err.Error(), "workers don't close sessions") {
		t.Fatalf("worker sweep: %v", err)
	}
}

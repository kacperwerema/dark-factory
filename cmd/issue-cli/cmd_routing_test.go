package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/routing"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

const routingBlock = `routing:
  default: claude
  fallback: [claude, codex]
  rules:
    - {label: docs, agent: codex}
    - {lead: true, agent: codex}
    - {department: marketing, agent: codex}
`

// makeRoutingProject is makeAutostartProject with a routing: block and
// "high" labelled docs; both CLIs count as installed (no real one runs).
func makeRoutingProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := makeAutostartProject(t)
	raw, _ := os.ReadFile(proj.WorkflowFile)
	mustWrite(t, proj.WorkflowFile, string(raw)+routingBlock)
	high := filepath.Join(proj.IssueDir, "high.md")
	body, _ := os.ReadFile(high)
	mustWrite(t, high, strings.Replace(string(body), "priority: \"high\"\n", "priority: \"high\"\nlabels: [docs]\n", 1))
	old := routing.Installed
	routing.Installed = func(a string) bool { return a == "claude" || a == "codex" }
	t.Cleanup(func() { routing.Installed = old })
	return proj
}

// stubPanes makes the given sessions live with the given pane text (a
// stand-in CLI's output) and records any key sent to a pane.
func stubPanes(t *testing.T, panes map[string]string) *[]string {
	t.Helper()
	var typed []string
	origList, origCap, origKeys, origText := agentrun.ListSessions, agentrun.CapturePane, agentrun.SendKeys, agentrun.SendText
	agentrun.ListSessions = func() []agentrun.Session {
		var out []agentrun.Session
		for name := range panes {
			out = append(out, agentrun.Session{Name: name})
		}
		return out
	}
	// Targets are exact ("=agent-x:", F-14): look the session name up.
	agentrun.CapturePane = func(target string) (string, error) {
		return panes[strings.TrimSuffix(strings.TrimPrefix(target, "="), ":")], nil
	}
	agentrun.SendKeys = func(target string, lines []string) error { typed = append(typed, target); return nil }
	agentrun.SendText = func(target, text string) error { typed = append(typed, target); return nil }
	t.Cleanup(func() {
		agentrun.ListSessions, agentrun.CapturePane, agentrun.SendKeys, agentrun.SendText = origList, origCap, origKeys, origText
	})
	return &typed
}

func TestRouting_DispatchAndLeadSpawnFollowTheRules(t *testing.T) {
	proj := makeRoutingProject(t)
	spec := captureStart(t, "dispatched")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDispatch(ctx, []string{"high", "--quote", "start high"}); err != nil {
		t.Fatal(err)
	}
	if spec.AgentType != "codex" || spec.Route == nil || spec.Route.Why != "label docs → codex" {
		t.Fatalf("high (label docs): agent %q route %+v", spec.AgentType, spec.Route)
	}
	if err := runDispatch(ctx, []string{"low", "--quote", "start low"}); err != nil {
		t.Fatal(err)
	}
	if spec.AgentType != "claude" || spec.Route.Why != "default → claude" {
		t.Fatalf("low: agent %q route %+v", spec.AgentType, spec.Route)
	}
	// --agent is the owner's choice and wins over routing.
	if err := runDispatch(ctx, []string{"high", "--agent", "claude", "--quote", "start high on claude"}); err != nil {
		t.Fatal(err)
	}
	if spec.AgentType != "claude" || spec.Route.Why != "--agent claude" {
		t.Fatalf("--agent claude: agent %q route %+v", spec.AgentType, spec.Route)
	}
	// The CEO is a lead: lead: true → codex.
	if err := runLead(ctx, []string{"spawn", "--headless"}); err != nil {
		t.Fatal(err)
	}
	if spec.Session != "lead-proj" || spec.AgentType != "codex" || spec.Route.Why != "leads → codex" {
		t.Fatalf("lead spawn: session %q agent %q route %+v", spec.Session, spec.AgentType, spec.Route)
	}
}

func TestRouting_ALimitInAPaneSendsNewStartsToTheOtherAndAutostartHolds(t *testing.T) {
	proj := makeRoutingProject(t)
	// A stand-in Codex worker shows Codex's limit message (codex-rs/protocol/src/error.rs),
	// its reset an hour from now so the test passes at any time of day.
	now := time.Now()
	pane, wantRest := codexLimitPane(now)
	typed := stubPanes(t, map[string]string{"agent-base": pane})
	spec := captureStart(t, "dispatched")
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := runDispatch(ctx, []string{"high", "--quote", "start high"}); err != nil {
		t.Fatal(err)
	}
	if spec.AgentType != "claude" || !spec.Route.Fallback || !strings.Contains(spec.Route.Why, "codex resting until") {
		t.Fatalf("high while codex rests: agent %q route %+v", spec.AgentType, spec.Route)
	}
	if len(*typed) != 0 {
		t.Fatalf("something was typed into a pane: %v", *typed)
	}

	// issue-cli agents reports the stalled worker, left running.
	stdout.Reset()
	if err := runAgents(ctx, []string{"status"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"codex   " + wantRest, "agent-base (codex): You’ve hit your usage limit", "left running; never killed", "label docs → codex", "fallback claude → codex"} {
		if !strings.Contains(out, want) {
			t.Errorf("agents status missing %q:\n%s", want, out)
		}
	}

	// Claude rests too (the owner saw it): auto-start holds high, whose
	// agents are both resting, and starts low only if its own agent is free.
	if err := runAgents(ctx, []string{"rest", "claude", "--until", now.Add(2 * time.Hour).Format("15:04")}); err != nil {
		t.Fatal(err)
	}
	autostartOnFor(t, proj)
	os.Remove(filepath.Join(proj.IssueDir, "blocked.md")) // so high is first in the next order
	started := stubStart(t)
	stdout.Reset()
	if err := runAutostart(ctx, []string{"next"}); err != nil {
		t.Fatal(err)
	}
	if out := stdout.String(); !strings.Contains(out, "every agent to try is resting") {
		t.Fatalf("autostart next with both resting:\n%s", out)
	}
	err := runDispatch(ctx, []string{"high", "--autostart"})
	if exitOf(err) != 1 || len(*started) != 0 {
		t.Fatalf("dispatch --autostart while both rest: %v, started %v", err, *started)
	}
	if err := runAgents(ctx, []string{"clear", "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := runDispatch(ctx, []string{"high", "--autostart"}); err != nil || len(*started) != 1 {
		t.Fatalf("after clearing claude: %v, started %v", err, *started)
	}
}

func TestRouting_JobRunsAreRoutedByDepartment(t *testing.T) {
	proj := makeJobProject(t)
	// Marketing's own workflow file routes its work.
	wf := filepath.Join(proj.WorkDir, "workflows", "marketing.yaml")
	raw, _ := os.ReadFile(wf)
	mustWrite(t, wf, string(raw)+routingBlock)
	old := routing.Installed
	routing.Installed = func(a string) bool { return a == "claude" || a == "codex" }
	t.Cleanup(func() { routing.Installed = old })
	spec := captureStart(t, "dispatched")
	if out, err := runJobCmd(t, proj, "run", "marketing/weekly-seo-pass", "--quote", jobWords); err != nil {
		t.Fatalf("job run: %v\n%s", err, out)
	}
	if spec.AgentType != "codex" || spec.Route == nil || spec.Route.Why != "department marketing → codex" {
		t.Fatalf("job run: agent %q route %+v", spec.AgentType, spec.Route)
	}
}

func TestRouting_WorkersCantChangeARest(t *testing.T) {
	proj := makeRoutingProject(t)
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "high")
	ctx, _, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.Local) }
	if err := runAgents(ctx, []string{"clear", "codex"}); err == nil || !strings.Contains(err.Error(), "workers can't change") {
		t.Fatalf("worker clear: %v", err)
	}
}

// codexLimitPane is a Codex pane whose limit resets an hour after now (to
// the minute), and the state words issue-cli agents prints for it.
func codexLimitPane(now time.Time) (pane, state string) {
	// An hour later on the wall clock (not now+1h, which on the autumn DST
	// night can be a time nextAt reads as now).
	reset := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()+1, now.Minute(), 0, 0, now.Location())
	pane = "working…\n■ You’ve hit your usage limit. Upgrade to Plus to continue using Codex (https://chatgpt.com/explore/plus), or try again at " + reset.Format("3:04 PM") + ".\n"
	return pane, "resting until " + routing.RestUntilText(routing.Rest{Until: reset, KnownReset: true}, now) + " (limit seen in agent-base)"
}

// The stand-in pane and its expected words agree at any clock: morning,
// evening (a fixed 6:30 PM would already be tomorrow) and across midnight.
func TestRouting_CodexLimitPaneAgreesAtAnyClock(t *testing.T) {
	for _, c := range []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local), "resting until 10:00 (limit seen in agent-base)"},
		{time.Date(2026, 10, 31, 18, 41, 30, 0, time.Local), "resting until 19:41 (limit seen in agent-base)"},
		{time.Date(2026, 10, 31, 23, 30, 0, 0, time.Local), "resting until Sun 00:30 (limit seen in agent-base)"},
		{time.Date(2026, 10, 25, 2, 30, 0, 0, time.Local), "resting until 03:30 (limit seen in agent-base)"},
	} {
		pane, state := codexLimitPane(c.now)
		if state != c.want {
			t.Errorf("at %s: state %q, want %q", c.now.Format("15:04"), state, c.want)
		}
		l, ok := routing.FindLimit("codex", pane, c.now)
		if !ok {
			t.Fatalf("at %s: no limit found in %q", c.now.Format("15:04"), pane)
		}
		st := &routing.State{Rests: map[string]routing.Rest{"codex": {Agent: "codex", Until: l.Until, KnownReset: l.KnownReset, Session: "agent-base", By: "scan"}}}
		if got := routing.StateWords("codex", st, true, c.now); got != c.want {
			t.Errorf("at %s: the pane parses to %q, want %q", c.now.Format("15:04"), got, c.want)
		}
	}
}

package agentrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// standIn writes an executable stand-in for an agent CLI that prints msg
// (the real CLI's limit text) and then waits, as a stalled CLI does. It is
// never the real claude or codex.
func standIn(t *testing.T, dir, name, msg string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\nprintf '%s\\n' 'working on the issue…'\nprintf '%s\\n' \"$LIMIT_MSG\"\nexec sleep 600\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A stand-in Codex in a live pane shows Codex's usage-limit message: the
// scan marks codex resting until the reset it names, the next routed start
// for a docs issue (routed to codex) falls back to claude, and the stalled
// worker is left running.
func TestALimitInALivePaneRestsThatSubscriptionAndTheWorkerKeepsRunning(t *testing.T) {
	isolateTmux(t)
	dir := t.TempDir()
	bin := t.TempDir()
	// Codex's message (codex-rs/protocol/src/error.rs, UsageLimitReachedError).
	msg := "■ You’ve hit your usage limit. Upgrade to Plus to continue using Codex (https://chatgpt.com/explore/plus), or try again at 6:30 PM."
	codex := standIn(t, bin, "codex", msg)

	proj := &tracker.Project{Slug: "proj", WorkDir: dir}
	tmuxRun(t, "new-session", "-d", "-s", "agent-docs", "-c", dir, "-e", "LIMIT_MSG="+msg, codex)
	// The pane shows the message once the stand-in printed it.
	deadline := time.Now().Add(10 * time.Second)
	for {
		pane, _ := CapturePane("agent-docs")
		if strings.Contains(pane, "hit your usage limit") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stand-in never printed its message; pane:\n%s", pane)
		}
		time.Sleep(100 * time.Millisecond)
	}

	now := time.Date(2026, 10, 31, 14, 2, 0, 0, time.Local)
	found := ScanLimits(proj, dir, now)
	if len(found) != 1 || found[0].Session != "agent-docs" || found[0].Limit.Agent != "codex" {
		t.Fatalf("ScanLimits = %+v", found)
	}
	st := Subscriptions()
	r, ok := st.Resting("codex", now)
	want := time.Date(2026, 10, 31, 18, 30, 0, 0, time.Local)
	if !ok || !r.Until.Equal(want) || r.Session != "agent-docs" || r.Project != "proj" {
		t.Fatalf("codex rest = %+v ok=%v, want until %v", r, ok, want)
	}

	old := routing.Installed
	routing.Installed = func(a string) bool { return a == "claude" || a == "codex" }
	t.Cleanup(func() { routing.Installed = old })
	cfg := &tracker.RoutingConfig{Default: "claude", Fallback: []string{"codex", "claude"}, Rules: []tracker.RoutingRule{{Type: "docs", Agent: "codex"}}}
	c := routing.Pick(cfg, routing.Subject{Type: "docs"}, "", st, now)
	if c.Agent != "claude" || !c.Fallback {
		t.Fatalf("next docs start = %+v, want the claude fallback", c)
	}
	if !IsLive(proj, "agent-docs") {
		t.Fatal("the worker that hit its limit was stopped; it must be left running")
	}
}

// RouteStep is the launch report's first line: the agent and why, a warning
// when every agent rests (a manual start still starts).
func TestRouteStepSaysWhichAgentAndWhy(t *testing.T) {
	s := RouteStep(routing.Choice{Agent: "codex", Why: "type docs → codex"})
	if s.Name != "Agent: codex (type docs → codex)" || s.Status != "ok" {
		t.Errorf("step = %+v", s)
	}
	s = RouteStep(routing.Choice{Agent: "claude", Why: "default → claude", Hold: "every agent to try is resting (claude until 15:00)"})
	if s.Status != "warning" || !strings.Contains(s.Detail, "starting claude anyway") {
		t.Errorf("hold step = %+v", s)
	}
}

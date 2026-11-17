package agentrun

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// standIn writes an executable stand-in for an agent CLI that prints msg
// (the real CLI's limit text) and then waits, as a stalled CLI does. It is
// never the real claude or codex.
func standIn(t *testing.T, dir, name, msg string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeStandIn(t, path, "printf '%s\\n' 'working on the issue…'\nprintf '%s\\n' \"$LIMIT_MSG\"\nexec sleep 600\n")
	return path
}

// writeStandIn writes body as an executable sh script at path, already
// run once as a no-op (internal/standin): macOS checks a freshly written
// executable on its first exec, now and then for over 10 s, during which
// the pane stays blank and a timed wait for the stand-in's first line
// misses (local CI red at 723407c). The helper pays that once per distinct
// body, where no deadline counts.
func writeStandIn(t *testing.T, path, body string) {
	t.Helper()
	standin.Write(t, path, body)
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
	waitPane(t, "agent-docs", "hit your usage limit")

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

// A stand-in Claude in a live pane shows the weekly line at 98%: the scan
// rests claude until the line's reset, the next routed start takes codex, the
// worker runs on and isn't reported as stalled at a limit. The same line in a
// pane running codex is a quote and rests nothing.
func TestAStopPointInALivePaneRestsThatSubscriptionAndTheWorkerKeepsRunning(t *testing.T) {
	isolateTmux(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	bin := t.TempDir()
	standIn(t, bin, "claude", "")
	standIn(t, bin, "codex", "")
	// The stand-ins come first on PATH inside the start command itself: a
	// pane's login shell resets the environment, and a test must never run
	// the real CLIs. SessionAgent reads the word after the trap.
	start := func(agent string) string { return "PATH=" + bin + ":$PATH; trap : INT; " + agent + " go" }
	now := time.Date(2026, 11, 2, 14, 2, 0, 0, time.Local)
	msg := "LIMIT_MSG=  You've used 98% of your weekly limit · resets Nov 5 at 8am …"
	proj := &tracker.Project{Slug: "proj", WorkDir: dir}

	tmuxRun(t, "new-session", "-d", "-s", "agent-quote", "-c", dir, "-e", msg, start("codex"))
	waitPane(t, "agent-quote", "weekly limit")
	ScanLimits(proj, dir, now)
	if _, ok := Subscriptions().UsageOf("claude", now); ok {
		t.Fatal("a codex pane's quote was read as claude's usage")
	}
	// The next session starts before this one goes: killing the server's
	// only session makes it exit, and a new-session sent while it shuts
	// down fails with "server exited unexpectedly".
	tmuxRun(t, "new-session", "-d", "-s", "agent-work", "-c", dir, "-e", msg, start("claude"))
	tmuxRun(t, "kill-session", "-t", "agent-quote")
	waitPane(t, "agent-work", "weekly limit")
	if found := ScanLimits(proj, dir, now); len(found) != 0 {
		t.Fatalf("a stop point reported as a limit: %+v", found)
	}
	st := Subscriptions()
	r, ok := st.Resting("claude", now)
	want := time.Date(2026, 11, 5, 8, 0, 0, 0, time.Local)
	if !ok || !r.Until.Equal(want) || r.By != "stop" || r.Session != "agent-work" {
		t.Fatalf("claude rest = %+v ok=%v, want until %v", r, ok, want)
	}

	old := routing.Installed
	routing.Installed = func(a string) bool { return a == "claude" || a == "codex" }
	t.Cleanup(func() { routing.Installed = old })
	cfg := &tracker.RoutingConfig{Default: "claude", Fallback: []string{"claude", "codex"}}
	if c := routing.Pick(cfg, routing.Subject{Type: "feature"}, "", st, now); c.Agent != "codex" || !c.Fallback {
		t.Fatalf("next start = %+v, want the codex fallback", c)
	}
	if !IsLive(proj, "agent-work") {
		t.Fatal("the worker at its stop point was stopped; it must be left running")
	}
}

// waitPane polls target's pane until it shows text. The stand-ins are warm
// (writeStandIn, internal/standin), so the limit is only a margin for a loaded machine.
func waitPane(t *testing.T, target, text string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		pane, _ := CapturePane(target)
		if strings.Contains(pane, text) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never showed %q; pane:\n%s", target, text, pane)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

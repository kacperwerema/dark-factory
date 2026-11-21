package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The factory runs 24/7 (factory/the-factory-never-idles-overnight-…#D-1):
// a project that started nothing for leads.idle_minutes (default 60) while
// it had room to start is news, said again each such stretch it stays so.
// Room: the start was held by the project itself (the queue empty, every
// open item held, auto-start off or paused, a route or CI hold) or by the
// worker cap with idle sessions on the slots; not by the machine (memory,
// mem-guard, the load) or another project's gap or turn, and not by a cap
// of busy workers. 2026-11-20: two finished reports held both slots, the
// queue was empty, and the tick said how long nothing had started only at
// 07:49 with the next real news.

// idleReason is why a held start leaves room unused, and what acts on it.
type idleReason struct {
	why, act string
}

// idleWorkerSessions are proj's live worker sessions (agent-…) whose agent
// sits at its prompt, a shelved report's aside (it holds no slot); a var so
// tests need no tmux.
var idleWorkerSessions = func(proj *tracker.Project, issues []*tracker.Issue) []string {
	shelved := agentrun.ShelvedReports(proj, issues)
	var out []string
	seen := map[string]bool{}
	for _, s := range agentrun.ForProject(proj, projectRoot(proj), agentrun.ListSessions()) {
		name := agentrun.BareName(s.Name)
		if !strings.HasPrefix(name, "agent-") || seen[name] {
			continue
		}
		seen[name] = true
		if _, ok := shelved[name]; ok || agentrun.AgentWorking(proj, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// leadTickIdleReason is the idleReason of a held start, nil when the hold
// leaves no room (the machine's, another project's, busy workers).
func leadTickIdleReason(ctx *Context, issues []*tracker.Issue, held *exitCodeError) *idleReason {
	proj := ctx.Project
	in := func(cmd string) string { return tracker.InjectProject(proj, cmd) }
	switch held.HoldKind {
	case autostart.HoldMemory, autostart.HoldMemGuard, autostart.HoldMemGuardMissing,
		autostart.HoldLoad, autostart.HoldGap, autostart.HoldTurn:
		return nil
	case autostart.HoldWorkers:
		names := idleWorkerSessions(proj, issues)
		if len(names) == 0 {
			return nil // the workers are at work
		}
		return &idleReason{
			why: "the worker slots are held by idle sessions (" + strings.Join(names, ", ") + ")",
			act: "answer or finish their issues, or close the ones that wait on nobody (" + in("issue-cli dispatch <slug>") + " re-attaches)",
		}
	case autostart.HoldOff, autostart.HoldPaused:
		return &idleReason{
			why: leadTickHoldReason(held.Msg),
			act: "start one by hand (" + in("issue-cli queue next --start") + ") or ask the owner to turn auto-start on",
		}
	}
	reason := leadTickHoldReason(held.Msg)
	if reason == "" || held.Msg == "" || strings.Contains(held.Msg, autostart.ErrBusy.Error()) {
		return nil
	}
	if strings.Contains(reason, "nothing open in the queue") || strings.Contains(reason, "the queue is empty") {
		return &idleReason{
			why: "the queue is empty",
			act: "file work and queue it (" + in(`issue-cli queue add <slug> --quote "<owner's words>"`) + ")",
		}
	}
	return &idleReason{
		why: "every open item is held: " + reason,
		act: "clear the hold or queue other work (" + in("issue-cli queue list") + ")",
	}
}

// leadTickIdle says the idle line once per leads.idle_minutes stretch.
func leadTickIdle(ctx *Context, t *leadTicker) {
	if t.idle == nil {
		return
	}
	wait := tracker.LeadsConfig{}.IdleWait()
	if wf := ctx.Project.LoadWorkflow(); wf != nil {
		wait = wf.Leads.IdleWait()
	}
	if wait <= 0 {
		return
	}
	since := t.idleFrom
	if t.lastStart.After(since) {
		since = t.lastStart
	}
	d := t.now.Sub(since)
	if d < wait {
		return
	}
	t.say(fmt.Sprintf("idle:%d", int(d/wait)), fmt.Sprintf("idle: nothing started for %s (since %s) with room to start (the factory runs 24/7): %s: %s",
		roundHours(d), since.Local().Format("15:04"), t.idle.why, t.idle.act))
}

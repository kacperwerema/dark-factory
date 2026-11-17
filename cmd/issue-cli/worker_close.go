package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Closing a finished worker: when an issue reaches a closed status (done,
// parked, a side exit), its worker's sessions (agent-<slug>, and the
// limited-<slug> a takeover left) have nothing more to do, and an idle
// Claude Code session still holds a few hundred MB. transition, done and
// close end them here; issue-cli agents sweep and the viewer's sweep close
// what is left. Never a lead or the post-mortem agent the close may start:
// their names are not worker names.

// closeWorkerSession closes the agent called name now; a var so tests stub
// it (agentrun.CloseSession is KillTmux: kill-window in shared mode).
var closeWorkerSession = func(proj *tracker.Project, name string) error { return agentrun.CloseSession(proj, name) }

// liveWorkerSessions lists slug's worker sessions live in proj; a var so
// tests need no tmux.
var liveWorkerSessions = func(proj *tracker.Project, slug string) []string {
	live := map[string]bool{}
	for _, s := range agentrun.ForProject(proj, projectRoot(proj), agentrun.ListSessions()) {
		live[agentrun.BareName(s.Name)] = true
	}
	var out []string
	for _, name := range agentrun.WorkerSessions(slug) {
		if live[name] {
			out = append(out, name)
		}
	}
	return out
}

// workerPaused reports whether mem-guard paused the agent called name; a
// var so tests need no ps.
var workerPaused = func(proj *tracker.Project, name string) bool {
	return agentrun.TakeSnapshot().Paused(proj, name)
}

// workerWorking reports whether the agent called name is in a turn.
var workerWorking = agentrun.AgentWorking

// closeFinishedWorker closes issue's worker sessions when issue is closed,
// and prints one line saying what it did. The pane this command runs in,
// and one whose agent is in a turn, close when the turn ends (the agent
// reads this command's output and may add a memory first); a session
// mem-guard paused is left for the sweep, as mem-guard means to resume it.
func closeFinishedWorker(ctx *Context, w io.Writer, issue *tracker.Issue) {
	proj := ctx.Project
	if proj == nil || issue == nil || !proj.IssueClosed(issue) {
		return
	}
	names := liveWorkerSessions(proj, issue.Slug)
	if len(names) == 0 {
		return
	}
	var closed, later, left []string
	for _, name := range names {
		switch {
		case workerPaused(proj, name):
			left = append(left, name)
		case inOwnPane(proj, name) || workerWorking(proj, name):
			if err := agentrun.CloseWhenIdle(proj, name); err != nil {
				left = append(left, name)
				continue
			}
			later = append(later, name)
		default:
			if err := closeWorkerSession(proj, name); err != nil {
				left = append(left, name)
				continue
			}
			closed = append(closed, name)
		}
	}
	var parts []string
	if len(closed) > 0 {
		parts = append(parts, "closed "+strings.Join(closed, ", "))
	}
	if len(later) > 0 {
		parts = append(parts, strings.Join(later, ", ")+" closes when its turn ends")
	}
	if len(left) > 0 {
		parts = append(parts, "left "+strings.Join(left, ", ")+" (paused by mem-guard or not closable now; "+
			tracker.InjectProject(proj, "issue-cli agents sweep")+" closes it later)")
	}
	fmt.Fprintf(w, "Worker session: %s (%s is %s).\n", strings.Join(parts, "; "), issue.Slug, issue.Status)
}

// inOwnPane reports whether this process runs in the agent called name: its
// session, or its window in the project's shared session.
func inOwnPane(proj *tracker.Project, name string) bool {
	session, window := currentTmuxNames()
	if shared := agentrun.SharedSession(proj); shared != "" {
		return session == shared && window == name
	}
	return session == name
}

// sweepFinished lists what agents sweep and the lead tick close, the idle
// sessions of issues whose work waits on the owner on the shelf among them
// (agentrun.ShelvedReports); a var so tests need no tmux.
var sweepFinished = func(proj *tracker.Project, issues []*tracker.Issue, keep map[string]bool) []agentrun.Finished {
	return agentrun.FinishedSessions(proj, projectRoot(proj), agentrun.SweepInput{
		Issues: issues, Closed: tracker.ClosedIssueFunc(proj), KeepNames: keep, Snap: agentrun.TakeSnapshot(),
		Shelved: agentrun.ShelvedReports(proj, issues),
	})
}

// agentsSweepJSON is agents sweep --json.
type agentsSweepJSON struct {
	DryRun bool                `json:"dry_run"`
	Closed []agentrun.Finished `json:"closed"`
	Failed []string            `json:"failed,omitempty"`
}

// runAgentsSweep is agents sweep: close the worker sessions left behind.
func runAgentsSweep(ctx *Context, args []string) error {
	fs := newFlagSet("agents sweep", ctx)
	dryRun := fs.Bool("dry-run", false, "list what it would close; close nothing")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	proj := ctx.Project
	if proj == nil {
		return fmt.Errorf("agents sweep needs a project — pass --project <slug> or run from a project root")
	}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return err
	}
	if me := guardWho(ctx, issues, "agents sweep"); me.Role == tracker.RoleWorker {
		return fmt.Errorf("workers don't close sessions (you are %s): your lead or the owner runs agents sweep", valueOr(me.Name, "a worker"))
	}
	// This process's own pane is never closed under it.
	keep := map[string]bool{}
	if session, window := currentTmuxNames(); session != "" {
		keep[session], keep[window] = true, true
	}
	found := sweepFinished(proj, issues, keep)
	out := agentsSweepJSON{DryRun: *dryRun, Closed: []agentrun.Finished{}}
	for _, f := range found {
		if !*dryRun {
			if err := closeWorkerSession(proj, f.Name); err != nil {
				out.Failed = append(out.Failed, f.Name+": "+err.Error())
				continue
			}
		}
		out.Closed = append(out.Closed, f)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}
	verb := "Closed"
	if *dryRun {
		verb = "Would close"
	}
	for _, f := range out.Closed {
		fmt.Fprintf(ctx.Stdout, "%s %s (%s)\n", verb, f.Name, f.Why)
	}
	for _, line := range out.Failed {
		fmt.Fprintf(ctx.Stdout, "✗ Not closed: %s\n", line)
	}
	if len(out.Closed)+len(out.Failed) == 0 {
		fmt.Fprintln(ctx.Stdout, "No worker session to close.")
	}
	return nil
}

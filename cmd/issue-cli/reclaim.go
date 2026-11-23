package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Under a memory hold, the queue names the idle worker sessions that hold
// memory for nothing (their issue is done, or waits on the owner), and the
// lead's tick closes them and asks again (agentrun.Reclaimable).

// isMemoryHold reports whether a hold is the machine's memory (over the
// line, or mem-guard refusing), by its kind: the hold's text is free to
// change (TestReclaimSurvivesAHoldReworded).
func isMemoryHold(kind autostart.HoldKind) bool { return kind.Memory() }

// ownerWaitReason is what an open issue waits on the owner for: a ready
// gate (the Status page's rule) or an open question: comment; "" when the
// worker still has its own work.
func ownerWaitReason(proj *tracker.Project, issue *tracker.Issue) string {
	comments := issue.Comments()
	if gate := tracker.WaitingOnGate(proj.LoadWorkflowForIssue(issue), issue, comments); gate != "" {
		if finishedBeforeItsGate(issue, comments) {
			// 2026-11-23: report 541 did all its work at idea and never
			// moved; "waits on the owner's approval" read as not started.
			return fmt.Sprintf("finished at %s without moving: a report: comment and every criterion ticked; move it on: %s",
				issue.Status, tracker.InjectProject(proj, "issue-cli transition "+issue.Slug+" --to "+shell.Quote(gate)))
		}
		return "waits on the owner's approval for " + gate
	}
	if n := len(comments); n > 0 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(comments[n-1].Text)), "question:") {
		return "an open question: comment"
	}
	return ""
}

// finishedBeforeItsGate reports whether the issue's worker says it is done
// while its gate is still ahead: a worker's report: comment and every
// Acceptance criteria box ticked (at least one).
func finishedBeforeItsGate(issue *tracker.Issue, comments []tracker.Comment) bool {
	reported := false
	for _, c := range comments {
		if _, ok := commentAfter(c, "report:"); ok && !askedByALead(tracker.CommentAuthor(c)) {
			reported = true
		}
	}
	if !reported {
		return false
	}
	criteria := 0
	for _, it := range tracker.ListCheckboxes(issue.BodyRaw) {
		if !strings.EqualFold(strings.TrimSpace(it.Section), "Acceptance criteria") {
			continue
		}
		if !it.Checked {
			return false
		}
		criteria++
	}
	return criteria > 0
}

// reclaimableSessions lists the sessions a memory hold could free; a var so
// tests need no tmux.
var reclaimableSessions = func(proj *tracker.Project, issues []*tracker.Issue, now time.Time) []agentrun.Reclaim {
	keep := map[string]bool{}
	if session, window := currentTmuxNames(); session != "" {
		keep[session], keep[window] = true, true
	}
	return agentrun.Reclaimable(proj, projectRoot(proj), agentrun.ReclaimInput{
		Issues: issues, Closed: tracker.ClosedIssueFunc(proj),
		Waiting:   func(i *tracker.Issue) string { return ownerWaitReason(proj, i) },
		KeepNames: keep, Snap: agentrun.TakeSnapshot, Now: now, Idle: timeline.IdleAfter,
	})
}

// printCouldFree prints the sessions under a memory hold and what frees them.
func printCouldFree(w io.Writer, proj *tracker.Project, free []agentrun.Reclaim) {
	if len(free) == 0 {
		return
	}
	for _, r := range free {
		fmt.Fprintln(w, "  could free: "+r.Line())
	}
	fmt.Fprintln(w, "  close them: "+tracker.InjectProject(proj, "issue-cli lead tick")+" (closes these, then asks the queue again)")
}

// leadTickSettle is how long the tick waits after closing sessions before
// it asks the queue again; a var so tests don't wait.
var leadTickSettle = 3 * time.Second

// leadTickPause waits out leadTickSettle; a var so tests don't sleep.
var leadTickPause = time.Sleep

// leadTickFree closes the sessions a memory hold could free and returns the
// tick's one line about it ("" when there were none).
func leadTickFree(ctx *Context, issues []*tracker.Issue, under string) (string, bool) {
	proj := ctx.Project
	free := reclaimableSessions(proj, issues, ctx.now())
	if len(free) == 0 {
		return "", false
	}
	var closed, failed []string
	for _, r := range free {
		what := r.Status
		if r.Why != "" && r.Why != r.Status {
			what += ", " + r.Why
		}
		if err := closeWorkerSession(proj, r.Name); err != nil {
			failed = append(failed, r.Name+": "+firstLineOf(err.Error()))
			continue
		}
		closed = append(closed, r.Name+" ("+what+")")
	}
	var parts []string
	if len(closed) > 0 {
		parts = append(parts, "closed "+strings.Join(closed, ", "))
	}
	if len(failed) > 0 {
		parts = append(parts, "not closed "+strings.Join(failed, "; "))
	}
	return "freed under " + under + ": " + strings.Join(parts, "; "), len(closed) > 0
}

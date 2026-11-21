package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// Under YOLO the factory runs 24/7 without the owner's approvals
// (cli/under-yolo-transition-human-testing-documentation-dry-run-says-ready-the#D-2):
// an issue ready at a gate YOLO passes moves on even when its worker has
// gone. 2026-11-21: eleven finished reports sat at human-testing for up to
// four days, their sessions closed, each one transition away from done.
// The tick makes that move: once per tick for each such issue, only when
// transition --dry-run says Ready, never into the work status (nobody would
// work it) and never through a gate that stays the owner's in YOLO
// (yolo.Eligible: an experiment, owner_only, design-first, an owner box
// before the build; release publish is not a gate it reaches).

// leadTickYoloMoves moves each open, worked issue in the lead's scope whose
// worker session is gone through the next gate YOLO passes, and prints a
// held line for each move.
func leadTickYoloMoves(ctx *Context, t *leadTicker, issues []*tracker.Issue) {
	proj := ctx.Project
	me := whoAmI(proj, issues)
	if me.Role == tracker.RoleWorker {
		return
	}
	if _, on, _ := yolo.Active(yoloRoot(proj), proj.Slug, t.now); !on {
		return
	}
	for _, issue := range issues {
		if proj.IssueClosed(issue) || strings.TrimSpace(issue.Assignee) == "" || outOfScope(proj, me, issue, "moves") != "" {
			continue
		}
		wf := proj.LoadWorkflowForIssue(issue)
		if wf == nil {
			continue
		}
		from := issue.Status
		to := wf.NextRequiredStatus(from)
		if to == "" || wf.HasRole(to, tracker.StatusRoleWork) || !yolo.Eligible(wf, issue, from, to) || workerLive(proj, issue.Slug) {
			continue
		}
		if !leadTickTransition(ctx, issue.Slug, to, true) {
			continue // it waits on something besides the gate: the lead's other lines say what
		}
		if !leadTickTransition(ctx, issue.Slug, to, false) {
			t.fail("yolo move "+issue.Slug, fmt.Errorf("transition %s → %s failed after a Ready dry run", from, to))
			continue
		}
		t.held(fmt.Sprintf("moved: %s %s → %s (YOLO passed the gate; its worker has gone; the owner reviews it after the fact)", issue.Slug, from, to))
	}
}

// workerLive reports whether slug's worker (agent-<slug>, limited-<slug>)
// has a live session.
func workerLive(proj *tracker.Project, slug string) bool {
	for _, name := range agentrun.WorkerSessions(slug) {
		if agentrun.IsLive(proj, name) {
			return true
		}
	}
	return false
}

// leadTickTransition runs transition <slug> --to <to> [--dry-run] with its
// output captured: a dry run reports whether it says Ready, a real one
// whether it moved. A var so tests can watch it.
var leadTickTransition = func(ctx *Context, slug, to string, dry bool) bool {
	var out, errOut bytes.Buffer
	sub := *ctx
	sub.Stdout, sub.Stderr, sub.JSONOutput = &out, &errOut, false
	args := []string{slug, "--to", to}
	if dry {
		args = append(args, "--dry-run")
	}
	err := runTransition(&sub, args)
	if dry {
		return err == nil && strings.Contains(out.String(), "✓ Ready:")
	}
	if err == nil {
		return true
	}
	// The move can land and a later write fail (a stats file): the status
	// is what counts.
	if issues, lerr := tracker.LoadIssues(ctx.Project.IssueDir); lerr == nil {
		if is := tracker.FindIssue(issues, slug); is != nil {
			return strings.EqualFold(is.Status, to)
		}
	}
	return false
}

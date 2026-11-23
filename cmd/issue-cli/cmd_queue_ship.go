package main

import (
	"fmt"
	"io"
	"strconv"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// queueShipSHA is the commit an issue shipped as: the tip of work/<slug>
// when the project's main branch (ci.branch, else the main checkout's
// branch: gitmain.Branch) contains it. A tip not on it ships nothing yet:
// no SHA and the note queue.NotOnMain, never main's head, which has
// nothing to do with the issue. With no work branch at all (work done on
// main) it is main's head; "" when the main branch isn't known.
func queueShipSHA(root, slug, configured, base string) (sha, note string) {
	tip, where := queueBranchSHA(root, slug, configured, base)
	switch where {
	case branchOnMain:
		return tip, ""
	case branchNotOnMain:
		return "", queue.NotOnMain
	case branchNone:
		main := gitmain.Root(root)
		if branch, err := gitmain.Branch(main, configured); err == nil {
			head, _ := gitmain.Head(main, branch)
			return head, ""
		}
	}
	return "", ""
}

// Where work/<slug>'s tip stands against the project's main branch.
const (
	branchUnknown   = iota // no repo, or the main branch isn't known
	branchNone             // no work/<slug> branch
	branchOnMain           // main contains the tip
	branchNotOnMain        // the tip is ahead of main
	branchEmpty            // the tip is the dispatch base: nothing of its own
)

// queueBranchSHA is work/<slug>'s tip and where it stands (gitmain.WorkAt
// against base, the issue's dispatch base): the commit the issue shipped
// as once the main branch contains it. A branch still at its base shipped
// nothing, wherever main is.
func queueBranchSHA(root, slug, configured, base string) (string, int) {
	main := gitmain.Root(root)
	branch, err := gitmain.Branch(main, configured)
	if err != nil {
		return "", branchUnknown
	}
	tip, err := gitmain.Git(main, "rev-parse", "--verify", "-q", "refs/heads/work/"+slug+"^{commit}")
	if err != nil || tip == "" {
		return "", branchNone
	}
	switch gitmain.WorkAt(tip, base, gitmain.Contains(main, branch)) {
	case gitmain.WorkEmpty:
		return tip, branchEmpty
	case gitmain.WorkOnMain:
		return tip, branchOnMain
	}
	return tip, branchNotOnMain
}

// queueCIBranch is the workflow's ci.branch, "" when unset.
func queueCIBranch(proj *tracker.Project) string {
	if wf := proj.LoadWorkflow(); wf != nil && wf.CI != nil {
		return wf.CI.Branch
	}
	return ""
}

// queueFillSHAs gives each done item with no SHA (marked not on main, or
// done by reconcile) its work branch's tip once the main branch contains
// it, and drops the not-on-main note. An item with no work branch keeps
// no SHA: main's head says nothing about it. Returns the numbers filled.
func queueFillSHAs(proj *tracker.Project, q *queue.Queue) []int {
	var ids []int
	root, configured := "", ""
	var issues []*tracker.Issue
	for i := range q.Items {
		it := &q.Items[i]
		if it.State != queue.Done || it.Slug == "" || it.DoneSHA != "" || (it.Note != "" && it.Note != queue.NotOnMain) {
			continue
		}
		if root == "" {
			root, configured = projectRoot(proj), queueCIBranch(proj)
			issues, _ = tracker.LoadIssues(proj.IssueDir)
		}
		tip, where := queueBranchSHA(root, it.Slug, configured, queueBaseOf(issues, it.Slug))
		if where == branchUnknown {
			return ids
		}
		if where != branchOnMain {
			continue
		}
		it.DoneSHA, it.Note = tip, ""
		ids = append(ids, it.ID)
	}
	return ids
}

// queueMarkShipped marks the issue's queue items done with its SHA when it
// reaches its workflow's last status. Quiet when it isn't queued.
func queueMarkShipped(ctx *Context, w io.Writer, wf *tracker.WorkflowConfig, slug, to string) {
	if !wf.IsLastStatus(to) || !queue.Exists(ctx.Project.IssueDir) {
		return
	}
	q, err := queue.Load(ctx.Project.IssueDir)
	if err != nil || !q.HasActive(slug) {
		return
	}
	configured := ""
	if wf.CI != nil {
		configured = wf.CI.Branch
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	sha, note := queueShipSHA(projectRoot(ctx.Project), slug, configured, queueBaseOf(issues, slug))
	var ids []int
	err = queue.Update(ctx.Project.IssueDir, func(q *queue.Queue) error {
		ids = q.MarkDone(slug, sha, ctx.now())
		for _, id := range ids {
			if note != "" {
				q.Find(strconv.Itoa(id)).Note = note
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "warning: marking the queue item done: %v\n", err)
		return
	}
	for _, id := range ids {
		if note != "" {
			fmt.Fprintf(w, "✓ Queue item %d marked done (%s): work/%s isn't on the main branch, so it has no SHA\n", id, note, slug)
			continue
		}
		fmt.Fprintf(w, "✓ Queue item %d marked done at %s\n", id, valueOr(gitdir.ShortSHA(sha), "(no sha)"))
	}
}

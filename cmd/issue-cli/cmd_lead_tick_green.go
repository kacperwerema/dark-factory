package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/trace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// leadTickGreenEvery is how often a branch still waiting is named again.
const leadTickGreenEvery = time.Hour

// sayEvery is say, except that a key first said every or longer ago is
// said again (and its time starts over): a thing the lead must not let
// wait is named once an hour while it stays true.
func (t *leadTicker) sayEvery(key, line string, every time.Duration) {
	if _, ok := t.seen[key]; ok {
		return
	}
	if at, ok := t.prev.Said[key]; ok && t.now.Sub(at) < every {
		t.seen[key] = at
		return
	}
	t.seen[key] = t.now
	fmt.Fprintln(t.w, line)
	t.said++
	t.news++
}

// leadTickGreen names each issue sitting at its merge status (shipping)
// whose branch is not on main and has waited leads.green_branch_minutes
// (default 30) since the later of its head commit and the issue's move
// into that status: the worker finished or stalled before the merge, and
// nobody else would notice. News, once an hour per branch, with the
// command that lands it; the merge stays the lead's act.
func leadTickGreen(ctx *Context, t *leadTicker, issues []*tracker.Issue) {
	proj := ctx.Project
	root := gitmain.Root(projectRoot(proj))
	if root == "" {
		return
	}
	mainBranch, err := gitmain.Branch(root, queueCIBranch(proj))
	if err != nil {
		return
	}
	refs, err := gitmain.Git(root, "for-each-ref", "--no-merged=refs/heads/"+mainBranch, "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return // leadTickBranches says this one
	}
	unmerged := strings.Fields(refs)
	if len(unmerged) == 0 {
		return
	}
	isUnmerged := map[string]bool{}
	for _, ref := range unmerged {
		isUnmerged[ref] = true
	}
	wait := tracker.LeadsConfig{}.GreenBranchWait()
	if wf := proj.LoadWorkflow(); wf != nil {
		wait = wf.Leads.GreenBranchWait()
	}
	for _, issue := range issues {
		wf := proj.LoadWorkflowForIssue(issue)
		merge := wf.MergeStatus()
		if merge == "" || !strings.EqualFold(strings.TrimSpace(issue.Status), merge) || wf.IsLastStatus(issue.Status) {
			continue
		}
		if !leadTickMayOwn(issue, unmerged, isUnmerged) {
			continue
		}
		b := checks.IssueBranch(root, issue)
		if !b.Found() || !isUnmerged[b.Name] {
			continue
		}
		since, ok := branchHeadTime(root, b.Name)
		if !ok {
			continue
		}
		if entered := statusEntered(proj, issue); entered.After(since) {
			since = entered
		}
		waited := t.now.Sub(since)
		if waited < wait {
			continue
		}
		t.sayEvery("green:"+issue.Slug+":"+b.Name, fmt.Sprintf("merge: %s is %s and %s has waited %s unmerged (since %s): %s",
			issue.Slug, issue.Status, b.Name, roundHours(waited), sinceStamp(since, t.now), greenMergeCommand(root, mainBranch, b.Name)), leadTickGreenEvery)
	}
}

// branchHeadTime is the committer date of branch's head.
func branchHeadTime(root, branch string) (time.Time, bool) {
	out, err := gitmain.Git(root, "log", "-1", "--format=%ct", "refs/heads/"+branch)
	if err != nil {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(sec, 0), true
}

// statusEntered is when issue's trace says it entered its current status;
// zero without a trace.
func statusEntered(proj *tracker.Project, issue *tracker.Issue) time.Time {
	spans, err := trace.Read(trace.Path(proj.IssueDir, issue.Slug))
	if err != nil {
		return time.Time{}
	}
	for i := len(spans) - 1; i >= 0; i-- {
		if strings.EqualFold(spans[i].Status, issue.Status) {
			return spans[i].From
		}
	}
	return time.Time{}
}

// sinceStamp is at as 15:04, with the date when it is not today's.
func sinceStamp(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	if at.Format("2006-01-02") == now.Format("2006-01-02") {
		return at.Format("15:04")
	}
	return at.Format("2006-01-02 15:04")
}

// greenMergeCommand is the lead's command that lands branch on main in
// the main checkout: a fast-forward when main is behind it, else the
// conventional merge commit the commit hook accepts.
func greenMergeCommand(root, mainBranch, branch string) string {
	if _, err := gitmain.Git(root, "merge-base", "--is-ancestor", "refs/heads/"+mainBranch, "refs/heads/"+branch); err == nil {
		return fmt.Sprintf("git -C %s merge --ff-only %s", root, branch)
	}
	return fmt.Sprintf("git -C %s merge --no-ff %s -m \"chore(merge): %s into %s\"", root, branch, branch, mainBranch)
}

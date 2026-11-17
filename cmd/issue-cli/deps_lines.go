package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// sharedFilesShown caps the hot files show lists for an issue's branch.
const sharedFilesShown = 10

// printDepLines prints the issue's depends-on, depended-on-by and decided-by
// links under show's header, plus a one-line guard-metrics summary for its
// system when workbench.metrics lists any.
func printDepLines(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue) {
	closed := tracker.ClosedIssueFunc(ctx.Project)
	deps := tracker.IssueDeps(issue, issues, tracker.BuildDecisionIndex(issues, closed), closed)
	askHint := ""
	for _, kind := range []string{"depends on", "depended on by", "decided by"} {
		var parts []string
		for _, d := range deps {
			if d.Kind != kind {
				continue
			}
			state := d.State
			if kind != "decided by" && d.Slug != "" {
				// The sibling channel between the two (issue-cli ask).
				switch between := tracker.AskBetween(issue, tracker.FindIssue(issues, d.Slug)); {
				case between != "":
					state += " · " + between
				case kind == "depends on" && !d.Closed && askHint == "":
					askHint = d.Slug
				}
			}
			parts = append(parts, fmt.Sprintf("%s (%s)", d.Target, state))
		}
		if len(parts) > 0 {
			fmt.Fprintf(ctx.Stdout, "%s: %s\n", strings.ToUpper(kind[:1])+kind[1:], strings.Join(parts, ", "))
		}
	}
	if line := childrenLine(issue, issues); line != "" {
		fmt.Fprintln(ctx.Stdout, line)
	}
	if askHint != "" {
		fmt.Fprintf(ctx.Stdout, "Building on %s's format? Agree it with its worker: %s\n", askHint, tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli ask %s \"...\"", askHint)))
	}
	if line := guardMetricsLine(ctx.Project, issue); line != "" {
		fmt.Fprintln(ctx.Stdout, line)
	}
}

// guardMetricsLine summarises the metrics the issue's system lists:
// "Guard metrics for Sim: 2 off (stuck, spawn-kills), 1 near. See …".
func guardMetricsLine(proj *tracker.Project, issue *tracker.Issue) string {
	wb := proj.LoadWorkflow().Workbench
	if wb.Metrics == (tracker.WorkbenchMetrics{}) || issue.System == "" {
		return ""
	}
	rows := tracker.MetricsForSystem(tracker.LoadMetrics(projectRoot(proj), wb, nil), wb, issue.System, "")
	if len(rows) == 0 {
		return ""
	}
	var off, near []string
	for _, m := range rows {
		switch m.Verdict() {
		case tracker.VerdictOff:
			off = append(off, m.ID)
		case tracker.VerdictNear:
			near = append(near, m.ID)
		}
	}
	var parts []string
	if len(off) > 0 {
		parts = append(parts, fmt.Sprintf("%d off (%s)", len(off), strings.Join(off, ", ")))
	}
	if len(near) > 0 {
		parts = append(parts, fmt.Sprintf("%d near", len(near)))
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("all %d ok", len(rows)))
	}
	return fmt.Sprintf("Guard metrics for %s: %s. See %s", issue.System, strings.Join(parts, ", "), metricsURL(proj, issue.System))
}

func metricsURL(proj *tracker.Project, system string) string {
	base := viewerBaseURL(proj)
	return fmt.Sprintf("%s/p/%s/metrics?system=%s", base, proj.Slug, system)
}

// printSharedFiles lists the hot files the issue's open branch shares with
// other open branches, so an agent knows where a merge may conflict.
func printSharedFiles(w io.Writer, proj *tracker.Project, issue *tracker.Issue, issues []*tracker.Issue) {
	shared := tracker.IssueSharedFiles(projectRoot(proj), issue, issues)
	if shared == nil {
		return
	}
	fmt.Fprintf(w, "== Shared files (%d, with %d other branch%s) ==\n", len(shared.Files), shared.Others, map[bool]string{true: "", false: "es"}[shared.Others == 1])
	for i, h := range shared.Files {
		if i == sharedFilesShown {
			fmt.Fprintf(w, "  … %d more: see the Branches tab of the Metrics view\n", len(shared.Files)-i)
			break
		}
		var others []string
		for _, c := range h.Changes {
			if c.Branch != shared.Branch && !c.Shared {
				others = append(others, strings.TrimPrefix(c.Branch, "work/"))
			}
		}
		fmt.Fprintf(w, "  %s  also on %s\n", h.Path, strings.Join(others, ", "))
	}
	fmt.Fprintln(w, "Merge main often and coordinate on these files with the branches' agents.")
	fmt.Fprintln(w)
}

// childrenLine lists an epic's children with their status:
// "Children (2): a (idea), b (done) · issue-cli list --parent <slug>".
func childrenLine(issue *tracker.Issue, issues []*tracker.Issue) string {
	kids := tracker.EpicChildren(issues, issue.Slug)
	if len(kids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(kids))
	for _, k := range kids {
		parts = append(parts, fmt.Sprintf("%s (%s)", k.Slug, k.Status))
	}
	return fmt.Sprintf("Children (%d): %s", len(kids), strings.Join(parts, ", "))
}

// printStartOverlaps tells a worker picking up an issue where its branch
// stands: behind main (merge before editing) and which open branches change
// the same files (ask the sibling before the merge).
func printStartOverlaps(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue) {
	lines := tracker.IssueBranchOverlaps(ctx.Project, projectRoot(ctx.Project), issue, issues).Lines(issue.Slug)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(ctx.Stdout, "== Open branches ==")
	for _, line := range lines {
		fmt.Fprintln(ctx.Stdout, tracker.InjectProject(ctx.Project, line))
	}
	fmt.Fprintln(ctx.Stdout)
}

// printStartMainHolds says what main did to the tests the brief names since
// the issue was created, with the go test that checks it. A dispatched
// worker whose briefing already holds every line gets nothing; one more
// commit since the dispatch prints the whole block again.
func printStartMainHolds(ctx *Context, brief *dispatchBrief, issue *tracker.Issue) {
	lines := dispatchMainHolds(projectRoot(ctx.Project), issue).Lines()
	if len(lines) == 0 {
		return
	}
	if brief != nil {
		fresh := false
		for _, l := range lines {
			if !brief.has(l) {
				fresh = true
				break
			}
		}
		if !fresh {
			return
		}
	}
	fmt.Fprintln(ctx.Stdout, "== Already on main? ==")
	for _, line := range lines {
		fmt.Fprintln(ctx.Stdout, line)
	}
	fmt.Fprintln(ctx.Stdout)
}

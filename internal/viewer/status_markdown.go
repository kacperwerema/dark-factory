package viewer

import (
	"fmt"

	"strings"

	"github.com/kacperwerema/dark-factory/internal/milestone"
	"github.com/kacperwerema/dark-factory/internal/outputs"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// statusMarkdown is the text form of the page (status.md), for pasting as a
// stand-up. Slugs, not titles, so the text still works with issue-cli. It
// follows the page: what needs the owner and what is running first, then
// counts and groups; every list stops at a cap with "+K more" and, where one
// exists, the issue-cli command that lists the rest.
func statusMarkdown(m *StatusModel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s status, %s\n", m.Project, m.AsOf.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "%d/%d done", m.All.Done, m.All.Total)
	if ev := m.EpicsByArea; ev.Epics > 0 {
		fmt.Fprintf(&b, " · %s in %s", plural(ev.Epics, "epic", "epics"), plural(ev.Areas, "area", "areas"))
	}
	b.WriteString("\n")
	for _, pb := range m.Privacy.badges() {
		fmt.Fprintf(&b, "%s: %s\n", pb.Label, pb.Sentence)
	}
	if a := m.AutoStart; a != nil {
		state := "on"
		if a.Paused {
			state = "paused"
		}
		load := ""
		if a.Load != "" {
			load = " · " + a.Load
		}
		fmt.Fprintf(&b, "Auto-start: %s below %d%% memory, cap %d (owner: %q)%s · now: %s\n", state, a.Below, a.Cap, a.Words, load, a.Verdict)
	}
	if line := queueStatusLine(m.Queue); line != "" {
		b.WriteString(line + "\n")
	}
	if m.Jobs != "" {
		b.WriteString(m.Jobs + "\n")
	}
	if m.Nightly != "" {
		b.WriteString(m.Nightly + "\n")
	}
	if m.Agents != "" {
		b.WriteString(m.Agents + "\n")
	}
	if m.Push != "" {
		b.WriteString(m.Push + "\n")
	}
	if m.HQReport != "" {
		fmt.Fprintf(&b, "Departments: the HQ report, one line each, at %s (issue-cli status --hq)\n", m.HQReport)
	}

	fmt.Fprintf(&b, "\n## Waiting on you (%d)\n", m.WaitingCount())
	if m.CI.Red() {
		fmt.Fprintf(&b, "- Local CI red on %s since %s: %s (issue-cli ci status)\n", m.CI.Branch, m.CI.Sha, m.CI.Failed)
	}
	if m.UnsentBugReports > 0 {
		fmt.Fprintf(&b, "- %d issue-cli bug report(s) not sent (issue-cli report-bug --send)\n", m.UnsentBugReports)
	}
	for _, sw := range m.Shelf {
		fmt.Fprintf(&b, "- %s to review on the shelf (issue-cli outputs %s --state review)\n", plural(sw.Count, sw.Name+" output", sw.Name+" outputs"), outputs.DeptKey(sw.Name))
	}
	own := 0
	for _, it := range m.Waiting {
		if !statusOwnRows[it.Kind] {
			continue // clarify and partner rows: with the other rows below
		}
		if own == capMDList {
			fmt.Fprintf(&b, "- +%d more on the Status page\n", m.ownWaiting()-own)
			break
		}
		own++
		switch it.Kind {
		case "gate":
			fmt.Fprintf(&b, "- Approve %s: %s → %s\n", it.Issue.Slug, it.Issue.Status, it.Target)
		case "playtest":
			fmt.Fprintf(&b, "- Playtest %s: session %s, no judge run\n", it.Issue.Slug, strings.Join(it.Sessions, ", "))
		case "checks":
			fmt.Fprintf(&b, "- Run checks %s: issue-cli check-script %s (%s), no owner run\n", it.Issue.Slug, it.Issue.Slug, strings.Join(it.Sessions, ", "))
		case "question":
			fmt.Fprintf(&b, "- Answer %s: open question: comment\n", it.Issue.Slug)
		}
	}
	for _, r := range m.NeedsYouOther() {
		if r.Kind != "outputs" {
			fmt.Fprintf(&b, "- %s: %s\n", r.Label, r.Note)
		}
	}
	if m.WaitingCount() == 0 {
		b.WriteString("- Nothing\n")
	}

	b.WriteString("\n## Working on\n")
	if m.LeadName != "" {
		fmt.Fprintf(&b, "CEO: %s (%s)\n", m.LeadName, strings.ReplaceAll(m.LeadState, "-", " "))
	}
	for i, r := range m.Active {
		if i == capMDList {
			fmt.Fprintf(&b, "- +%d more on the Status page\n", len(m.Active)-i)
			break
		}
		agent := "no agent"
		if r.Agent != "" {
			agent = "agent " + r.Agent
			if r.Gate != "" {
				agent += " → " + r.Gate
			}
			if r.CLI != "" {
				agent += ", " + r.CLI
			}
			if r.Limit != "" {
				agent += "; usage limit: " + r.Limit
			}
			if r.Stalled != "" {
				agent += "; " + r.Stalled
			}
		}
		approx := ""
		if r.Age.Approx {
			approx = "≈"
		}
		fmt.Fprintf(&b, "- [%s %s%s] %s (%s)\n", r.Issue.Status, approx, r.Age.Text, r.Issue.Slug, agent)
	}
	if len(m.Active) == 0 {
		b.WriteString("- Nothing between in progress and shipping\n")
	}
	if len(m.InDesign) > 0 {
		var parts []string
		for i, c := range m.InDesign {
			if i == capChips {
				parts = append(parts, fmt.Sprintf("+%d more", len(m.InDesign)-i))
				break
			}
			parts = append(parts, fmt.Sprintf("%s (%s)", c.Issue.Slug, c.State))
		}
		fmt.Fprintf(&b, "- Agents on issues not yet in progress: %s\n", strings.Join(parts, ", "))
	}
	siblingAsksMarkdown(&b, m)

	if ms := m.Milestone; ms != nil {
		fmt.Fprintf(&b, "\n## Current milestone %s: %d/%d done\n", ms.Version, ms.Progress.Done, ms.Progress.Total)
		var parts []string
		for _, sg := range ms.Legend {
			parts = append(parts, fmt.Sprintf("%d %s", sg.Count, sg.Status))
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "- By status: %s\n", strings.Join(parts, ", "))
		}
		if len(ms.OpenAreas) > 0 {
			fmt.Fprintf(&b, "- Open by area: %s\n", areaCountsLine(ms.OpenAreas, capMilestoneAreas))
		}
	} else {
		b.WriteString("\n## Where we are\n")
		note := ""
		if len(m.Versions) == 0 {
			note = " (no versions set)"
		}
		fmt.Fprintf(&b, "- All issues: %d/%d done%s\n", m.All.Done, m.All.Total, note)
	}
	ciMarkdown(&b, m.CI)
	healthMarkdown(&b, m.Health)
	behindMarkdown(&b, m.Behind)
	if m.ReleaseWait != nil {
		releaseWaitMarkdown(&b, m.ReleaseWait, m.AsOf)
	}

	epicsMarkdown(&b, m.EpicsByArea)

	b.WriteString("\n## Next\n")
	if m.Next.Found {
		fmt.Fprintf(&b, "From %s:\n%s\n", m.Next.File, m.Next.Text)
	} else {
		b.WriteString("No ## Next section in the CEO's handoff note.\n")
	}
	if len(m.Backlog) > 0 {
		fmt.Fprintf(&b, "Backlog (%d):\n", len(m.Backlog))
		for i, issue := range m.Backlog {
			if i == capNext {
				fmt.Fprintf(&b, "- +%d more: issue-cli list --status backlog\n", len(m.Backlog)-i)
				break
			}
			prio := issue.Priority
			if prio == "" {
				prio = "no priority"
			}
			fmt.Fprintf(&b, "- %s (%s)\n", issue.Slug, prio)
		}
	}
	if len(m.Parked) == 0 {
		b.WriteString("Parked: none\n")
	} else {
		fmt.Fprintf(&b, "Parked (%d):\n", len(m.Parked))
		for i, p := range m.Parked {
			if i == capNext {
				fmt.Fprintf(&b, "- +%d more: issue-cli list --status parked\n", len(m.Parked)-i)
				break
			}
			fmt.Fprintf(&b, "- %s: %s\n", p.Issue.Slug, p.Reason)
		}
	}

	fmt.Fprintf(&b, "\n## Recently shipped: %d in 7 days\n", len(m.DoneRecent))
	for _, g := range m.Shipped {
		var slugs []string
		for i, d := range g.Rows {
			if i == capShippedItems {
				more := fmt.Sprintf("+%d more", len(g.Rows)-i)
				if sys := g.Rows[0].Issue.System; !g.Merged && sys != "" {
					more += " (issue-cli list --status done --system " + sys + ")"
				}
				slugs = append(slugs, more)
				break
			}
			slugs = append(slugs, d.Issue.Slug)
		}
		fmt.Fprintf(&b, "- %s %d: %s\n", g.Area, len(g.Rows), strings.Join(slugs, ", "))
	}
	if len(m.DoneRecent) == 0 {
		b.WriteString("- Nothing\n")
	}

	if len(m.Versions) > 0 {
		b.WriteString("\n## Versions\n")
		var parts []string
		for i, v := range m.Versions {
			if i == capVersions {
				parts = append(parts, fmt.Sprintf("+%d more", len(m.Versions)-i))
				break
			}
			cur := ""
			if m.IsCurrent(v.Label) {
				cur = " (current)"
			}
			parts = append(parts, fmt.Sprintf("%s %d/%d%s", v.Label, v.Done, v.Total, cur))
		}
		fmt.Fprintf(&b, "- %s\n", strings.Join(parts, " · "))
	}

	b.WriteString("\n## Recently released\n")
	for _, r := range m.Releases {
		date := ""
		if r.Date != "" {
			date = " (" + r.Date + ")"
		}
		fmt.Fprintf(&b, "- %s%s: %s\n", r.Version, date, r.SummaryText())
	}
	if len(m.Releases) == 0 {
		b.WriteString("- None yet\n")
	}
	return b.String()
}

// ciMarkdown is the Local CI line, with what landed and the log tail when
// red.
func ciMarkdown(b *strings.Builder, c *CIRow) {
	if c == nil {
		return
	}
	fmt.Fprintf(b, "- Local CI: %s\n", c.Line)
	if l := c.LastRed; l != nil {
		fmt.Fprintf(b, "  - Last red log: %s (%s)\n", l.Log, l.Sha)
	}
	if !c.Red() {
		return
	}
	if len(c.Blamed) > 0 {
		var slugs []string
		for _, issue := range c.Blamed {
			slugs = append(slugs, issue.Slug)
		}
		fmt.Fprintf(b, "  - Landed since green: %s\n", strings.Join(slugs, ", "))
	}
	if len(c.LogTail) > 0 {
		tail := c.LogTail
		if len(tail) > 20 {
			tail = tail[len(tail)-20:]
		}
		fmt.Fprintf(b, "\n```\n%s\n```\n", strings.Join(tail, "\n"))
	}
}

// epicsMarkdown is the Epics card for the current panel: one line per
// active area with its epics, then the areas with nothing started on one
// line.
func epicsMarkdown(b *strings.Builder, ev EpicsView) {
	p := ev.CurrentPanel()
	if p == nil {
		return
	}
	scope := ""
	if p.Key != epicsAll {
		scope = " (" + p.Key + ")"
	}
	var started, idle []AreaRow
	for _, a := range p.Areas {
		if a.Started() {
			started = append(started, a)
		} else {
			idle = append(idle, a)
		}
	}
	fmt.Fprintf(b, "\n## Epics%s: %d active, %d not started, in %s\n", scope, p.Active, p.NotStarted, plural(len(p.Areas), "area", "areas"))
	for _, a := range started {
		var parts []string
		for i, e := range a.Epics {
			if i == capMDAreaEpics {
				parts = append(parts, fmt.Sprintf("+%d more", len(a.Epics)-i))
				break
			}
			parts = append(parts, fmt.Sprintf("%s %d/%d", e.Issue.Slug, e.Progress.Done, e.Progress.Total))
		}
		fmt.Fprintf(b, "- %s %d/%d, %s: %s\n", a.Area, a.Progress.Done, a.Progress.Total, plural(len(a.Epics), "epic", "epics"), strings.Join(parts, ", "))
	}
	if len(idle) > 0 {
		var parts []string
		for i, a := range idle {
			if i == capMDNotStarted {
				parts = append(parts, fmt.Sprintf("+%d more areas", len(idle)-i))
				break
			}
			parts = append(parts, fmt.Sprintf("%s 0/%d (%d)", a.Area, a.Progress.Total, len(a.Epics)))
		}
		fmt.Fprintf(b, "- Not started: %s\n", strings.Join(parts, ", "))
	}
	if len(ev.Panels) > 1 {
		fmt.Fprintf(b, "- Other versions on the Status page's Version select; all versions: %s\n", plural(ev.Epics, "open epic", "open epics"))
	}
}

// areaCountsLine is "campaign 14, ui 10, +4 areas".
func areaCountsLine(areas []AreaCount, n int) string {
	var parts []string
	for i, a := range areas {
		if i == n {
			parts = append(parts, fmt.Sprintf("+%s", plural(len(areas)-i, "area", "areas")))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d", a.Area, a.Count))
	}
	return strings.Join(parts, ", ")
}

// upcomingVersions keeps the versions after released (and the non-numeric
// ones, such as "future") and sums their progress.
func upcomingVersions(all []Progress, released string) ([]Progress, Progress) {
	rel := milestone.Norm(released)
	var out []Progress
	var sum Progress
	for _, v := range all {
		if n := milestone.Norm(v.Label); n != "" && rel != "" && milestone.Compare(n, rel) <= 0 {
			continue
		}
		out = append(out, v)
		sum.Done += v.Done
		sum.Total += v.Total
	}
	return out, sum
}

// siblingAsksMarkdown is status.md's open asks between sibling workers: who
// asked whom, how long ago, the words clipped. Nothing without any.
func siblingAsksMarkdown(b *strings.Builder, m *StatusModel) {
	if len(m.SiblingAsks) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Open asks between siblings (%d)\n", len(m.SiblingAsks))
	for i, a := range m.SiblingAsks {
		if i == capMDList {
			fmt.Fprintf(b, "- +%d more (issue-cli show <slug> lists an issue's asks)\n", len(m.SiblingAsks)-i)
			break
		}
		age := ""
		if t, ok := tracker.CommentTime(a.Date); ok {
			age = " " + shortDuration(m.AsOf.Sub(t)) + " ago"
		}
		fmt.Fprintf(b, "- %s → %s (#%d%s): %s\n", a.From, a.On, a.ID, age, clip(a.Words, 100))
	}
}

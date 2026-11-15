package viewer

import (
	"fmt"

	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/privacy"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// reportWhere is Where we are: every issue, the upcoming versions (or why
// there are none) and the open epics furthest along.
func reportWhere(m *StatusModel, issues []*tracker.Issue, prefix string) WbWhere {
	w := WbWhere{All: WbBar{Label: "All issues", Done: m.All.Done, Total: m.All.Total}, HallURL: prefix + "/hall"}
	versioned := false
	for _, issue := range issues {
		if strings.TrimSpace(issue.Version) != "" {
			versioned = true
			break
		}
	}
	for i, v := range m.Versions {
		bar := WbBar{Label: "Version " + v.Label, Done: v.Done, Total: v.Total, Current: m.IsCurrent(v.Label)}
		if bar.Current {
			bar.Note = "current milestone"
			if ms := m.Milestone; ms != nil && ms.Cut != "" {
				bar.Note += " · becomes " + ms.Cut
			}
		}
		if i >= reportVersions {
			w.MoreMilestones = append(w.MoreMilestones, bar)
			continue
		}
		w.Milestones = append(w.Milestones, bar)
	}
	w.MoreVers = len(w.MoreMilestones)
	if len(m.Versions) > 1 {
		w.VersionsTotal = fmt.Sprintf("%d/%d done over %s", m.VersionsProgress.Done, m.VersionsProgress.Total, plural(len(m.Versions), "version", "versions"))
	}
	if ms := m.Milestone; ms != nil {
		color := funcMap["statusColor"].(func(string) string)
		for _, sg := range ms.Segments {
			c := color(sg.Status)
			switch {
			case sg.Done:
				c = reportWorkingColor
			case sg.Track:
				c = reportIdleColor
			}
			w.Segments = append(w.Segments, WbSegment{Value: sg.Count, Color: c, Label: sg.Status})
		}
		for _, sg := range ms.Legend {
			c := color(sg.Status)
			switch {
			case sg.Done:
				c = reportWorkingColor
			case sg.Track:
				c = reportIdleColor
			}
			w.Legend = append(w.Legend, WbLegendRow{Color: c, Count: sg.Count, Label: sg.Status})
		}
		if ms.OpenEpics > 0 {
			w.Areas = append(w.Areas, WbChip{Text: plural(ms.OpenEpics, "epic", "epics") + " open", Tone: "agent", Title: "Open epics count as open issues: the milestone is done when they are closed too"})
		}
		for _, a := range ms.OpenAreas {
			w.Areas = append(w.Areas, WbChip{Text: fmt.Sprintf("%s %d", a.Area, a.Count), Tone: "idle"})
		}
	}
	switch {
	case !versioned:
		w.Locked = "Milestones locked."
		w.LockedWhy = "No issue has a version: set one and you get a bar and a row per version."
	case len(w.Milestones) == 0:
		w.Locked = "No upcoming milestone."
		w.LockedWhy = "Every version on the board is released: set a version on new work for the next one."
	}
	epics := append([]EpicProgress(nil), m.Epics...)
	sort.SliceStable(epics, func(i, j int) bool {
		a, b := epics[i].Progress, epics[j].Progress
		return a.Done*b.Total > b.Done*a.Total
	})
	for i, e := range epics {
		if i == reportEpics {
			w.MoreEpics = len(epics) - i
			break
		}
		w.Epics = append(w.Epics, WbBar{Label: e.Issue.Title, Note: e.Note(), URL: prefix + "/issue/" + e.Issue.Slug, Done: e.Progress.Done, Total: e.Progress.Total})
	}
	return w
}

// reportShipped is Shipped in 7 days by system: each system's newest issue.
func reportShipped(m *StatusModel, prefix string) WbShipped {
	s := WbShipped{Count: len(m.DoneRecent), ListURL: prefix + "/?status=done"}
	for i, g := range m.Shipped {
		if i == reportShippedRows {
			s.More = len(m.Shipped) - i
			break
		}
		top := g.Rows[0]
		row := WbShippedRow{Area: g.Area, Slug: top.Issue.Slug, URL: prefix + "/issue/" + top.Issue.Slug, Date: top.At.Format("2 Jan"), More: len(g.Rows) - 1}
		if g.Merged {
			row.Area = plural(g.Areas, "more system", "more systems")
		}
		if top.Approx {
			row.Date = "≈ " + row.Date
		}
		if row.More > 0 {
			row.MoreURL = prefix + "/?status=done"
			if sys := top.Issue.System; !g.Merged && sys != "" {
				row.MoreURL += "&system=" + sys
			}
		}
		s.Rows = append(s.Rows, row)
	}
	return s
}

// reportHealth is Code health from the Health card; empty boxes without a
// snapshot or a review.
func reportHealth(m *StatusModel, p *tracker.Project, prefix string) WbHealth {
	h := WbHealth{
		URL:     prefix + "/health",
		Derived: WbScoreBox{Label: "FROM MEASUREMENTS", Value: "—", Line: "too little data"},
		Opinion: WbScoreBox{Label: "AGENT OPINION", Value: "—", Line: "no review yet"},
		LintCmd: lintCommandFor(p),
	}
	c := m.Health
	if c == nil {
		h.Derived.Line = "no snapshot yet"
		return h
	}
	h.Snapshot = true
	h.Recs = c.Proposed
	if c.HasDerived {
		h.Derived.Has, h.Derived.Value, h.Derived.Line = true, c.Derived, c.DerivedLine
	}
	if c.HasOpinion {
		h.Opinion.Has, h.Opinion.Value, h.Opinion.Line = true, c.Opinion, c.OpinionLine
		if c.OpinionStale {
			h.Opinion.Line += " · stale"
		}
	}
	h.Weakest = c.WeakestPart
	h.Lint, h.LintOK = c.Lint, c.Lint == "no lint warnings"
	h.Departments = c.Departments
	return h
}

// unlockBench adds the benchmark to To unlock: on once a benchmark has a
// task in the project, else the command that lists candidates.
func unlockBench(u WbUnlock, on bool) WbUnlock {
	cmd := "issue-cli bench candidates"
	it := WbUnlockItem{Key: "benchmark", Title: "Factory benchmark", On: on,
		Text: "Done issues solved again in a sandbox measure how well the factory works; the owner picks the golden tasks.", Code: cmd,
		Action: WbAction{Label: "Copy", Copy: cmd, Tone: "paper", TestID: "unlock_benchmark"}}
	if on {
		it.Text = "A benchmark has golden tasks from this project; its runs are on the Benchmark card."
	}
	u.Total++
	if on {
		u.Done++
		u.Items = append(u.Items, it)
		return u
	}
	// Locked items come before the enabled ones.
	i := 0
	for i < len(u.Items) && !u.Items[i].On {
		i++
	}
	u.Items = append(u.Items[:i], append([]WbUnlockItem{it}, u.Items[i:]...)...)
	return u
}

// reportUnlock is To unlock: each thing that gives the Report more to
// show, whether it is on, why it matters and how to turn it on.
func reportUnlock(m *StatusModel, p *tracker.Project, issues []*tracker.Issue, prefix string) WbUnlock {
	var items []WbUnlockItem
	updates := prefix + "/updates"

	work := WbUnlockItem{Key: "tracker", Title: "Tracker backup", Text: "issues/, sessions/, retros/ and .health/snapshots/ live outside git; a private tracker repo backs them up.",
		Action: WbAction{Label: "Set up", URL: updates, Tone: "paper"}}
	if b := m.Privacy.Badge("work"); b != nil {
		work.On = b.Level == privacy.Green || (b.Level == privacy.Grey && !b.NotSetUp)
		work.Risk = b.Level == privacy.Amber || b.Level == privacy.Red
		if !work.On {
			work.Text = b.Sentence + "."
		}
		if b.FixIsCommand() {
			work.Code, work.Action = b.Fix, WbAction{Label: "Copy", Copy: b.Fix, Tone: "paper"}
		}
		if work.On {
			work.Text = b.Sentence + "."
		}
		work.Note = b.Note
	}
	items = append(items, work)

	code := WbUnlockItem{Key: "code", Title: "Code privacy", Text: "Develop in a private repo and publish releases only.", Action: WbAction{Label: "Enable", URL: updates, Tone: "paper"}}
	if b := m.Privacy.Badge("code"); b != nil {
		code.On = b.Level == privacy.Green || (b.Level == privacy.Grey && !b.NotSetUp)
		code.Risk = b.Level == privacy.Red
		if code.On || b.Level != privacy.Grey {
			code.Text = b.Sentence + "."
		}
		if b.FixIsCommand() {
			code.Code, code.Action = b.Fix, WbAction{Label: "Copy", Copy: b.Fix, Tone: "paper"}
		}
		code.Note = b.Note
	}
	items = append(items, code)

	versioned := false
	for _, issue := range issues {
		if strings.TrimSpace(issue.Version) != "" {
			versioned = true
			break
		}
	}
	cmd := "issue-cli set-meta <slug> --key version --value 0.1"
	items = append(items, WbUnlockItem{Key: "milestones", Title: "Milestones", On: versioned,
		Text: "Give issues a version: Where we are gets a bar per version and the current milestone.", Code: cmd,
		Action: WbAction{Label: "Copy", Copy: cmd, Tone: "paper"}})

	cmd = "issue-cli set-meta <child> --key parent --value <epic>"
	items = append(items, WbUnlockItem{Key: "epics", Title: "Epics", On: len(m.Epics) > 0,
		Text: "An issue whose children name it in parent: becomes an epic, with its own bar.", Code: cmd,
		Action: WbAction{Label: "Copy", Copy: cmd, Tone: "paper"}})

	cmd = "workbench:\n  changelog: CHANGELOG.md"
	items = append(items, WbUnlockItem{Key: "releases", Title: "Releases", On: len(m.Releases) > 0,
		Text: "Point workbench.changelog in workflow.yaml at your changelog (or tag releases in git).", Code: "workbench.changelog",
		Action: WbAction{Label: "Copy", Copy: cmd, Tone: "paper"}})

	review := WbUnlockItem{Key: "review", Title: "Health review by an agent", Text: "No agent has reviewed the code yet.",
		Action: WbAction{Label: "Order", URL: prefix + "/health", Tone: "paper"}}
	if c := m.Health; c != nil && c.HasOpinion {
		review.On = !c.OpinionStale
		if line := c.OpinionLine; line != "" {
			review.Text = strings.ToUpper(line[:1]) + line[1:] + "."
		}
		if c.OpinionStale {
			review.Text += " It is stale: order a new one."
		}
	}
	items = append(items, review)

	u := WbUnlock{Total: len(items)}
	// What is still locked first, risks on top.
	sort.SliceStable(items, func(i, j int) bool {
		rank := func(it WbUnlockItem) int {
			switch {
			case it.Risk:
				return 0
			case !it.On:
				return 1
			}
			return 2
		}
		return rank(items[i]) < rank(items[j])
	})
	for i := range items {
		items[i].Action.TestID = "unlock_" + items[i].Key
		if items[i].On {
			u.Done++
		}
	}
	u.Items = items
	return u
}

// reportActiveRows is how many in-flight issues Working on lists before
// the fold.
const reportActiveRows = 6

// reportActive is Working on: the Status model's in-flight issues with
// their age and agent, and the agents on issues not started yet.
func reportActive(m *StatusModel, prefix string) WbActiveList {
	l := WbActiveList{AgentsURL: prefix + "/agents"}
	for i, a := range m.Active {
		r := WbActiveRow{Title: a.Issue.Title, Slug: a.Issue.Slug, URL: prefix + "/issue/" + a.Issue.Slug, Status: a.Issue.Status,
			Age: a.Age.Text, Tone: "idle", Agent: "no agent"}
		if a.Age.Approx {
			r.Age = "≈ " + r.Age
			r.AgeTitle = "No transition recorded: the file's last change"
		} else if !a.Age.Since.IsZero() {
			r.AgeTitle = "Since " + a.Age.Since.Format("2 Jan 15:04")
		}
		if a.Age.Level != "" {
			r.Tone = "red"
		}
		if a.Agent != "" {
			r.Agent = "agent " + a.Agent
			if a.Gate != "" {
				r.Agent += " → " + a.Gate
			}
		}
		if i < reportActiveRows {
			l.Rows = append(l.Rows, r)
		} else {
			l.More = append(l.More, r)
		}
	}
	for _, c := range m.InDesign {
		tone := "agent"
		if c.State == "at gate" || c.State == "question" {
			tone = "you"
		}
		l.Design = append(l.Design, WbActiveChip{Text: c.Issue.Title + " · " + c.State, Tone: tone, URL: prefix + "/issue/" + c.Issue.Slug})
	}
	return l
}

// reportReleasedRows is how many releases Recently released lists.
const reportReleasedRows = 3

// reportReleased is Recently released from the Status model's releases.
func reportReleased(m *StatusModel) WbReleased {
	r := WbReleased{Source: m.ReleaseSource}
	for i, rel := range m.Releases {
		if i == reportReleasedRows {
			break
		}
		r.Rows = append(r.Rows, WbReleaseRow{Version: rel.Version, Date: rel.Date, Summary: rel.SummaryHTML()})
	}
	if len(r.Rows) == 0 {
		if ms := m.Milestone; ms != nil && ms.Cut != "" {
			r.First = "No releases yet: " + ms.Cut + " will be the first."
		}
		r.Hint = !m.ChangelogDeclined
	}
	return r
}

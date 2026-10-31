package main

import (
	"fmt"
	"html"
	"html/template"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/routing"
	"github.com/michal-franc/issue-viewer/internal/testids"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// What the station took over from the classic issue page
// (ui/classic-page-gaps-move-what-still-lives-only-on-classic1-pages-into-the-workshop-board):
// editing the title and the fields, delete, dispatch and the workflow's
// custom actions, the brief, what you're approving since the last gate,
// mockups, relations, an epic's children and the body itself, folded by
// section, with its checkboxes and an editor. Every write goes to an
// endpoint the classic page already posts to.

// WbFieldEdit is what the Fields card edits in place: the frontmatter
// fields, each saved on change through POST <issue> (status through the
// workflow engine, after the transition preview), the type through
// POST <issue>/type.
type WbFieldEdit struct {
	URL, TypeURL, PreviewURL, ApproveURL string
	Status                               string
	Statuses                             []string
	Priority                             string
	Priorities                           []string
	Assignee, Version                    string
	Labels                               []string
	Type                                 string
	Types                                []WorkTypeOption
	TypeWarning                          string
}

// WbFieldRow is one read-only row under the editable fields: a value, a
// link, a list or a breakdown.
type WbFieldRow struct {
	Label, Value, URL, Tone string
	Mono, External          bool
	List                    []string
	Detail                  []string
}

// WbBrief is "What this is": the body's lead paragraph, the owner's own
// words about the issue and the parent epic's progress.
type WbBrief struct {
	Text  string // markdown
	Words *briefWords
	Epic  *CardProgress
}

// WbApproving is what was recorded since the last gate the owner passed.
type WbApproving struct {
	From, Since string
	Counts      []approvingCount
	Rows        []WbTimelineRow
	More        int
	Empty       string
	FullURL     string
}

// WbMockups is the issue's mockups: the images its body links and the
// canvas (the mockups: field).
type WbMockups struct {
	Shots []mockupThumb
	URL   string
}

// WbRelations is the issue's links to other issues.
type WbRelations struct {
	Groups []WbRelGroup
}

// WbRelGroup is one kind of link: depends on, depended on by, decided by.
type WbRelGroup struct {
	Kind string
	Rows []WbRelRow
}

// WbRelRow is one linked issue or decision with its state. Tone is the
// chip: green (closed or active), red (unknown, replaced, stale), idle.
type WbRelRow struct {
	Target, URL, State, Tone string
}

// WbEpicChildren is an epic's children by version.
type WbEpicChildren struct {
	Done, Total int
	Versions    []WbEpicVersion
	Hint        string
}

// WbEpicVersion is one version's children.
type WbEpicVersion struct {
	Version     string
	Done, Total int
	Pct         int
	Children    []WbEpicChild
}

// WbEpicChild is one child issue.
type WbEpicChild struct {
	Title, URL, Meta, Status string
	Closed                   bool
}

// WbBody is the issue body, rendered and folded by "## " section. The
// boxes tick through POST <issue>/checkbox and the editor saves through
// POST <issue>; both carry the body's hash so a stale page never
// overwrites what an agent wrote.
type WbBody struct {
	Parts       []WbBodyPart
	Raw, Hash   string
	URL         string
	CheckboxURL string
	Empty       bool
}

// WbBodyPart is one section: the text before the first "## " (Intro) or a
// "## " section with its rendered HTML.
type WbBodyPart struct {
	ID, Title, Count, Tone string
	HTML                   template.HTML
	Intro                  bool
}

// WbDispatch is the agent card's dispatch: Claude or Codex, the workflow's
// custom actions and the prompt that was sent.
type WbDispatch struct {
	URL, ActionURL, PromptURL string
	Actions                   []tracker.CustomAction
	// Routed is the agent the workflow's routing: picks now ("codex") and
	// Why the rule; empty without routing:, so the page offers only the
	// explicit Claude / Codex buttons.
	Routed, Why string
}

// stationPriorities is the priority select, as on the classic page.
var stationPriorities = []string{"", "low", "medium", "high", "critical"}

// stationFieldEdit is the Fields card's inputs.
func stationFieldEdit(wf *tracker.WorkflowConfig, issue *tracker.Issue, issueURL string) *WbFieldEdit {
	e := &WbFieldEdit{
		URL: issueURL, TypeURL: issueURL + "/type", PreviewURL: issueURL + "/transition", ApproveURL: issueURL + "/approve",
		Status: issue.Status, Statuses: orderedStatusesForIssue(wf, issue.Status),
		Priority: issue.Priority, Priorities: stationPriorities,
		Assignee: issue.Assignee, Version: issue.Version, Labels: issue.Labels,
		Type: firstNonEmpty(issue.Type, wf.ActiveType), Types: workTypeOptions(wf),
		TypeWarning: strings.TrimSpace(wf.UnknownTypeWarning(issue.Slug, issue.Type) + " " + wf.OffPathWarning()),
	}
	return e
}

// stationFieldRows are the read-only rows under the inputs: system,
// created, epic, GitHub, score, shared hot files, the CEO route and the
// issue's other frontmatter fields.
func stationFieldRows(wf *tracker.WorkflowConfig, issue *tracker.Issue, prefix string, lead tracker.LeadRoute, shared *tracker.SharedFiles, now time.Time) []WbFieldRow {
	var rows []WbFieldRow
	if issue.System != "" {
		rows = append(rows, WbFieldRow{Label: "System", Value: issue.System})
	}
	if issue.Created != "" {
		created := issue.Created
		if t, err := time.Parse("2006-01-02", issue.Created); err == nil {
			created = t.Format("2 Jan 2006")
		}
		rows = append(rows, WbFieldRow{Label: "Created", Value: created})
	}
	if p := tracker.ParentSlug(issue); p != "" {
		rows = append(rows, WbFieldRow{Label: "Epic", Value: p, URL: prefix + "/issue/" + p, Mono: true})
	}
	if issue.GithubURL != "" {
		rows = append(rows, WbFieldRow{Label: "GitHub", Value: fmt.Sprintf("%s#%d", issue.Repo, issue.Number), URL: issue.GithubURL, External: true})
	}
	if wf.Scoring.Enabled {
		if sc := tracker.ComputeScore(issue, &wf.Scoring, now); sc != nil {
			row := WbFieldRow{Label: "Score", Value: "⚡ " + strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", sc.Total), "0"), ".")}
			for _, c := range sc.Components {
				pts := fmt.Sprintf("%g", c.Points)
				if c.Points >= 0 {
					pts = "+" + pts
				}
				line := c.Name + " " + pts
				if c.Detail != "" {
					line += " (" + c.Detail + ")"
				}
				row.Detail = append(row.Detail, line)
			}
			rows = append(rows, row)
		}
	}
	if shared != nil {
		rows = append(rows, WbFieldRow{Label: "Shared files", URL: prefix + "/metrics?tab=branches",
			Value: plural(len(shared.Files), "hot file", "hot files") + " with " + plural(shared.Others, "other branch", "other branches")})
	}
	if lead.Lead != "" {
		v := lead.Lead + " · not running"
		if lead.Live {
			v = lead.Lead + " · live"
		}
		row := WbFieldRow{Label: "CEO", Value: v, URL: prefix + "/agents?agent=" + lead.Lead, Mono: true}
		if !lead.Live {
			row.Tone = "red"
		}
		rows = append(rows, row)
	}
	for _, f := range issue.ExtraFields {
		switch f.Key {
		case "type", "depends_on", "slug", "parent", "umbrella":
			continue
		}
		row := WbFieldRow{Label: f.Label, Value: f.Value}
		if f.IsList {
			row.Value, row.List = "", f.Values
		} else if f.IsURL {
			row.URL, row.External = f.Value, true
		}
		rows = append(rows, row)
	}
	return rows
}

// stationBrief is "What this is". Nil when there is nothing to say: no lead
// paragraph, no words, no epic.
func stationBrief(tab IssueTab) *WbBrief {
	b := &WbBrief{Text: tab.Brief, Words: tab.Words}
	if tab.Epic != nil && !tab.Epic.Own {
		b.Epic = tab.Epic
	}
	if b.Text == "" && b.Words == nil && b.Epic == nil {
		return nil
	}
	return b
}

// stationApproving is what was recorded since the last gate, newest first.
func stationApproving(a approving, fullURL string, now time.Time) WbApproving {
	out := WbApproving{From: a.From, Counts: a.Counts, More: a.More, FullURL: fullURL}
	if !a.Since.IsZero() {
		out.Since = a.Since.Local().Format("Mon 2 Jan, 15:04")
	}
	for i := len(a.Events) - 1; i >= 0; i-- {
		e := a.Events[i]
		out.Rows = append(out.Rows, WbTimelineRow{At: clock(e.At, now), Text: e.Text, Kind: e.Chip, Tone: e.Tone, Quote: e.Quote})
	}
	if len(out.Rows) == 0 {
		out.Empty = "No recorded events since the issue was made."
		if !a.Since.IsZero() {
			out.Empty = "No recorded events since " + a.Since.Local().Format("2 Jan") + "."
		}
		if !a.Known {
			out.Empty += " No CLI log was found for this issue."
		}
	}
	return out
}

// stationMockups is the issue's mockups; nil when it has none.
func stationMockups(tab IssueTab) *WbMockups {
	if len(tab.Mockups) == 0 && tab.MockupsURL == "" {
		return nil
	}
	return &WbMockups{Shots: tab.Mockups, URL: tab.MockupsURL}
}

// stationRelations groups the issue's links by kind; nil when it has none.
func stationRelations(deps []tracker.IssueDep, prefix string) *WbRelations {
	if len(deps) == 0 {
		return nil
	}
	out := &WbRelations{}
	for _, kind := range []string{"depends on", "depended on by", "decided by"} {
		g := WbRelGroup{Kind: kind}
		for _, d := range deps {
			if d.Kind != kind {
				continue
			}
			row := WbRelRow{Target: d.Target, State: d.State, Tone: "idle"}
			if d.Slug != "" {
				row.URL = prefix + "/issue/" + d.Slug
				if strings.Contains(d.Target, "#") {
					row.URL += "#decisions"
				}
			}
			switch {
			case d.State == "unknown issue", strings.HasPrefix(d.State, "replaced"), d.State == tracker.DecisionStale:
				row.Tone = "red"
			case d.Closed, d.State == tracker.DecisionActive:
				row.Tone = "green"
			}
			g.Rows = append(g.Rows, row)
		}
		if len(g.Rows) > 0 {
			out.Groups = append(out.Groups, g)
		}
	}
	return out
}

// stationEpic is an epic's children by version; nil for an issue that is
// not an epic.
func stationEpic(tab *EpicTab, slug, prefix string, wf *tracker.WorkflowConfig) *WbEpicChildren {
	if tab == nil {
		return nil
	}
	cs := newCanvasStatuses(wf)
	out := &WbEpicChildren{Done: tab.Done, Total: tab.Total}
	if len(tab.Versions) == 0 {
		out.Hint = "No children yet: give issues parent: " + slug + "."
	}
	for _, v := range tab.Versions {
		ev := WbEpicVersion{Version: v.Version, Done: v.Done, Total: v.Total, Pct: v.Pct()}
		for _, c := range v.Children {
			meta := c.Slug
			if c.Type != "" {
				meta += " · " + c.Type
			}
			if c.Assignee != "" {
				meta += " · " + c.Assignee
			}
			ev.Children = append(ev.Children, WbEpicChild{Title: c.Title, URL: prefix + "/issue/" + c.Slug, Meta: meta, Status: c.Status, Closed: cs.closed(c.Status)})
		}
		out.Versions = append(out.Versions, ev)
	}
	return out
}

// stationDispatch is the agent card's dispatch buttons.
func stationDispatch(p *tracker.Project, wf *tracker.WorkflowConfig, issue *tracker.Issue, issueURL string, now time.Time) WbDispatch {
	d := WbDispatch{URL: issueURL + "/dispatch", ActionURL: issueURL + "/action/", PromptURL: issueURL + "/dispatch-prompt", Actions: wf.IssueActionList()}
	if wf.Routing != nil {
		c := routing.Pick(wf.Routing, routing.IssueSubject(p, issue), "", agentrun.Subscriptions(), now)
		d.Routed, d.Why = c.Agent, c.Why
		if c.Hold != "" {
			d.Why = c.Hold
		}
	}
	return d
}

// h2Re finds a rendered "## " heading; the renderer writes each on its own
// line.
var h2Re = regexp.MustCompile(`(?m)^<h2[^>]*>(.*?)</h2>`)

var tagRe = regexp.MustCompile(`<[^>]+>`)

// stationBodyParts renders the body and cuts it at each "## " heading. The
// counts come from the fold (the same as the body index). The data table
// (<!-- data -->) stays on the classic page: its rows need the classic
// page's controls, so the station shows a line that links there.
func stationBodyParts(issue *tracker.Issue, prefix, root string, slugMap map[string]string, fold []foldSection, classic string) []WbBodyPart {
	body := issue.BodyHTML
	store, _ := tracker.LoadData(issue.FilePath)
	note := `<p class="wb-note">The data table` + func() string {
		if n := len(store.Entries); n > 0 {
			return fmt.Sprintf(" (%d rows)", n)
		}
		return ""
	}() + ` is on the <a href="` + template.HTMLEscapeString(classic) + `">classic page</a>.</p>`
	if m := tracker.ParseDataMarker(body); m.Found {
		body = body[:m.Start] + note + body[m.Start+len(m.Raw):]
	} else if len(store.Entries) > 0 {
		body += note
	}
	body = rewriteRelativeImages(body, prefix, filepath.Dir(issue.FilePath), root)
	body = linkIssueRefs(body, prefix, slugMap)

	byKey := map[string]foldSection{}
	for _, f := range fold {
		byKey[f.Key] = f
	}
	var parts []WbBodyPart
	locs := h2Re.FindAllStringSubmatchIndex(body, -1)
	start := 0
	if len(locs) > 0 {
		start = locs[0][0]
	}
	if intro := strings.TrimSpace(body[:start]); intro != "" && len(locs) > 0 || len(locs) == 0 && strings.TrimSpace(body) != "" {
		if len(locs) == 0 {
			intro = strings.TrimSpace(body)
		}
		parts = append(parts, WbBodyPart{ID: "intro", Title: "Intro", Intro: true, HTML: template.HTML(intro), Tone: "idle"})
	}
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		title := strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(body[loc[2]:loc[3]], "")))
		key := headingKey(title)
		f, ok := byKey[key]
		if !ok {
			f = foldSection{ID: testids.Segment(key)}
		}
		p := WbBodyPart{ID: f.ID, Title: title, Tone: "idle", HTML: template.HTML(body[loc[1]:end])}
		if f.Total > 0 {
			p.Count = fmt.Sprintf("%d/%d", f.Done, f.Total)
			switch {
			case f.Done == f.Total:
				p.Tone = "green"
			case ownerSection(title):
				p.Tone = "you"
			}
		}
		parts = append(parts, p)
	}
	return parts
}

// stationMissing lists the sources the page could not read, one line each.
func stationMissing(tl Timeline, ev *EvidenceTab) []string {
	var out []string
	for _, m := range tl.Missing {
		out = append(out, m.Name+": "+m.Detail)
	}
	if ev != nil {
		out = append(out, ev.Missing...)
	}
	return out
}

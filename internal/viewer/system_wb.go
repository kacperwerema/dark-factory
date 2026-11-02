package viewer

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The Workshop page (L2, /p/<project>/systems/<System>[/<Family>]): one
// system as a workshop in the hall. The main column has its crates, issues,
// families and decisions; the side its code (paths: or the draft from its
// issues), design doc, metrics, knobs and balance notes. The model wraps the
// classic page's (buildSystemPage, unchanged), which stays at ?classic=1.
// No owner mockup: drafted from the design system and L2-systems (D-6 on
// the Workshop Board epic).

// WbTrail is the page's own crumb under the hall header: Systems › UI ›
// Family. The last one has no URL.
type WbTrail []WbLink

// WbStatusBar is a status bar: one segment per workflow status, with the
// legend under it. Side is "7 obsolete not counted".
type WbStatusBar struct {
	Segs  []SystemSeg
	Total int
	Side  string
}

// Pct is a segment's share of the bar, 0–100.
func (b WbStatusBar) Pct(n int) float64 {
	if b.Total == 0 {
		return 0
	}
	return float64(n) * 100 / float64(b.Total)
}

// WbFloor is the workshop floor: every issue as a crate, the legend and the
// status bar.
type WbFloor struct {
	Crates []WbCrateDot
	Legend []WbCrateKey
	Bar    WbStatusBar
	Aria   string
}

// WbFamilies is the families panel, or the epics with issues here when the
// system has no families. Note is "3 open issues are in no family."
type WbFamilies struct {
	Title, Source string
	Families      []FamilyCard
	Epics         []EpicCard
	System        string
	Note          string
}

// WbSysDecisions is the recent decisions of the system's issues.
type WbSysDecisions struct {
	Rows  []WbDecisionRow
	Total int
	URL   string
	Hint  string
}

// SystemWbData is the whole Workshop page.
type SystemWbData struct {
	PageTitle  string
	Header     WbHeader
	Trail      WbTrail
	Name       string
	Kind       string // "system" or "family"
	Summary    string
	Hint       string // the not-configured hint
	Figures    []WbFigure
	Links      []WbLink
	Umbrella   *UmbrellaView
	ClassicURL string

	Floor    WbFloor
	Issues   WbShelf
	Hidden   string
	Families *WbFamilies
	Decision WbSysDecisions

	Code        WbCodeMap
	Doc         *DocPanel
	DocNote     string
	Metrics     *MetricsPanel
	Knobs       *KnobsPanel
	Balance     []BalanceItem
	BalanceHref string
}

func (s *Server) handleSystemWb(w http.ResponseWriter, proj *tracker.Project, prefix string, issues []*tracker.Issue, g *systemGroup, famKey string, fam *tracker.WorkbenchFamily) {
	data := s.buildSystemWb(proj, prefix, issues, g, famKey, fam, time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "system_wb.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildSystemWb(p *tracker.Project, prefix string, issues []*tracker.Issue, g *systemGroup, famKey string, fam *tracker.WorkbenchFamily, now time.Time) SystemWbData {
	page := s.buildSystemPage(p, issues, prefix, g, famKey, fam)
	wf := p.LoadWorkflow()
	st := newStatusTones(wf)
	groups, _ := groupSystems(issues, wf.Workbench)

	d := SystemWbData{
		Header:     systemsHeader(p, prefix, s.buildCompany(p, prefix, now)),
		Name:       page.Title,
		Kind:       "system",
		Summary:    page.Summary,
		Umbrella:   page.Umbrella,
		ClassicURL: page.SystemHref + "?classic=1",
		Doc:        page.Doc,
		Balance:    page.Balance,
	}
	d.PageTitle = p.Name + " · " + page.Title
	d.Trail = WbTrail{{Label: "Systems", URL: prefix + "/systems"}, {Label: g.Name}}
	if page.Family != "" {
		d.Kind = "family"
		d.Trail[1].URL = page.SystemHref
		d.Trail = append(d.Trail, WbLink{Label: page.Family})
		d.ClassicURL = familyHref(prefix, g.Name, page.Family) + "?classic=1"
	}
	if !page.Configured {
		d.Hint = fmt.Sprintf("No summary, design doc or families for %s yet. Add them under workbench.systems.%s in workflow.yaml (issue-cli process schema lists the keys); everything here is built from the issues alone.", g.Name, g.Name)
	}

	members := g.Members
	if fam != nil {
		members = childrenOf(g.Members, fam.Umbrella)
	}
	waits, workerOf := s.systemsWho(p, prefix, st, groups)
	cs := systemCrates(st, prefix, members, waits, workerOf)

	d.Figures = []WbFigure{
		{Value: page.Open, Label: "open"},
		{Value: page.Done, Label: "done"},
		{Value: cs.Gates, Label: "gates for you"},
	}
	if cs.Gates > 0 {
		d.Figures[2].Tone = "you"
	}

	d.Floor = WbFloor{Crates: cs.Crates, Legend: wbCrateKeys(), Bar: WbStatusBar{Segs: page.Bar, Side: page.Side}}
	for _, seg := range page.Bar {
		d.Floor.Bar.Total += seg.Count
	}
	d.Floor.Aria = fmt.Sprintf("%s: %d open, %d done", page.Title, page.Open, page.Done)

	d.Issues, d.Hidden = systemShelf(p, prefix, page, waits, workerOf, now)

	if fam == nil && (len(page.Families) > 0 || len(page.Epics) > 0) {
		f := &WbFamilies{Families: page.Families, Epics: page.Epics, System: g.Name}
		noun := "family"
		if len(page.Families) > 0 {
			f.Title, f.Source = "Families", "workbench.systems."+g.Name+".families"
		} else {
			f.Title, f.Source, noun = "Epics with issues here", "from parent: links", "epic"
		}
		f.Note = fmt.Sprintf("%s in no %s.", plural(page.Unfamilied, "open issue is", "open issues are"), noun)
		d.Families = f
	}

	d.Decision = systemDecisions(p, prefix, issues, members, page)

	// The code map: every system's files, so shared files know their owners.
	code := newCodeIndex(ciRootFor(p), latestHealth(p, now))
	for _, other := range groups {
		code.chipsFor(other.Name, other.Config.Paths, other.Members)
	}
	d.Code = code.codeMapOf(g.Name, len(g.Config.Paths) > 0, latestHealth(p, now) != nil)

	if page.DesignDoc != "" && page.DocInherited {
		d.DocNote = "the system's design doc"
	}
	if m, ok := page.Metrics.(*MetricsPanel); ok {
		d.Metrics = m
	}
	if k, ok := page.Knobs.(*KnobsPanel); ok {
		d.Knobs = k
	}
	d.BalanceHref = page.BalanceHref

	if page.Waiting.Count > 0 {
		d.Links = append(d.Links, WbLink{Label: plural(page.Waiting.Count, "item waits", "items wait") + " on you", URL: page.Waiting.Href})
	}
	d.Links = append(d.Links,
		WbLink{Label: "All issues", URL: prefix + "/?system=" + url.QueryEscape(g.Name)},
		WbLink{Label: "On the line", URL: prefix + "/hall"},
	)
	if page.DesignDoc != "" {
		d.Links = append(d.Links, WbLink{Label: "Design doc", URL: page.DesignDocHref})
	}
	return d
}

// systemShelf is the page's issue rows (buildSystemPage's, open first and
// capped) as crate cards.
func systemShelf(p *tracker.Project, prefix string, page SystemPageData, waits map[string][]WaitingItem, workerOf map[string]AgentNode, now time.Time) (WbShelf, string) {
	var open []*tracker.Issue
	for _, r := range page.Rows {
		if !r.Closed {
			open = append(open, r.Issue)
		}
	}
	hc := newHallContext(p, prefix, open, nil, nil, now)
	shelf := WbShelf{Label: fmt.Sprintf("Issues · %d", page.Total), Status: "the list"}
	for _, r := range page.Rows {
		key := strings.ToLower(r.Issue.Slug)
		c := WbCrateCard{Title: r.Issue.Title, Slug: r.Issue.Slug, URL: r.Href, Status: r.Issue.Status, ShowStatus: true, CI: hc.blamed[key], YOLO: hc.marked[key]}
		if c.Title == "" {
			c.Title = c.Slug
		}
		if ws := waits[key]; len(ws) > 0 {
			c.You = waitingWords(ws[0])
		}
		if w, ok := workerOf[key]; ok && !r.Closed {
			rb := workerRobot(w)
			c.Robot = &rb
		}
		if r.Epic != "" {
			c.Tags = append(c.Tags, WbLink{Label: "epic · " + r.Epic})
		}
		for _, m := range r.Markers {
			// The owner's items already show as the You chip.
			if m.Tone == "amber" {
				continue
			}
			c.Tags = append(c.Tags, WbLink{Label: m.Label, URL: m.Href})
		}
		shelf.Crates = append(shelf.Crates, c)
	}
	if len(page.Rows) < page.Total {
		shelf.More = page.Total - len(page.Rows)
		shelf.MoreURL = page.ListHref
	}
	return shelf, page.HiddenNote
}

// systemDecisions is the members' latest decisions as decision-log rows.
func systemDecisions(p *tracker.Project, prefix string, issues, members []*tracker.Issue, page SystemPageData) WbSysDecisions {
	d := WbSysDecisions{Total: page.DecisionsTotal, URL: page.DecisionsHref}
	in := map[*tracker.Issue]bool{}
	for _, it := range members {
		in[it] = true
	}
	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(p))
	for _, e := range index.Entries { // newest first
		if !in[e.Issue] {
			continue
		}
		if len(d.Rows) == recentCap {
			break
		}
		row := decisionsRow(e, prefix)
		row.Confirm = nil // confirmed on the decisions log, where the CEO's state is known
		if !e.Date.IsZero() {
			row.Date = e.Date.Format("2006-01-02")
		}
		d.Rows = append(d.Rows, row)
	}
	if len(d.Rows) == 0 {
		d.Hint = "No decisions yet. Decisions recorded with issue-cli decide show up here."
		if n := page.DecisionsHint; n > 0 {
			d.Hint = fmt.Sprintf("%s a ## Decisions section, but none uses the indexed line - **D-n** · date · how: text. Decisions recorded with issue-cli decide show up here.", plural(n, "issue keeps", "issues keep"))
		}
	}
	return d
}

// wbTone maps a classic chip tone (green, red, amber, blue, muted) to a
// Workshop Board chip tone.
func wbTone(tone string) string {
	switch tone {
	case "green", "red":
		return tone
	case "amber":
		return "you"
	case "blue":
		return "agent"
	}
	return "idle"
}

// wbVerdictTone is a metric verdict's chip tone.
func wbVerdictTone(v string) string {
	switch v {
	case tracker.VerdictOK:
		return "green"
	case tracker.VerdictNear:
		return "you"
	case tracker.VerdictOff:
		return "red"
	}
	return "idle"
}

// wbVerdictText is a metric verdict in words.
func wbVerdictText(v string) string {
	switch v {
	case tracker.VerdictOK:
		return "ok"
	case tracker.VerdictNear:
		return "near"
	case tracker.VerdictOff:
		return "off"
	case tracker.VerdictUnmeasured:
		return "not measured"
	}
	return "no verdict"
}
